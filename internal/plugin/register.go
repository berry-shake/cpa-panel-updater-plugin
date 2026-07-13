package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/berry-shake/cliproxy-panel-updater/internal/updater"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	statusPath = "/v0/resource/plugins/panel-updater/status"
	updatePath = "/v0/resource/plugins/panel-updater/update"
	panelPath  = "/v0/resource/plugins/panel-updater/panel"
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
}

type rpcCapabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type rpcRegistration struct {
	SchemaVersion uint32             `json:"schema_version"`
	Metadata      pluginapi.Metadata `json:"metadata"`
	Capabilities  rpcCapabilities    `json:"capabilities"`
}

type resourceDeclaration struct {
	Path        string
	Menu        string
	Description string
}

type managementRegistration struct {
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
		return okEnvelope(rpcRegistration{
			SchemaVersion: pluginabi.SchemaVersion,
			Metadata: pluginapi.Metadata{
				Name:             "Panel Updater",
				Version:          s.version,
				Author:           "berry-shake",
				GitHubRepository: "https://github.com/berry-shake/cliproxy-panel-updater",
				ConfigFields:     []pluginapi.ConfigField{},
			},
			Capabilities: rpcCapabilities{ManagementAPI: true},
		})
	case pluginabi.MethodPluginShutdown:
		return okEnvelope(struct{}{})
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration{
			Resources: []resourceDeclaration{
				{Path: "/panel", Menu: "Panel Updater", Description: "Manually update the CLIProxyAPI management panel."},
				{Path: "/status", Description: "Show the configured panel repository and local management.html state."},
				{Path: "/update", Description: "Download and atomically replace management.html."},
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
