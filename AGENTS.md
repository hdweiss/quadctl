# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## What this is

`quadctl` is a compose-like CLI for running Podman Quadlets, either directly via `podman` or installed into systemd's quadlet generator directories. It's a single Go module with no external services — read `README.md` for the full user-facing feature set (directory layout conventions, `.quadlets`/`.kube` file handling, symbolic-link install mode, etc.) before making behavioral changes, since a lot of nuance lives there rather than in code comments.

## Commands

```bash
go build -o quadctl main.go   # or ./build.sh — same thing
go test -v ./...              # run all tests
go test -v ./util/...         # test a single package
go test -v -run TestVolumeQuadletOptionsToPodmanTableDriven ./util/   # single test
go vet ./...
```

CI (`.github/workflows/go.yml`) just runs `go build -v ./...` and `go test -v ./...` on push/PR to `main`. There is no linter configured.

Running the built binary requires a `quadctl.ini` config (auto-created at `$HOME/.config/quadctl` on first run, template embedded from `util/config/quadctl.ini`); most subcommands additionally expect to find quadlet files in a search directory (CWD, an explicit path arg, or `quadlet.src.path`), so ad hoc `go run . <cmd>` invocations from an empty directory will hit the "no quadlets found" selector prompt.

## Architecture

Four packages, each with a distinct responsibility:

- **`main.go`** — entry point. Builds the initial `util.Quadctl` state struct, then runs a fixed pipeline: parse flags → load config → parse the subcommand → load quadlet schemas → discover/parse quadlet files → dispatch to a `Handle*` function in `core` based on `quadctl.Subcommand` and `quadctl.IsSystemd`.
- **`util`** — cross-cutting state and I/O:
  - `Quadctl` (in `util/parser.go`) is the mutable state struct threaded through the whole run: parsed flags, config values, and systemd command templates. `Quadlet` is a single parsed quadlet file (sections as `map[string]map[string][]string`, plus resolved `Deps`/`ParentPod`/`ServiceName`).
  - `parser.go` — discovers quadlet files under a directory, parses INI-style sections, extracts inter-quadlet dependencies (pod membership, `Requires=`/`After=`, volume/network refs) and topologically sorts them so `core` can process/start things in the right order. Also parses `.quadlets` (combined) and `.kube` (+ companion k8s YAML) files.
  - `flags.go` — defines one `flag.FlagSet` per subcommand (pull/create/start/run/stop/remove/status/ps/stats/images/list/logs) and the corresponding `Print*Usage` help text. Adding a subcommand flag means editing both this file and `main.go`'s switch.
  - `files.go` / `config` — config file loading (`GetConfig`/`InitConfig`), default config install, and filesystem helpers (copy/link/list) used when installing quadlets to systemd's generator directories.
  - `tui.go` — a small Bubble Tea list picker used when no quadlet directory is specified.
- **`schema`** — a hand-built, declarative model of every Quadlet/Podman option (`container.go`, `pod.go`, `network.go`, `volume.go`, `kube.go`, `build.go`, `image.go`). Each `opt*()` function returns a `SchemaOption` describing one key: its Quadlet-file spelling, its Podman CLI equivalent, a Go `text/template` for rendering each into command args, and a validator. `validator.go` implements the regex/format validation referenced by schema options. This schema is what lets `core` translate parsed quadlet key/values into the right `podman <verb>` arguments — when Podman/Quadlet adds or changes an option, this is where support gets added, not in `core`.
- **`core`** — command execution:
  - `commands.go` — the generic `Command` struct (spinner + run + output capture) and `RunCommands`, the shared execution/print-only/verbose-warnings loop used by every handler.
  - `handlers.go` — one `Handle*` pair per subcommand: a non-systemd version that shells out to `podman` directly (walking the sorted quadlet list and calling `generateCreateCommand`/`generateStartupCommand`/etc., which consult the `schema` package to build args) and a `HandleSystemd*` version that installs/removes quadlet files under `QuadletRootPath`/`QuadletUserPath` and drives `systemctl`/`journalctl` via the configurable templates on `Quadctl`.

### Key control flow

`quadctl.IsSystemd` (the `-s`/`-systemd` flag) is the fork point almost everywhere: the same logical operation (`create`, `start`, `stop`, `remove`, `status`, `logs`) has two implementations in `core/handlers.go` — one driving `podman` directly with dependency ordering computed by `quadctl` itself, the other installing quadlet files and delegating ordering/lifecycle to systemd. `ps`, `stats`, `images` have no systemd variant — they always inspect live podman state, filtered down to resources belonging to the discovered quadlets.

Rootful vs. rootless (`quadctl.IsRootful`) is derived from `os.Geteuid() == 0`, not a flag — it's what decides which systemd path (`QuadletUserPath` vs `QuadletRootPath`) and `--user` templating gets used.
