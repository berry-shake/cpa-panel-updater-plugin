package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/berry-shake/cpa-panel-updater-plugin/internal/updater"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type configFileStatus struct {
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
	Error    string `json:"error,omitempty"`
}

type statusResponse struct {
	ConfigFile            configFileStatus `json:"config_file"`
	FilePath              string           `json:"file_path"`
	Exists                bool             `json:"exists"`
	Size                  int64            `json:"size"`
	ModifiedAt            string           `json:"modified_at,omitempty"`
	LocalSHA256           string           `json:"local_sha256,omitempty"`
	PanelGitHubRepository string           `json:"panel_github_repository"`
	ReleaseURL            string           `json:"release_url"`
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// handleManagement serves both route families. The panel page and logo are
// unauthenticated GET resources; status and update are /v0/management routes,
// and CPA checks the management key before forwarding them, so the plugin
// needs no origin or CSRF checks of its own.
func (s *Service) handleManagement(req managementRPCRequest) pluginapi.ManagementResponse {
	switch req.Path {
	case logoPath:
		if req.Method == http.MethodGet {
			return logoResponse()
		}
	case panelPath:
		if req.Method == http.MethodGet {
			return panelResponse(s.AllowedOrigins())
		}
	case statusPath:
		if req.Method == http.MethodGet {
			return s.status()
		}
	case updatePath:
		// POST only: replacing management.html changes state, so a plain
		// link or prefetch must never trigger it.
		if req.Method == http.MethodPost {
			return s.update(req.HostCallbackID)
		}
	default:
		return jsonResponse(http.StatusNotFound, errorBody{Error: "not_found", Message: "plugin route not found"})
	}
	return jsonResponse(http.StatusMethodNotAllowed, errorBody{Error: "method_not_allowed", Message: "method not allowed on this route"})
}

func (s *Service) status() pluginapi.ManagementResponse {
	config := s.resolver()
	filePath := filepath.Join(config.StaticDir, updater.AssetName)
	status := statusResponse{
		ConfigFile: configFileStatus{
			Path:     config.ConfigFile,
			Readable: config.ConfigReadable,
			Error:    config.ConfigError,
		},
		FilePath:              filePath,
		PanelGitHubRepository: config.PanelGitHubRepository,
		ReleaseURL:            updater.ResolveReleaseURL(config.PanelGitHubRepository),
	}

	info, errStat := os.Stat(filePath)
	if errors.Is(errStat, os.ErrNotExist) {
		return jsonResponse(http.StatusOK, status)
	}
	if errStat != nil {
		return jsonResponse(http.StatusInternalServerError, errorBody{Error: "stat_failed", Message: errStat.Error()})
	}
	localHash, errHash := updater.FileSHA256(filePath)
	if errHash != nil {
		return jsonResponse(http.StatusInternalServerError, errorBody{Error: "hash_failed", Message: errHash.Error()})
	}
	status.Exists = true
	status.Size = info.Size()
	status.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	status.LocalSHA256 = localHash
	return jsonResponse(http.StatusOK, status)
}

func (s *Service) update(hostCallbackID string) pluginapi.ManagementResponse {
	if s.runner == nil {
		return jsonResponse(http.StatusInternalServerError, errorBody{Error: "updater_unavailable", Message: "panel updater is unavailable"})
	}
	config := s.resolver()
	result, errUpdate := s.runner.Update(context.Background(), updater.UpdateRequest{
		StaticDir:             config.StaticDir,
		PanelGitHubRepository: config.PanelGitHubRepository,
		HostCallbackID:        hostCallbackID,
	})
	if errors.Is(errUpdate, updater.ErrUpdateInProgress) {
		return jsonResponse(http.StatusConflict, errorBody{Error: "update_in_progress", Message: errUpdate.Error()})
	}
	if errUpdate != nil {
		return jsonResponse(http.StatusInternalServerError, errorBody{Error: "update_failed", Message: errUpdate.Error()})
	}
	return jsonResponse(http.StatusOK, result)
}

func jsonResponse(statusCode int, value any) pluginapi.ManagementResponse {
	raw, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		statusCode = http.StatusInternalServerError
		raw = []byte(`{"error":"marshal_failed","message":"failed to encode response"}`)
	}
	return pluginapi.ManagementResponse{
		StatusCode: statusCode,
		Headers: http.Header{
			"Content-Type":  []string{"application/json; charset=utf-8"},
			"Cache-Control": []string{"no-store"},
		},
		Body: raw,
	}
}
