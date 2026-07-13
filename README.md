# CLIProxyAPI Panel Updater Plugin

A CLIProxyAPI ManagementAPI plugin for manually updating the built-in
`management.html` control panel.

The plugin reads `remote-management.panel-github-repository` directly from
the same host configuration file selected by `--config`. Downloads use the
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
remote-management:
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
        version: 0.1.1
        release-tag: v0.1.1
        repository: https://github.com/berry-shake/cliproxy-panel-updater
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

No plugin-specific repository setting is required or supported.

## Configuration

| Key | Type | Description |
| --- | --- | --- |
| `management_key` | string | Optional. Plaintext management key prefilled into the panel page so it does not have to be typed each time. See the security notes before enabling it. |

```yaml
plugins:
  enabled: true
  configs:
    panel-updater:
      enabled: true
      management_key: "<remote management secret key>"
```

## Use

Start CLIProxyAPI with its normal config argument:

```bash
./cli-proxy-api --config config.yaml
```

Open:

```text
http://127.0.0.1:<port>/v0/resource/plugins/panel-updater/panel
```

Enter the remote management secret key, then select **Check status** or
**Update now**. The key is sent only in the `Authorization` header and stored
in the browser's localStorage for that origin; it is not written by the
plugin.

When `management_key` is configured, the key field is prefilled and nothing
needs to be typed or maintained in the page. Manually-typed keys are then no
longer saved to localStorage.

Authenticated API endpoints:

```text
GET  /v0/management/plugins/panel-updater/status
POST /v0/management/plugins/panel-updater/update
```

Example:

```bash
curl -H 'Authorization: Bearer <management-key>' \
  http://127.0.0.1:8317/v0/management/plugins/panel-updater/status

curl -X POST -H 'Authorization: Bearer <management-key>' \
  http://127.0.0.1:8317/v0/management/plugins/panel-updater/update
```

## Update behavior

1. Read `remote-management.panel-github-repository` from the active host
   config (`--config`, `-config`, or the default `./config.yaml`).
2. Resolve the same static directory used by CLIProxyAPI:
   `MANAGEMENT_STATIC_PATH`, then `WRITABLE_PATH/static`, then
   `<config-directory>/static`.
3. Fetch the latest GitHub release and locate the `management.html` asset.
4. Skip the download when the local SHA-256 already matches the release
   digest.
5. Verify the downloaded digest and atomically replace `management.html`.
6. If GitHub metadata or asset download fails while the local panel is
   missing, use `https://cpamc.router-for.me/` as an unverified fallback.
   Preserve an existing panel on GitHub failure. Digest mismatch never falls
   back and never replaces the current file.

Only one update can run inside the plugin at a time. A concurrent request
returns HTTP 409.

## Build locally

```bash
go test ./...
go build -buildmode=c-shared \
  -ldflags '-X main.pluginVersion=0.1.0-dev' \
  -o panel-updater-v0.1.0-dev.dylib .
```

Use `.so` on Linux and `.dll` on Windows. The c-shared build also produces a
C header; the host does not need it.

## Security notes

- The browser page is public, like all CLIProxyAPI plugin resources, but it
  cannot read status or run updates without the management key.
- Setting `management_key` embeds the plaintext key into the page HTML so it
  can be prefilled. Anyone who can open the panel URL (including proxies
  that expose it publicly) will be able to read that key from the page
  source. Only enable it in environments where the panel URL itself is
  trusted, or leave it empty to keep entering the key manually per browser.
- The plugin never logs the management key.
- GitHub release digests are verified before replacement.
- The fallback page has no digest metadata; update responses clearly report
  `source: "fallback"` when it is used.
