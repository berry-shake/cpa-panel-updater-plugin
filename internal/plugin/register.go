package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/berry-shake/cliproxy-panel-updater/internal/updater"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const (
	statusPath          = "/v0/management/plugins/panel-updater/status"
	updatePath          = "/v0/management/plugins/panel-updater/update"
	panelPath           = "/v0/resource/plugins/panel-updater/panel"
	configManagementKey = "management_key"
)

// UpdateRunner abstracts the updater so management handlers can be tested with a fake.
type UpdateRunner interface {
	Update(ctx context.Context, req updater.UpdateRequest) (updater.Result, error)
}

// ConfigResolver returns the host configuration at call time so updates always see fresh values.
type ConfigResolver func() HostConfig

// Service dispatches plugin lifecycle and management RPC methods.
type Service struct {
	version  string
	runner   UpdateRunner
	resolver ConfigResolver

	mu            sync.RWMutex
	managementKey string
}

type pluginConfig struct {
	ManagementKey string `yaml:"management_key"`
}

type rpcLifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type rpcCapabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type rpcRegistration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  rpcCapabilities    `json:"capabilities"`
}

type routeDeclaration struct {
	Method      string
	Path        string
	Menu        string
	Description string
}

type resourceDeclaration struct {
	Path        string
	Menu        string
	Description string
}

type managementRegistration struct {
	Routes    []routeDeclaration    `json:"routes"`
	Resources []resourceDeclaration `json:"resources"`
}

type managementRPCRequest struct {
	Method         string
	Path           string
	Headers        http.Header
	Query          map[string][]string
	Body           []byte
	HostCallbackID string `json:"host_callback_id"`
}

func New(version string, runner UpdateRunner, resolver ConfigResolver) *Service {
	if strings.TrimSpace(version) == "" {
		version = "0.0.0-dev"
	}
	if resolver == nil {
		resolver = ResolveCurrentHostConfig
	}
	return &Service{version: version, runner: runner, resolver: resolver}
}

// Call handles one plugin RPC method and always returns a JSON envelope.
func (s *Service) Call(method string, request []byte) []byte {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		s.applyConfig(request)
		return okEnvelope(rpcRegistration{
			SchemaVersion: pluginabi.SchemaVersion,
			Metadata: pluginapi.Metadata{
				Name:             "Panel Updater",
				Version:          s.version,
				Author:           "berry-shake",
				GitHubRepository: "https://github.com/berry-shake/cliproxy-panel-updater",
				ConfigFields: []pluginapi.ConfigField{{
					Name:        configManagementKey,
					Type:        pluginapi.ConfigFieldTypeString,
					Description: "Optional plaintext management key prefilled into the panel page. Anyone who can open the public panel page can read it; leave empty to keep entering the key manually.",
				}},
			},
			Capabilities: rpcCapabilities{ManagementAPI: true},
		})
	case pluginabi.MethodPluginShutdown:
		return okEnvelope(struct{}{})
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration{
			Routes: []routeDeclaration{
				{Method: http.MethodGet, Path: "/plugins/panel-updater/status", Description: "Show the configured panel repository and local management.html state."},
				{Method: http.MethodPost, Path: "/plugins/panel-updater/update", Description: "Download and atomically replace management.html."},
			},
			Resources: []resourceDeclaration{
				{Path: "/panel", Menu: "Panel Updater", Description: "Manually update the CLIProxyAPI management panel."},
			},
		})
	case pluginabi.MethodManagementHandle:
		var req managementRPCRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return ErrorEnvelope("invalid_request", "decode management request: "+errUnmarshal.Error())
		}
		return okEnvelope(s.handleManagement(req))
	default:
		return ErrorEnvelope("unknown_method", "unknown method: "+method)
	}
}

// applyConfig replaces the plugin configuration from a lifecycle request payload.
// Invalid or empty payloads clear the configured key instead of keeping stale values.
func (s *Service) applyConfig(request []byte) {
	next := pluginConfig{}
	if len(bytes.TrimSpace(request)) > 0 {
		var req rpcLifecycleRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal == nil && len(bytes.TrimSpace(req.ConfigYAML)) > 0 {
			var cfg pluginConfig
			if errYAML := yaml.Unmarshal(req.ConfigYAML, &cfg); errYAML == nil {
				next = cfg
			}
		}
	}
	s.mu.Lock()
	s.managementKey = strings.TrimSpace(next.ManagementKey)
	s.mu.Unlock()
}

func (s *Service) configuredManagementKey() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.managementKey
}

func okEnvelope(result any) []byte {
	rawResult, errMarshal := json.Marshal(result)
	if errMarshal != nil {
		return ErrorEnvelope("marshal_failed", errMarshal.Error())
	}
	raw, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: rawResult})
	return raw
}

// ErrorEnvelope builds a failure envelope for RPC and ABI initialization errors.
func ErrorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{
		OK: false,
		Error: &pluginabi.Error{
			Code:    code,
			Message: message,
		},
	})
	return raw
}
