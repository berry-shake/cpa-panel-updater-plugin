package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
)

const (
	AssetName              = "management.html"
	DefaultPanelRepository = "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
	DefaultReleaseURL      = "https://api.github.com/repos/router-for-me/Cli-Proxy-API-Management-Center/releases/latest"
	FallbackURL            = "https://cpamc.router-for.me/"
	SourceGitHub           = "github"
	SourceFallback         = "fallback"
	SourceUpToDate         = "up-to-date"
	userAgent              = "cliproxy-panel-updater"
	maxReleaseResponseSize = 2 << 20
	maxAssetDownloadSize   = 50 << 20
)

var (
	ErrUpdateInProgress = errors.New("panel update already in progress")
	ErrDigestMismatch   = errors.New("management asset digest mismatch")
)

// HTTPRequest describes one outbound request issued through the host callback.
type HTTPRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

// HTTPResponse describes the host callback response.
type HTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// HTTPDoer abstracts the host.http.do callback so tests can inject a fake.
type HTTPDoer interface {
	Do(ctx context.Context, hostCallbackID string, req HTTPRequest) (HTTPResponse, error)
}

// UpdateRequest carries the resolved host configuration for one update attempt.
type UpdateRequest struct {
	StaticDir             string
	PanelGitHubRepository string
	HostCallbackID        string
}

// Result reports the outcome of one update attempt.
type Result struct {
	Updated bool   `json:"updated"`
	Hash    string `json:"hash"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

// Updater downloads and atomically replaces the management panel asset.
type Updater struct {
	http       HTTPDoer
	inProgress atomic.Bool
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type releaseResponse struct {
	Assets []releaseAsset `json:"assets"`
}

func New(httpDoer HTTPDoer) *Updater {
	return &Updater{http: httpDoer}
}

// Update fetches the latest release asset and atomically replaces management.html.
func (u *Updater) Update(ctx context.Context, req UpdateRequest) (Result, error) {
	if u == nil || u.http == nil {
		return Result{}, errors.New("host HTTP callback is unavailable")
	}
	if !u.inProgress.CompareAndSwap(false, true) {
		return Result{}, ErrUpdateInProgress
	}
	defer u.inProgress.Store(false)

	staticDir := strings.TrimSpace(req.StaticDir)
	if staticDir == "" {
		return Result{}, errors.New("static directory is empty")
	}
	if errMkdir := os.MkdirAll(staticDir, 0o755); errMkdir != nil {
		return Result{}, fmt.Errorf("prepare static directory: %w", errMkdir)
	}

	localPath := filepath.Join(staticDir, AssetName)
	localHash, errHash := FileSHA256(localPath)
	if errHash != nil && !errors.Is(errHash, os.ErrNotExist) {
		return Result{}, fmt.Errorf("hash local management asset: %w", errHash)
	}

	result, errGitHub := u.updateFromGitHub(ctx, req, localPath, localHash)
	if errGitHub == nil {
		return result, nil
	}
	if errors.Is(errGitHub, ErrDigestMismatch) || localHash != "" {
		return Result{}, errGitHub
	}

	fallbackData, errFallback := u.get(ctx, req.HostCallbackID, FallbackURL, maxAssetDownloadSize, http.Header{
		"User-Agent": []string{userAgent},
	})
	if errFallback != nil {
		return Result{}, fmt.Errorf("github update failed: %v; fallback failed: %w", errGitHub, errFallback)
	}
	fallbackHash := hashBytes(fallbackData)
	if errWrite := atomicWriteFile(localPath, fallbackData); errWrite != nil {
		return Result{}, fmt.Errorf("persist fallback management asset: %w", errWrite)
	}
	return Result{
		Updated: true,
		Hash:    fallbackHash,
		Source:  SourceFallback,
		Message: "Management panel updated from the unverified fallback page because GitHub update failed.",
	}, nil
}

func (u *Updater) updateFromGitHub(ctx context.Context, req UpdateRequest, localPath, localHash string) (Result, error) {
	releaseURL := ResolveReleaseURL(req.PanelGitHubRepository)
	releaseData, errRelease := u.get(ctx, req.HostCallbackID, releaseURL, maxReleaseResponseSize, http.Header{
		"Accept":     []string{"application/vnd.github+json"},
		"User-Agent": []string{userAgent},
	})
	if errRelease != nil {
		return Result{}, fmt.Errorf("fetch latest release: %w", errRelease)
	}

	var release releaseResponse
	if errUnmarshal := json.Unmarshal(releaseData, &release); errUnmarshal != nil {
		return Result{}, fmt.Errorf("decode latest release: %w", errUnmarshal)
	}
	asset, remoteHash, errAsset := findManagementAsset(release.Assets)
	if errAsset != nil {
		return Result{}, errAsset
	}
	if remoteHash != "" && localHash != "" && strings.EqualFold(remoteHash, localHash) {
		return Result{
			Updated: false,
			Hash:    localHash,
			Source:  SourceUpToDate,
			Message: "Management panel is already up to date.",
		}, nil
	}

	data, errDownload := u.get(ctx, req.HostCallbackID, asset.BrowserDownloadURL, maxAssetDownloadSize, http.Header{
		"User-Agent": []string{userAgent},
	})
	if errDownload != nil {
		return Result{}, fmt.Errorf("download management asset: %w", errDownload)
	}
	downloadedHash := hashBytes(data)
	if remoteHash != "" && !strings.EqualFold(remoteHash, downloadedHash) {
		return Result{}, fmt.Errorf("%w: expected %s, got %s", ErrDigestMismatch, remoteHash, downloadedHash)
	}
	if errWrite := atomicWriteFile(localPath, data); errWrite != nil {
		return Result{}, fmt.Errorf("persist management asset: %w", errWrite)
	}
	return Result{
		Updated: true,
		Hash:    downloadedHash,
		Source:  SourceGitHub,
		Message: "Management panel updated from the configured GitHub repository.",
	}, nil
}

func (u *Updater) get(ctx context.Context, callbackID, target string, maxSize int, headers http.Header) ([]byte, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("request URL is empty")
	}
	resp, errDo := u.http.Do(ctx, callbackID, HTTPRequest{
		Method:  http.MethodGet,
		URL:     target,
		Headers: headers,
	})
	if errDo != nil {
		return nil, errDo
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET %s returned HTTP %d", target, resp.StatusCode)
	}
	if len(resp.Body) > maxSize {
		return nil, fmt.Errorf("GET %s returned %d bytes, limit is %d", target, len(resp.Body), maxSize)
	}
	return append([]byte(nil), resp.Body...), nil
}

// ResolveReleaseURL converts a configured repository value into a GitHub latest-release API URL.
func ResolveReleaseURL(repository string) string {
	repository = strings.TrimSpace(repository)
	if repository == "" {
		return DefaultReleaseURL
	}
	parsed, errParse := url.Parse(repository)
	if errParse != nil || parsed.Host == "" {
		return DefaultReleaseURL
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	switch strings.ToLower(parsed.Host) {
	case "api.github.com":
		if !strings.HasSuffix(strings.ToLower(parsed.Path), "/releases/latest") {
			parsed.Path += "/releases/latest"
		}
		return parsed.String()
	case "github.com":
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			repositoryName := strings.TrimSuffix(parts[1], ".git")
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", parts[0], repositoryName)
		}
	}
	return DefaultReleaseURL
}

func findManagementAsset(assets []releaseAsset) (releaseAsset, string, error) {
	for _, asset := range assets {
		if strings.EqualFold(asset.Name, AssetName) {
			return asset, parseDigest(asset.Digest), nil
		}
	}
	return releaseAsset{}, "", fmt.Errorf("management asset %s not found in latest release", AssetName)
}

func parseDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if index := strings.Index(digest, ":"); index >= 0 {
		digest = digest[index+1:]
	}
	return strings.ToLower(strings.TrimSpace(digest))
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FileSHA256 returns the lowercase hex SHA-256 of a file on disk.
func FileSHA256(path string) (string, error) {
	file, errOpen := os.Open(path)
	if errOpen != nil {
		return "", errOpen
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, errCopy := io.Copy(hash, file); errCopy != nil {
		return "", errCopy
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func atomicWriteFile(path string, data []byte) error {
	tmpFile, errCreate := os.CreateTemp(filepath.Dir(path), "management-*.html")
	if errCreate != nil {
		return errCreate
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()
	if _, errWrite := tmpFile.Write(data); errWrite != nil {
		return errWrite
	}
	if errChmod := tmpFile.Chmod(0o644); errChmod != nil {
		return errChmod
	}
	if errClose := tmpFile.Close(); errClose != nil {
		return errClose
	}
	return os.Rename(tmpName, path)
}
