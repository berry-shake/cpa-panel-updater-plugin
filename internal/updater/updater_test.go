package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeDoer struct {
	mu        sync.Mutex
	responses map[string]HTTPResponse
	errors    map[string]error
	calls     []HTTPRequest
	fn        func(HTTPRequest) (HTTPResponse, error)
}

func (f *fakeDoer) Do(_ context.Context, _ string, req HTTPRequest) (HTTPResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	fn := f.fn
	resp := f.responses[req.URL]
	err := f.errors[req.URL]
	f.mu.Unlock()
	if fn != nil {
		return fn(req)
	}
	return resp, err
}

func (f *fakeDoer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func releaseBody(t *testing.T, downloadURL, digest string) []byte {
	t.Helper()
	raw, errMarshal := json.Marshal(map[string]any{
		"assets": []map[string]string{{
			"name":                 AssetName,
			"browser_download_url": downloadURL,
			"digest":               digest,
		}},
	})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	return raw
}

func TestResolveReleaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"", DefaultReleaseURL},
		{"https://github.com/acme/panel", "https://api.github.com/repos/acme/panel/releases/latest"},
		{"https://github.com/acme/panel.git/", "https://api.github.com/repos/acme/panel/releases/latest"},
		{"https://api.github.com/repos/acme/panel", "https://api.github.com/repos/acme/panel/releases/latest"},
		{"https://api.github.com/repos/acme/panel/releases/latest", "https://api.github.com/repos/acme/panel/releases/latest"},
		{"https://example.com/acme/panel", DefaultReleaseURL},
		{"not a url", DefaultReleaseURL},
	}

	for _, tt := range tests {
		if got := ResolveReleaseURL(tt.input); got != tt.want {
			t.Errorf("ResolveReleaseURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestUpdateDownloadsAndAtomicallyReplacesPanel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := []byte("old panel")
	if errWrite := os.WriteFile(filepath.Join(dir, AssetName), old, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	fresh := []byte("new panel")
	downloadURL := "https://downloads.example/management.html"
	releaseURL := ResolveReleaseURL("https://github.com/acme/panel")
	doer := &fakeDoer{responses: map[string]HTTPResponse{
		releaseURL:  {StatusCode: http.StatusOK, Body: releaseBody(t, downloadURL, "sha256:"+testSHA256(fresh))},
		downloadURL: {StatusCode: http.StatusOK, Body: fresh},
	}}

	result, errUpdate := New(doer).Update(context.Background(), UpdateRequest{
		StaticDir:             dir,
		PanelGitHubRepository: "https://github.com/acme/panel",
		HostCallbackID:        "callback-1",
	})
	if errUpdate != nil {
		t.Fatal(errUpdate)
	}
	if !result.Updated || result.Source != SourceGitHub || result.Hash != testSHA256(fresh) {
		t.Fatalf("result = %+v", result)
	}
	got, errRead := os.ReadFile(filepath.Join(dir, AssetName))
	if errRead != nil {
		t.Fatal(errRead)
	}
	if string(got) != string(fresh) {
		t.Fatalf("panel = %q, want %q", got, fresh)
	}
}

func TestUpdateSkipsDownloadWhenDigestMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	current := []byte("current panel")
	if errWrite := os.WriteFile(filepath.Join(dir, AssetName), current, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	releaseURL := ResolveReleaseURL(DefaultPanelRepository)
	doer := &fakeDoer{responses: map[string]HTTPResponse{
		releaseURL: {
			StatusCode: http.StatusOK,
			Body:       releaseBody(t, "https://downloads.example/management.html", "sha256:"+testSHA256(current)),
		},
	}}

	result, errUpdate := New(doer).Update(context.Background(), UpdateRequest{
		StaticDir:             dir,
		PanelGitHubRepository: DefaultPanelRepository,
	})
	if errUpdate != nil {
		t.Fatal(errUpdate)
	}
	if result.Updated || result.Source != SourceUpToDate {
		t.Fatalf("result = %+v", result)
	}
	if doer.callCount() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.callCount())
	}
}

func TestUpdateFailsWithoutWritingWhenGitHubFailsAndLocalFileIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, AssetName)
	releaseURL := ResolveReleaseURL(DefaultPanelRepository)
	doer := &fakeDoer{errors: map[string]error{releaseURL: errors.New("github unavailable")}}

	result, errUpdate := New(doer).Update(context.Background(), UpdateRequest{
		StaticDir:             dir,
		PanelGitHubRepository: DefaultPanelRepository,
	})
	if errUpdate == nil || !strings.Contains(errUpdate.Error(), "github unavailable") {
		t.Fatalf("error = %v, want github failure", errUpdate)
	}
	if result != (Result{}) {
		t.Fatalf("result = %+v, want zero value", result)
	}
	if _, errStat := os.Stat(path); !errors.Is(errStat, os.ErrNotExist) {
		t.Fatalf("panel file stat error = %v, want not exist", errStat)
	}
	if doer.callCount() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.callCount())
	}
}

func TestUpdateLeavesExistingFileWhenGitHubFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, AssetName)
	old := []byte("old panel")
	if errWrite := os.WriteFile(path, old, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	releaseURL := ResolveReleaseURL(DefaultPanelRepository)
	doer := &fakeDoer{errors: map[string]error{releaseURL: errors.New("github unavailable")}}

	_, errUpdate := New(doer).Update(context.Background(), UpdateRequest{
		StaticDir:             dir,
		PanelGitHubRepository: DefaultPanelRepository,
	})
	if errUpdate == nil {
		t.Fatal("Update returned nil error")
	}
	got, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if string(got) != string(old) {
		t.Fatalf("panel changed to %q", got)
	}
	if doer.callCount() != 1 {
		t.Fatalf("HTTP calls = %d, want 1", doer.callCount())
	}
}

func TestUpdateRejectsDigestMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	old := []byte("old panel")
	path := filepath.Join(dir, AssetName)
	if errWrite := os.WriteFile(path, old, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	downloadURL := "https://downloads.example/management.html"
	releaseURL := ResolveReleaseURL(DefaultPanelRepository)
	doer := &fakeDoer{responses: map[string]HTTPResponse{
		releaseURL:  {StatusCode: http.StatusOK, Body: releaseBody(t, downloadURL, "sha256:"+testSHA256([]byte("expected")))},
		downloadURL: {StatusCode: http.StatusOK, Body: []byte("tampered")},
	}}

	_, errUpdate := New(doer).Update(context.Background(), UpdateRequest{
		StaticDir:             dir,
		PanelGitHubRepository: DefaultPanelRepository,
	})
	if !errors.Is(errUpdate, ErrDigestMismatch) {
		t.Fatalf("error = %v, want ErrDigestMismatch", errUpdate)
	}
	got, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatal(errRead)
	}
	if string(got) != string(old) {
		t.Fatalf("panel changed to %q", got)
	}
	if doer.callCount() != 2 {
		t.Fatalf("HTTP calls = %d, want 2", doer.callCount())
	}
}

func TestUpdateRejectsConcurrentAttempt(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	current := []byte("current")
	if errWrite := os.WriteFile(filepath.Join(dir, AssetName), current, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	doer := &fakeDoer{fn: func(req HTTPRequest) (HTTPResponse, error) {
		once.Do(func() { close(started) })
		<-release
		return HTTPResponse{StatusCode: http.StatusOK, Body: releaseBody(t, req.URL, "sha256:"+testSHA256(current))}, nil
	}}
	instance := New(doer)
	firstDone := make(chan error, 1)
	go func() {
		_, errUpdate := instance.Update(context.Background(), UpdateRequest{StaticDir: dir})
		firstDone <- errUpdate
	}()
	<-started

	_, errSecond := instance.Update(context.Background(), UpdateRequest{StaticDir: dir})
	if !errors.Is(errSecond, ErrUpdateInProgress) {
		t.Fatalf("second error = %v, want ErrUpdateInProgress", errSecond)
	}
	close(release)
	if errFirst := <-firstDone; errFirst != nil {
		t.Fatal(errFirst)
	}
}
