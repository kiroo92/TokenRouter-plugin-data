# TokenRouter Go plugin SDK

This module provides plugin lifecycle interfaces, host capability clients, protocol
bindings, an in-process test host, and the `trplugin` packaging command. It uses the
MIT license independently of the gateway.

## Persistent data in this distribution

The `kiroo92/TokenRouter-plugin-data` distribution publishes Go SDK v0.4.0 with
`sdk.Env.DataDir`. Its host v0.7.3 creates a persistent directory for each plugin
ID. Ordinary uninstall retains that directory; explicit data purge removes it.
`Env.StateDir` remains disposable cache storage.

Plugins choose their own SQLite driver and store the database under
`filepath.Join(env.DataDir, "plugin.sqlite")`. Check that `DataDir` is nonempty in
`Init` before opening files, and close database connections during `Shutdown`.
Older hosts leave the field empty. Set `minHostVersion` to at least `0.7.3` when
targeting this distribution; the same version number in another distribution does
not establish support for this field.

## Wallet consumption

Host v0.7.6 adds `UserUsageStats.balance_cost` to `Usage.Stats` responses. It is
the decimal cost paid from the wallet, including the wallet portion of a mixed
subscription request. Consumption rewards use this field to exclude subscription
spending. An empty value means that the host does not report funding splits;
consumers that award rewards require the host capability before settling them.

## Use the published module

The fork preserves the upstream module and import paths. In an independent plugin
project, select this distribution with a versioned replacement:

```sh
go mod edit -require=github.com/TokenFlux/TokenRouter/plugin-sdk/go@v0.2.1
go mod edit -replace=github.com/TokenFlux/TokenRouter/plugin-sdk/go=github.com/kiroo92/TokenRouter-plugin-data/plugin-sdk/go@v0.4.0
go mod edit -tool=github.com/TokenFlux/TokenRouter/plugin-sdk/go/cmd/trplugin
go mod tidy
go test ./...
go build ./cmd/plugin
```

Keep imports under `github.com/TokenFlux/TokenRouter/plugin-sdk/go`. Commit both
`go.mod` and `go.sum`. This replacement selects SDK v0.4.0 from the personal
repository; it does not publish a version in the TokenFlux module namespace.

The official template's Makefile runs `trplugin` with an explicit older version.
Use the selected module for packaging by setting its Makefile default to:

```make
TRPLUGIN ?= go tool trplugin
```

The tool declaration keeps packaging dependencies in `go.mod` and `go.sum` when
`go mod tidy` runs. `go tool trplugin` honors the project's replacement, including
when the template's release workflow invokes `make test pack`.

## Test local files

`plugintest.New(t).Env` supplies separate, writable `DataDir` and `StateDir`
directories for each harness. Register plugin shutdown with `t.Cleanup` after
creating the harness so plugin-owned file handles close before testing removes the
directories. These fixtures do not simulate the real host's update, uninstall,
backup, or restore operations.

The browser Web SDK remains unchanged. Frontend pages access plugin-owned files
through authenticated plugin HTTP handlers; filesystem paths stay in the backend.
