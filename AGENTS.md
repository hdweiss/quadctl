# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## What this is

`quadctl` is a compose-like CLI for running Podman Quadlets, either directly via `podman` or installed into systemd's quadlet generator directories. It's a single Go module with no external services — read `README.md` for the full user-facing feature set (directory layout conventions, `.quadlets`/`.kube` file handling, symbolic-link install mode, etc.) before making behavioral changes, since a lot of nuance lives there rather than in code comments.

`TODO.md` (known defects), `PLAN.md` (the phased refactor and its ordering constraints) and `FEATURES.md` (ideas, not scheduled) are the working docs. Check `TODO.md` before reporting a bug as new.

## Running the binary safely

**Read this before invoking `quadctl` for any reason.** This tool mutates the host: it copies and deletes files under the systemd quadlet generator directories, starts and stops systemd units, and removes podman containers, volumes and networks. The machine you are working on is likely to have real quadlets installed and running. Several open defects in `TODO.md` make destructive behavior easy to trigger by accident — most notably §1's "`create|start <file>` wipes the whole generator directory".

Non-negotiable when running the binary:

- **Never run against the real config.** Every invocation sets `QUADCTL_CONFIG_DIR` to a throwaway config directory whose `quadlet.src.path`, `quadlet.user.path` and `quadlet.root.path` all point inside your own scratch directory.
- **Use `-p` (print mode) for `create`, `start`, `run`, `stop` and `remove`.** These generate real `podman`/`systemctl` invocations otherwise. Do not run them without `-p`.
- **Never write to or delete from** `/etc/containers/systemd`, `~/.config/containers/systemd`, or `~/.local/quadlets`. Build fixture quadlets in your scratch directory instead.
- **Mind the working directory.** When the CWD contains no quadlets, most commands silently widen their scope to *every* quadlet under `quadlet.src.path` (the `WidensWhenEmpty` rows in `registry.go`) — so `quadctl pull` run from the repo root will pull every image on the system. `cd` into a scratch fixture directory first.

Building (`go build`), testing (`go test`) and reading are unrestricted. If verifying a change appears to require breaking one of the rules above, stop and ask rather than working around it.

Setting up a scratch config:

```bash
SCRATCH=<your scratch dir>
mkdir -p "$SCRATCH"/{cfg,src,user,root}
sed -e "s|^quadlet.src.path=.*|quadlet.src.path=$SCRATCH/src|" \
    -e "s|^quadlet.user.path=.*|quadlet.user.path=$SCRATCH/user|" \
    -e "s|^quadlet.root.path=.*|quadlet.root.path=$SCRATCH/root|" \
    -e "s|^systemd.enabled.*|systemd.enabled=false|" \
    util/config/quadctl.ini > "$SCRATCH/cfg/quadctl.ini"
# then: QUADCTL_CONFIG_DIR=$SCRATCH/cfg ./quadctl <cmd> -p
```

The three path substitutions also remove the `{{.home}}` placeholders the shipped template carries (normally expanded when the default config is installed). Leave the `{{.user}}` variables in the `systemd.*` lines alone — those are expanded at runtime to `--user` when rootless. Setting `systemd.enabled=false` is what exercises the podman-direct path; there is currently no CLI flag to turn systemd mode off once the config enables it (`TODO.md` §3), so this is the only way to test that path.

## Commands

```bash
go build -o quadctl .         # or ./build.sh — same thing
go test -v ./...              # run all tests
go test -v ./util/...         # test a single package
go test -v -run TestVolumeQuadletOptionsToPodmanTableDriven ./util/   # single test
go vet ./...
```

CI (`.github/workflows/go.yml`) just runs `go build -v ./...` and `go test -v ./...` on push/PR to `main`. There is no linter configured. `go vet ./...` currently fails on `schema/validator.go:181-182` (`PLAN.md` 2.3) — that failure is pre-existing, not something you introduced.

Tests live beside the code with fixtures in `testdata/`. `.gitignore` is anchored to the
repo root (`/*.container`, …) precisely so those fixtures can be committed — don't widen it
back. `core/testdata/commands.golden` records the exact argv the generators produce today,
defects included; regenerate deliberately with `go test ./core/ -run TestGenerateCommandsGolden -update`
and review the diff.

Running the built binary requires a `quadctl.ini` config (auto-created at `$HOME/.config/quadctl` on first run, template embedded from `util/config/quadctl.ini`); most subcommands additionally expect to find quadlet files in a search directory (CWD, an explicit path arg, or `quadlet.src.path`), so ad hoc `go run . <cmd>` invocations from an empty directory will hit the "no quadlets found" selector prompt.

## Architecture

Four packages, each with a distinct responsibility:

- **`main.go`** — entry point. `main()` is `os.Exit(run())`; `run()` builds the initial `util.Quadctl` state struct, then runs a fixed pipeline: parse global flags → load config → resolve the subcommand and its flags → load quadlet schemas → discover/parse quadlet files → dispatch. **Nothing below `main` calls `os.Exit`** — every failure returns an error and `run()` is the only place that picks an exit code (`PLAN.md` 1.2). Keep it that way.
- **`registry.go`** — the whole command line in one table (`PLAN.md` 2.2). Each row is a subcommand: its name and aliases, the flags it accepts (drawn from the flag catalogue at the top of the file, so a flag reads the same way everywhere), its help text, whether an empty search directory should prompt or widen to `quadlet.src.path`, and the `Run`/`RunSystemd` handlers in `core` it dispatches to. `RunSystemd == nil` means `-s` makes no difference to that subcommand. **Adding or changing a subcommand means editing this table and nothing else** — there is no second switch to keep in sync, and global flags are registered on every subcommand's `FlagSet`, so `-s` works on either side of the subcommand.
- **`util`** — cross-cutting state and I/O:
  - `Quadctl` (in `util/parser.go`) is the mutable state struct threaded through the whole run: parsed flags, config values, and systemd command templates. `Quadlet` is a single parsed quadlet file (sections as `map[string]map[string][]string`, plus resolved `Deps`/`ParentPod`/`ServiceName`).
  - `parser.go` — discovers quadlet files under a directory, parses INI-style sections, extracts inter-quadlet dependencies (pod membership, `Requires=`/`After=`, volume/network refs) and topologically sorts them so `core` can process/start things in the right order. Also parses `.quadlets` (combined) and `.kube` (+ companion k8s YAML) files.
  - `flags.go` — `ErrUsage` and `ResolveSearchDir`, which turns the optional path argument into the absolute search directory. The flag sets themselves live in `registry.go`.
  - `files.go` / `config` — config file loading (`GetConfig`/`InitConfig`), default config install, and filesystem helpers (copy/link/list) used when installing quadlets to systemd's generator directories.
  - `tui.go` — a small Bubble Tea list picker used when no quadlet directory is specified.
  - `runner.go` — `Runner`, the single seam between quadctl and the host. Everything that
    shells out goes through the `Runner` on the run state: `ExecRunner` in production,
    `RecordingRunner` in tests, which records invocations and answers from a canned table.
    Do not reach for `exec.Command` directly in `core` or `util`.
- **`schema`** — a hand-built, declarative model of every Quadlet/Podman option (`container.go`, `pod.go`, `network.go`, `volume.go`, `kube.go`, `build.go`, `image.go`). Each `opt*()` function returns a `SchemaOption` describing one key: its Quadlet-file spelling, its Podman CLI equivalent, a Go `text/template` for rendering each into command args, and a validator. `validator.go` implements the regex/format validation referenced by schema options. This schema is what lets `core` translate parsed quadlet key/values into the right `podman <verb>` arguments — when Podman/Quadlet adds or changes an option, this is where support gets added, not in `core`.
- **`core`** — command execution. One `Handle*` pair per subcommand, each returning
  `([]Command, error)` (or just `error` for the ones that print rather than generate
  commands): a non-systemd version that shells out to `podman` directly (walking the sorted
  quadlet list and calling `generateCreateCommand`/`generateStartupCommand`/etc., which
  consult the `schema` package to build args) and a `HandleSystemd*` version that
  installs/removes quadlet files under `QuadletRootPath`/`QuadletUserPath` and drives
  `systemctl`/`journalctl` via the configurable templates on `Quadctl`. One file per
  subcommand (`PLAN.md` 2.1):
  - `command.go` — the generic `Command` struct (spinner + run + output capture) and `RunCommands`, the shared execution/print-only/verbose-warnings loop used by every handler. `RunCommands` returns the process exit code: 0, or the failing command's own status. `pull`/`create`/`start`/`run` abort at the first failure; teardown and query subcommands run everything and report at the end.
  - `pull.go`, `create.go`, `start.go`, `run.go`, `stop.go`, `remove.go` — the podman-direct handlers.
  - `systemd_install.go` — `HandleSystemdCreate`/`HandleSystemdRemove`, stale-file pruning, and the podman quadlet generator dry-run validation.
  - `systemd_lifecycle.go` — `HandleSystemdStart`/`Stop`/`Status`/`Logs`/`Reload` and the systemd command templating.
  - `inspect.go` — `HandlePS`/`HandleStats`/`HandleImages`/`HandleLogs`. `list.go` — the `list`/`ls` tree.
  - `generate.go` — the four command generators, the only place quadlet key/values become podman argv.
  - `podman.go` — the read-only podman/systemctl queries (`resourceExists`, `getContainerPS`, `listSystemdInstalledQuadlets`).

### Key control flow

Command generation is deterministic: the section loops in the generators, the dependency
set, and the topological-sort seed are all sorted (`PLAN.md` 1.3). Map iteration order must
not leak into generated commands — the golden test will catch it if it does.

`quadctl.IsSystemd` (the `-s`/`-systemd` flag) is the fork point almost everywhere: the same logical operation (`create`, `start`, `stop`, `remove`, `status`, `logs`) has two implementations in `core/handlers.go` — one driving `podman` directly with dependency ordering computed by `quadctl` itself, the other installing quadlet files and delegating ordering/lifecycle to systemd. `ps`, `stats`, `images` have no systemd variant — they always inspect live podman state, filtered down to resources belonging to the discovered quadlets.

Rootful vs. rootless (`quadctl.IsRootful`) is derived from `os.Geteuid() == 0`, not a flag — it's what decides which systemd path (`QuadletUserPath` vs `QuadletRootPath`) and `--user` templating gets used.
