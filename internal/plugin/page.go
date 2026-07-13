package plugin

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

//go:embed page.html
var panelPage []byte

// panelKeyPlaceholder is replaced with a JSON string literal at serve time.
var panelKeyPlaceholder = []byte(`"__CONFIGURED_MANAGEMENT_KEY__"`)

func panelResponse(managementKey string) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Content-Type":            []string{"text/html; charset=utf-8"},
			"Cache-Control":           []string{"no-store"},
			"X-Content-Type-Options":  []string{"nosniff"},
			"Content-Security-Policy": []string{"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'"},
		},
		Body: renderPanelPage(managementKey),
	}
}

func renderPanelPage(managementKey string) []byte {
	// json.Marshal escapes <, >, and & so the value cannot break out of
	// the inline script block.
	encoded, errMarshal := json.Marshal(managementKey)
	if errMarshal != nil {
		encoded = []byte(`""`)
	}
	return bytes.Replace(append([]byte(nil), panelPage...), panelKeyPlaceholder, encoded, 1)
}
