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

1. Download the release asset matching the host platform.
2. Remove the platform suffix from the filename. For example:

   ```text
   panel-updater-v0.1.0-linux-amd64.so
   → panel-updater-v0.1.0.so
   ```

3. Put it in the preferred platform directory:

   ```text
   plugins/linux/amd64/panel-updater-v0.1.0.so
   plugins/linux/arm64/panel-updater-v0.1.0.so
   plugins/darwin/amd64/panel-updater-v0.1.0.dylib
   plugins/darwin/arm64/panel-updater-v0.1.0.dylib
   plugins/windows/amd64/panel-updater-v0.1.0.dll
   ```

4. Enable the plugin in the CLIProxyAPI host configuration:

   ```yaml
   remote-management:
     panel-github-repository: https://github.com/router-for-me/Cli-Proxy-API-Management-Center

   plugins:
     enabled: true
     configs:
       panel-updater:
         enabled: true
   ```

No plugin-specific repository setting is required or supported.

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
- The plugin never logs the management key or embeds it in HTML.
- GitHub release digests are verified before replacement.
- The fallback page has no digest metadata; update responses clearly report
  `source: "fallback"` when it is used.
