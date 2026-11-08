# CLIProxyAPI Panel Updater Plugin

A CLIProxyAPI plugin for manually updating the built-in `management.html`
control panel from a browser page. The status and update endpoints are
`/v0/management` routes, so they require the CPA management key.

The plugin reads `management.panel-github-repository` (legacy files:
`remote-management.panel-github-repository`) directly from the same host
configuration file selected by `--config`. Downloads use the
host's `host.http.do` callback, so the host's `proxy-url` and request logging
behavior apply automatically.

## Requirements

- CLIProxyAPI v7.2.71 or newer with dynamic plugin support
- Linux amd64/arm64, macOS amd64/arm64, or Windows amd64
- cgo-enabled CLIProxyAPI build on Unix platforms

## Install

### Automatic (plugin store)

Release assets follow the CLIProxyAPI plugin store layout
(`panel-updater_<version>_<goos>_<goarch>.zip` plus `checksums.txt`), so the
host can install the plugin from GitHub releases when a `store` block is
configured:

```yaml
management:
  panel-github-repository: https://github.com/router-for-me/Cli-Proxy-API-Management-Center

plugins:
  enabled: true
  configs:
    panel-updater:
      enabled: true
      store:
        id: panel-updater
        name: Panel Updater
        description: Manually update the management center panel (management.html).
        author: berry-shake
        version: 0.2.5
        release-tag: v0.2.5
        repository: https://github.com/berry-shake/cpa-panel-updater-plugin
        install:
          type: github-release
```

### Manual

1. Download the zip matching the host platform and extract
   `panel-updater.<so|dylib|dll>`.
2. Put it in the preferred platform directory:

   ```text
   plugins/linux/amd64/panel-updater.so
   plugins/linux/arm64/panel-updater.so
   plugins/darwin/amd64/panel-updater.dylib
   plugins/darwin/arm64/panel-updater.dylib
   plugins/windows/amd64/panel-updater.dll
   ```

3. Enable the plugin in the CLIProxyAPI host configuration:

   ```yaml
   plugins:
     enabled: true
     configs:
       panel-updater:
         enabled: true
   ```

## Configuration

| Key | Type | Description |
| --- | --- | --- |
| `allowed_origins` | string | Comma-separated list of extra origins (e.g. `https://admin.example, https://ops.example`) that may embed the panel page in an iframe (CSP `frame-ancestors`). Empty (default) allows only same-origin embedding, which is what the CPA management panel needs. The status and update endpoints always require the management key regardless of this setting. |

Example:

```yaml
plugins:
  enabled: true
  configs:
    panel-updater:
      enabled: true
      allowed_origins: "https://admin.example.com, https://ops.example.com"
```

Entries are trimmed of whitespace and trailing slashes and deduplicated, then
appended after `'self'`. When `allowed_origins` is empty, the CSP is
`frame-ancestors 'self'`.

## Use

Start CLIProxyAPI with its normal config argument:

```bash
./cli-proxy-api --config config.yaml
```

Open:

```text
http://127.0.0.1:<port>/v0/resource/plugins/panel-updater/panel
```

Opened from the **Panel Updater** menu inside the CPA management panel, the
page reuses the management key the panel remembered ("remember password"),
in either the official panel or the CPAMP storage format. It only uses a key
saved for this origin and never writes it back. Opened directly, or when the
host rejects the saved key, the page asks for the key; it stays in page
memory only, and a reload asks again.

The page loads the current `management.html` status on open. Click
**Refresh status** to check again, then **Update now** to pull the latest
release and atomically replace the file.

The page is localized in English and Simplified Chinese. It picks the
default language from `navigator.language` (any `zh-*` tag selects Chinese;
everything else falls back to English), and the choice can be overridden
with the EN/中文 toggle in the header. The selection is remembered in
`localStorage` under `cliproxy-panel-updater-lang`.

Endpoints:

```text
GET  /v0/resource/plugins/panel-updater/panel      page shell, no auth, no data
GET  /v0/management/plugins/panel-updater/status   management key required
POST /v0/management/plugins/panel-updater/update   management key required
```

The host's management middleware checks `Authorization: Bearer <management key>`
(plus its remote-management and IP ban rules) before forwarding the request,
so a missing or wrong key gets 401/403 from the host itself. Example:

```bash
curl -H "Authorization: Bearer $MANAGEMENT_KEY" \
  http://127.0.0.1:8317/v0/management/plugins/panel-updater/status
curl -X POST -H "Authorization: Bearer $MANAGEMENT_KEY" \
  http://127.0.0.1:8317/v0/management/plugins/panel-updater/update
```

## Update behavior

1. Read `management.panel-github-repository` from the active host config
   (`--config`, `-config`, or the default `./config.yaml`). As in
   CLIProxyAPI, the v8 field wins whenever present; legacy files without it
   fall back to `remote-management.panel-github-repository`.
2. Resolve the same static directory used by CLIProxyAPI:
   `MANAGEMENT_STATIC_PATH`, then `WRITABLE_PATH/static`, then
   `<config-directory>/static`.
3. Fetch the latest GitHub release and locate the `management.html` asset.
4. Skip the download when the local SHA-256 already matches the release
   digest.
5. Verify the downloaded digest and atomically replace `management.html`.
6. If GitHub metadata or asset download fails, or the digest does not match,
   the update fails and nothing is written: an existing panel is kept, and a
   missing panel stays missing. There is no unverified fallback source.

Only one update can run inside the plugin at a time. A concurrent request
returns HTTP 409.

## Build locally

```bash
go test ./...
go build -buildmode=c-shared \
  -ldflags '-X main.pluginVersion=0.2.5-dev' \
  -o panel-updater-v0.2.5-dev.dylib .
```

Use `.so` on Linux and `.dll` on Windows. The c-shared build also produces a
C header; the host does not need it.

## Security notes

- Reading status and triggering an update both require the CPA management
  key: they are `/v0/management` routes guarded by the host. Only the page
  shell and logo are public `/v0/resource` assets, and they contain no data.
- Update is POST only, so a link, image tag or prefetch cannot trigger it.
- The panel page sends `frame-ancestors 'self'` (plus any `allowed_origins`),
  so other sites cannot embed it.
- Update content is constrained by design: the plugin only replaces
  `management.html` with the digest-verified asset from the repository
  configured in `management.panel-github-repository`. It never accepts
  arbitrary URLs, file paths, or content from the request.
- GitHub release digests are verified before replacement. The plugin never
  downloads the panel from any other source.
- Do not expose the CLIProxyAPI HTTP port to untrusted networks without
  additional protection (reverse proxy auth, IP allow-list, VPN, etc.).
