# Development

## Prerequisites

- Go 1.26+
- `git`
- macOS or Linux (this project is developed on macOS and deployed to
  Linux — see "Compiling for Linux" in the root `README.md`)

## Getting started

```bash
git clone https://github.com/BirdRa1n/screenlet-player.git
cd screenlet-player
go run ./cmd/screenlet-player
```

This starts the local control API on `:8089` with a `NoopPlayer` backend
(no real video output yet — see `docs/ROADMAP.md`). Useful flags:

```bash
go run ./cmd/screenlet-player -version       # print version and exit
go run ./cmd/screenlet-player -addr :9000    # change the control API port
```

## Project layout

See `docs/ARCHITECTURE.md` for the full component map. The short version:
`cmd/screenlet-player` wires together one implementation of each
`internal/*` interface. Most `internal/*` packages currently only define
an interface (`playback.Player`, `sync.Syncer`, `telemetry.Reporter`,
`updater.Checker`, `display.Detector`) plus, where one exists yet, a real
implementation (`storage`, `device`, `playback.NoopPlayer`, `api`).

## Testing conventions

- Tests live next to the code they cover (`foo.go` → `foo_test.go`), no
  separate `tests/` tree.
- Packages that touch the filesystem (`storage`, `device`) sandbox it with
  `t.Setenv("XDG_CONFIG_HOME", t.TempDir())` — never write to the real
  `~/.config` from a test.
- The HTTP API (`internal/api`) is tested with `net/http/httptest`
  against a real `playback.NoopPlayer`, not a mock — assert on actual
  responses, not recorded calls.
- Run the full suite the same way CI does:

  ```bash
  gofmt -l .              # must print nothing
  go vet ./...
  go test -race -v ./...
  go build ./...
  ```

## Adding a new `internal/` package

1. Define the interface first (see `playback.Player` or `sync.Syncer` for
   the style: small, behavior-focused, documented with a package comment
   explaining *why* it exists, not just what it does).
2. Add a real or `Noop`-style implementation if one is ready; otherwise
   leave the interface for a later milestone and note it in
   `docs/ROADMAP.md`.
3. Wire it into `cmd/screenlet-player/main.go` only once it has a real
   implementation — `main.go` should never import an interface it can't
   yet satisfy meaningfully.
4. Add tests in the same package, following the conventions above.

## Release builds

Tagging `vX.Y.Z` and pushing the tag triggers
`.github/workflows/release.yml`, which builds the full platform matrix and
publishes a GitHub Release. To reproduce that build locally, use
`scripts/build.sh`.
