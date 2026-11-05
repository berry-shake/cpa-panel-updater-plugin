package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/berry-shake/cpa-panel-updater-plugin/internal/updater"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type fakeRunner struct {
	result updater.Result
	err    error
	got    updater.UpdateRequest
}

func (f *fakeRunner) Update(_ context.Context, req updater.UpdateRequest) (updater.Result, error) {
	f.got = req
	return f.result, f.err
}

func decodeEnvelopeResult[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var envelope pluginabi.Envelope
	if errUnmarshal := json.Unmarshal(raw, &envelope); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if !envelope.OK {
		t.Fatalf("envelope error: %+v", envelope.Error)
	}
	var out T
	if errUnmarshal := json.Unmarshal(envelope.Result, &out); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	return out
}

func managementRequest(t *testing.T, method, path, callbackID string) []byte {
	t.Helper()
	raw, errMarshal := json.Marshal(managementRPCRequest{
		Method:         method,
		Path:           path,
		HostCallbackID: callbackID,
	})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	return raw
}

func lifecycleRequest(t *testing.T, configYAML string) []byte {
	t.Helper()
	raw, errMarshal := json.Marshal(rpcLifecycleRequest{ConfigYAML: []byte(configYAML)})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	return raw
}

func TestRegisterAdvertisesManagementAPIAndAllowedOriginsField(t *testing.T) {
	t.Parallel()

	service := New("1.2.3", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	registration := decodeEnvelopeResult[rpcRegistration](t, service.Call(pluginabi.MethodPluginRegister, nil))
	if registration.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1 for a host that advertises none", registration.SchemaVersion)
	}
	if registration.Metadata.Version != "1.2.3" || registration.Metadata.Name != "Panel Updater" {
		t.Fatalf("Metadata = %+v", registration.Metadata)
	}
	if len(registration.Metadata.ConfigFields) != 1 ||
		registration.Metadata.ConfigFields[0].Name != configAllowedOrigins ||
		registration.Metadata.ConfigFields[0].Type != pluginapi.ConfigFieldTypeString {
		t.Fatalf("ConfigFields = %+v, want single %s string field", registration.Metadata.ConfigFields, configAllowedOrigins)
	}
	if !registration.Capabilities.ManagementAPI {
		t.Fatal("management_api capability is false")
	}
}

func TestRegisterNegotiatesSchemaVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload []byte
		want    uint32
	}{
		{name: "no payload", payload: nil, want: 1},
		{name: "host without schema", payload: []byte(`{"config_yaml":null}`), want: 1},
		{name: "older host", payload: []byte(`{"schema_version":3}`), want: 3},
		{name: "host at pinned schema", payload: []byte(`{"schema_version":6}`), want: 6},
		{name: "newer host", payload: []byte(`{"schema_version":10,"multi_auth_providers":true}`), want: maxSchemaVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
			for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
				registration := decodeEnvelopeResult[rpcRegistration](t, service.Call(method, tt.payload))
				if registration.SchemaVersion != tt.want {
					t.Fatalf("%s schema_version = %d, want %d", method, registration.SchemaVersion, tt.want)
				}
			}
		})
	}
}

func TestRegisterAppliesAndReconfigureClearsAllowedOrigins(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	decodeEnvelopeResult[rpcRegistration](t, service.Call(
		pluginabi.MethodPluginRegister,
		lifecycleRequest(t, "enabled: true\nallowed_origins: \"https://a.example, https://b.example/, https://a.example\"\n"),
	))
	if got := service.AllowedOrigins(); len(got) != 2 || got[0] != "https://a.example" || got[1] != "https://b.example" {
		t.Fatalf("AllowedOrigins() = %v, want deduped and trimmed [a b]", got)
	}

	decodeEnvelopeResult[rpcRegistration](t, service.Call(
		pluginabi.MethodPluginReconfigure,
		lifecycleRequest(t, "enabled: true\n"),
	))
	if got := service.AllowedOrigins(); len(got) != 0 {
		t.Fatalf("AllowedOrigins() = %v, want empty after reconfigure", got)
	}
}

func TestManagementRegisterSplitsResourcesAndManagementRoutes(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	raw := service.Call(pluginabi.MethodManagementRegister, []byte(`{}`))
	registration := decodeEnvelopeResult[managementRegistration](t, raw)
	paths := map[string]string{}
	for _, res := range registration.Resources {
		paths[res.Path] = res.Menu
	}
	if len(registration.Resources) != 2 || paths["/panel"] != "Panel Updater" {
		t.Fatalf("resources = %+v, want /panel menu entry and /logo.png", registration.Resources)
	}
	if menu, ok := paths["/logo.png"]; !ok || menu != "" {
		t.Fatalf("/logo.png must have empty Menu to stay out of sidebar submenu, got %+v", registration.Resources)
	}
	for _, private := range []string{"/status", "/update"} {
		if _, ok := paths[private]; ok {
			t.Fatalf("%q must not be an unauthenticated resource", private)
		}
	}

	// Decode with the host's shape: CPA reads routes into pluginapi.ManagementRoute.
	host := decodeEnvelopeResult[struct {
		Routes []pluginapi.ManagementRoute `json:"routes"`
	}](t, raw)
	got := map[string]bool{}
	for _, route := range host.Routes {
		got[route.Method+" "+route.Path] = true
	}
	if len(host.Routes) != 2 || !got[http.MethodGet+" "+statusPath] || !got[http.MethodPost+" "+updatePath] {
		t.Fatalf("routes = %+v, want GET %s and POST %s", host.Routes, statusPath, updatePath)
	}
}

func TestPanelPageCSPReflectsAllowedOrigins(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })

	// No origins configured → only the same-origin management panel may embed.
	response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodGet, panelPath, ""),
	))
	csp := response.Headers.Get("Content-Security-Policy")
	if !strings.HasSuffix(csp, "frame-ancestors 'self'") {
		t.Fatalf("csp without origins = %q", csp)
	}

	// Configure origins → they are added after 'self'.
	decodeEnvelopeResult[rpcRegistration](t, service.Call(
		pluginabi.MethodPluginReconfigure,
		lifecycleRequest(t, "allowed_origins: \"https://admin.example, https://ops.example\"\n"),
	))
	response = decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodGet, panelPath, ""),
	))
	csp = response.Headers.Get("Content-Security-Policy")
	if !strings.HasSuffix(csp, "frame-ancestors 'self' https://admin.example https://ops.example") {
		t.Fatalf("csp with origins = %q", csp)
	}
}

func TestStatusReturnsHostConfigAndLocalPanelMetadata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	panel := []byte("panel")
	path := filepath.Join(dir, updater.AssetName)
	if errWrite := os.WriteFile(path, panel, 0o644); errWrite != nil {
		t.Fatal(errWrite)
	}
	service := New("dev", &fakeRunner{}, func() HostConfig {
		return HostConfig{
			ConfigFile:            "/etc/cliproxy/config.pro.yaml",
			ConfigReadable:        true,
			PanelGitHubRepository: "https://github.com/acme/panel",
			StaticDir:             dir,
		}
	})

	response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodGet, statusPath, "callback-status"),
	))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, body = %s", response.StatusCode, response.Body)
	}
	var status statusResponse
	if errUnmarshal := json.Unmarshal(response.Body, &status); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if !status.ConfigFile.Readable || status.ConfigFile.Path != "/etc/cliproxy/config.pro.yaml" {
		t.Fatalf("config_file = %+v", status.ConfigFile)
	}
	if !status.Exists || status.Size != int64(len(panel)) || status.LocalSHA256 == "" {
		t.Fatalf("status = %+v", status)
	}
	if status.ReleaseURL != "https://api.github.com/repos/acme/panel/releases/latest" {
		t.Fatalf("ReleaseURL = %q", status.ReleaseURL)
	}
}

func TestUpdateForwardsRepositoryDirectoryAndCallbackID(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{result: updater.Result{
		Updated: true,
		Hash:    "abc",
		Source:  updater.SourceGitHub,
		Message: "updated",
	}}
	service := New("dev", runner, func() HostConfig {
		return HostConfig{
			PanelGitHubRepository: "https://github.com/acme/panel",
			StaticDir:             "/var/lib/cliproxy/static",
		}
	})

	response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodPost, updatePath, "callback-update"),
	))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, body = %s", response.StatusCode, response.Body)
	}
	if runner.got.StaticDir != "/var/lib/cliproxy/static" || runner.got.PanelGitHubRepository != "https://github.com/acme/panel" || runner.got.HostCallbackID != "callback-update" {
		t.Fatalf("update request = %+v", runner.got)
	}
}

func TestUpdateMapsBusyAndOtherErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "busy", err: updater.ErrUpdateInProgress, want: http.StatusConflict},
		{name: "failure", err: errors.New("disk denied"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := New("dev", &fakeRunner{err: tt.err}, func() HostConfig { return HostConfig{StaticDir: "/tmp/static"} })
			response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
				pluginabi.MethodManagementHandle,
				managementRequest(t, http.MethodPost, updatePath, "callback"),
			))
			if response.StatusCode != tt.want {
				t.Fatalf("StatusCode = %d, want %d", response.StatusCode, tt.want)
			}
		})
	}
}

func TestManagementHandleRejectsWrongMethodAndUnknownPath(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	service := New("dev", runner, func() HostConfig { return HostConfig{} })
	tests := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: updatePath, want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: statusPath, want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: panelPath, want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: logoPath, want: http.StatusMethodNotAllowed},
		// The old unauthenticated resource paths must be gone.
		{method: http.MethodGet, path: "/v0/resource/plugins/panel-updater/status", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/v0/resource/plugins/panel-updater/update", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/v0/management/plugins/panel-updater/nope", want: http.StatusNotFound},
	}
	for _, tt := range tests {
		response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
			pluginabi.MethodManagementHandle,
			managementRequest(t, tt.method, tt.path, "cb"),
		))
		if response.StatusCode != tt.want {
			t.Fatalf("%s %s: StatusCode = %d, want %d, body=%s", tt.method, tt.path, response.StatusCode, tt.want, response.Body)
		}
	}
	if runner.got != (updater.UpdateRequest{}) {
		t.Fatalf("rejected requests must not run the updater, got %+v", runner.got)
	}
}

func TestPanelPageUsesManagementRoutes(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodGet, panelPath, ""),
	))
	body := string(response.Body)
	for _, expected := range []string{statusPath, updatePath, "Authorization", "'Bearer '", "cli-proxy-auth", "method: 'POST'"} {
		if !strings.Contains(body, expected) {
			t.Errorf("page does not contain %q", expected)
		}
	}
	for _, stale := range []string{
		"/v0/resource/plugins/panel-updater/status",
		"/v0/resource/plugins/panel-updater/update",
	} {
		if strings.Contains(body, stale) {
			t.Errorf("page still references %q", stale)
		}
	}
}

func TestPanelResourceEmbedsEnglishAndChineseDictionaries(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	response := decodeEnvelopeResult[pluginapi.ManagementResponse](t, service.Call(
		pluginabi.MethodManagementHandle,
		managementRequest(t, http.MethodGet, panelPath, ""),
	))
	body := string(response.Body)
	for _, expected := range []string{
		// English dictionary sentinels.
		"'hero.title': 'Panel Updater'",
		"'actions.update': 'Update now'",
		// Chinese dictionary sentinels.
		"'hero.title': '面板更新器'",
		"'actions.update': '立即更新'",
		// Language switcher buttons.
		`data-lang="en"`,
		`data-lang="zh"`,
		// Persistence and default-detection hooks.
		"cliproxy-panel-updater-lang",
		"navigator.languages",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("page does not contain %q", expected)
		}
	}
}

func TestUnknownRPCMethodReturnsErrorEnvelope(t *testing.T) {
	t.Parallel()

	service := New("dev", &fakeRunner{}, func() HostConfig { return HostConfig{} })
	var envelope pluginabi.Envelope
	if errUnmarshal := json.Unmarshal(service.Call("unknown.method", nil), &envelope); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "unknown_method" {
		t.Fatalf("envelope = %+v", envelope)
	}
}
