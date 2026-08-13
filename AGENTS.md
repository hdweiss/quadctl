# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## What this is

`quadctl` is a compose-like CLI for running Podman Quadlets, either directly via `podman` or installed into systemd's quadlet generator directories. It's a single Go module with no external services — read `README.md` for the full user-facing feature set (directory layout conventions, `.quadlets`/`.kube` file handling, symbolic-link install mode, etc.) before making behavioral changes, since a lot of nuance lives there rather than in code comments.

The working docs live in `docs/`: `TODO.md` (known defects), `PLAN.md` (the phased refactor and its ordering constraints) and `FEATURES.md` (ideas, not scheduled). They are referred to by bare filename throughout this file and in code comments. Check `TODO.md` before reporting a bug as new.

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
    internal/config/quadctl.ini > "$SCRATCH/cfg/quadctl.ini"
# then: QUADCTL_CONFIG_DIR=$SCRATCH/cfg ./quadctl <cmd> -p
```

The three path substitutions also remove the `{{.home}}` placeholders the shipped template carries (normally expanded when the default config is installed). Leave the `{{.user}}` variables in the `systemd.*` lines alone — those are expanded at runtime to `--user` when rootless. Setting `systemd.enabled=false` is what exercises the podman-direct path. `--no-systemd` now overrides a config that enables it, so a throwaway config with `systemd.enabled=true` is a fine way to test both paths — passing `-s` and `--no-systemd` together is an error, not a silent winner.

## Commands

```bash
make                          # list the targets
make check                    # build + vet + lint + test -race — what CI runs, in CI's order
make build                    # ./quadctl, with version information stamped in
make test                     # go test -race ./...
make lint                     # go tool golangci-lint run
make golden                   # regenerate internal/command/testdata/commands.golden
make snapshot                 # cross-build every release archive into ./dist, publish nothing

go test -v ./internal/quadlet/...   # test a single package
go test -v -run TestVolumeQuadletOptionsToPodmanTableDriven ./internal/podman/   # single test
```

CI (`.github/workflows/go.yml`) runs build, `go vet ./...`, `go tool golangci-lint run` and `go test -race -v ./...` on every branch and PR; `build_release.yml` calls the same workflow, so a tag that fails any of it doesn't publish. All four are clean — keep them that way.

`golangci-lint` and `goreleaser` are pinned as `go.mod` `tool` dependencies, so `go tool <name>` is the same version CI uses and there is nothing to install. Two consequences worth knowing: `go.mod`/`go.sum` are large because those tools' transitive graphs are recorded there, and **after any `go get -tool` you must run `go mod tidy` and rebuild** — a new tool's graph can move a shared dependency and leave `go.sum` incomplete for an existing one, which surfaces as a "missing go.sum entry" in a package you never touched.

Tests live beside the code with fixtures in `testdata/`. `.gitignore` is anchored to the
repo root (`/*.container`, …) precisely so those fixtures can be committed — don't widen it
back. `internal/command/testdata/commands.golden` records the exact argv the generators produce today,
defects included; regenerate deliberately with `go test ./internal/command/ -run TestGenerateCommandsGolden -update`
and review the diff.

Running the built binary requires a `quadctl.ini` config (auto-created at `$HOME/.config/quadctl` on first run, template embedded from `internal/config/quadctl.ini`); most subcommands additionally expect to find quadlet files in a search directory (CWD, an explicit path arg, or `quadlet.src.path`), so ad hoc `go run . <cmd>` invocations from an empty directory will hit the "no quadlets found" selector prompt.

## Architecture

One `main` package at the repo root and eight packages under `internal/`, each named for its
subject rather than its role in the build (`PLAN.md` 6.1 — there is no `core` and no `util`).
`internal/` is deliberate (`PLAN.md` 2.5): quadctl is a CLI with no library consumers, so
nothing here is importable from outside the module and any exported signature is free to
change.

Dependencies run one way: `main` → `command`/`systemd` → `podman` → `quadlet` →
`config`/`runner`/`schema`. `systemd` imports `command`; nothing imports `systemd` but `main`.

- **`main.go`** — entry point. `main()` is `os.Exit(run())`; `run()` builds the initial `quadlet.State`, then runs a fixed pipeline: parse global flags → load config → resolve the subcommand and its flags → load quadlet schemas → discover/parse quadlet files → dispatch. **Nothing below `main` calls `os.Exit`** — every failure returns an error and `run()` is the only place that picks an exit code (`PLAN.md` 1.2). Keep it that way.
- **`registry.go`** — the whole command line in one table (`PLAN.md` 2.2). Each row is a subcommand: its name and aliases, the flags it accepts (drawn from the flag catalogue at the top of the file, so a flag reads the same way everywhere), its help text, whether an empty search directory should prompt or widen to `quadlet.src.path`, and the `Run`/`RunSystemd` handlers it dispatches to (in `internal/command` and `internal/systemd` respectively). `RunSystemd == nil` means `-s` makes no difference to that subcommand. `toolName` and `errUsage` live here too. **Adding or changing a subcommand means editing this table and nothing else** — there is no second switch to keep in sync, and global flags are registered on every subcommand's `FlagSet`, so `-s` works on either side of the subcommand.
- **`internal/quadlet`** — quadlet files and the run over them.
  - `Quadlet` is a single parsed quadlet file. `Sections` (`map[string]map[string][]string`) holds each assignment **exactly as written** — one entry per line, nothing tokenized (`PLAN.md` 3.1). Turning those lines into values is `OptionValues`' job, and it needs the schema: a repeatable option splits on whitespace, a single-valued one keeps its line and the last assignment wins. `ParseFields` is the one place quoting is resolved, so what leaves it is argv. Don't split values anywhere else.
  - `state.go` — `State`, the per-run struct threaded through everything below `main`: parsed flags, the subcommand, the directory being acted on, the `Runner`. `State.Cleanup` removes the scratch directories `.quadlets` bundles were extracted into; `main` defers it, and nothing else should call it, because the systemd install commands read from them when they run.
  - `parser.go` — discovers quadlet files under a directory, parses INI-style sections, resolves each resource's podman name, extracts inter-quadlet dependencies (pod membership, `Requires=`/`After=`, volume/network refs) and topologically sorts them so the handlers can process/start things in the right order. Also parses `.quadlets` (combined) and `.kube` (+ companion k8s YAML) files.
  - `search.go` — `ResolveSearchDir`, which turns the optional path argument into the absolute search directory. The flag sets themselves live in `registry.go`.
- **`internal/config`** — `Config`, `DefaultConfig` and `LoadConfig`: locating and reading `quadctl.ini`, and installing the embedded default (`quadctl.ini` in that directory) when there is none. Config is what the *user* configured — paths, install behavior, the systemd command templates — read once and **treated as read-only** (`PLAN.md` 3.2); writing to it changes the meaning of the next directory in the same run. `files.go` holds the filesystem helpers (copy/list/write) used when installing quadlets to systemd's generator directories.
- **`internal/runner`** — `Runner`, the single seam between quadctl and the host. Everything that
  shells out goes through the `Runner` on the run state: `ExecRunner` in production,
  `RecordingRunner` in tests, which records invocations and answers from a canned table.
  `RunStreaming`/`RunSilent`/`RunCaptured` are the side shell-outs a handler makes while still
  building its command list. Do not reach for `exec.Command` anywhere else.
- **`internal/tui`** — a small Bubble Tea list picker used when no quadlet directory is specified, or to choose a service when several could supply logs.
- **`internal/schema`** — a hand-built, declarative model of every Quadlet/Podman option (`container.go`, `pod.go`, `network.go`, `volume.go`, `kube.go`, `build.go`, `image.go`). Each `opt*()` function returns a `SchemaOption` describing one key: its Quadlet-file spelling, its Podman CLI equivalent, a Go `text/template` for rendering each into command args, and a validator regex (filled in by `PopulateValidators`, and currently unused — see `FEATURES.md`'s `validate` command). `options.go` indexes those slices by key (`QuadletOptions`, `AllQuadletOptions`). When Podman/Quadlet adds or changes an option, this is where support gets added.
- **`internal/podman`** — everything quadctl knows about podman itself. `options.go` — `OptionArgs`, which renders one quadlet key/value into podman argv using the schema's templates. `query.go` — the read-only questions (`ResourceExists`, `ContainerPS`) a handler asks while deciding what to build.
- **`internal/command`** — the podman-direct handlers and the machinery every handler shares. One `Handle*` per subcommand, each returning `([]Command, error)` (or just `error` for the ones that print rather than generate commands), walking the sorted quadlet list and shelling out to `podman`. One file per subcommand (`PLAN.md` 2.1):
  - `command.go` — the generic `Command` struct (spinner + run + output capture) and `RunCommands`, the shared execution/print-only/verbose-warnings loop used by every handler in both packages. `RunCommands` returns the process exit code: 0, or the failing command's own status. `pull`/`create`/`start`/`run` abort at the first failure; teardown and query subcommands run everything and report at the end.
  - `pull.go`, `create.go`, `start.go`, `run.go`, `stop.go`, `remove.go` — the podman-direct handlers.
  - `inspect.go` — `HandlePS`/`HandleStats`/`HandleImages`/`HandleLogs`. `list.go` — the `list`/`ls` tree.
  - `generate.go` — the four command generators, the only place quadlet key/values become podman argv.
  - `output.go` — `Label`, `TableStyle`, `UseColor`, `InfoPrefix` and the spinner/TTY decisions. The exported ones are what `internal/systemd` uses so both halves' output reads the same.
- **`internal/systemd`** — the `-s` half of every subcommand that has one: instead of driving podman, it installs quadlet files under `QuadletRootPath`/`QuadletUserPath` and drives `systemctl`/`journalctl` via the configurable templates on `Config`. It builds the same `command.Command` values.
  - `install.go` — `HandleCreate`/`HandleRemove`, stale-file pruning, and the podman quadlet generator dry-run validation.
  - `lifecycle.go` — `HandleStart`/`Stop`/`Status`/`Logs`/`Reload` and the systemd command templating.
  - `list.go` — `podman quadlet list`, with a `systemctl show`/`is-active` fallback for podman versions that predate it.

### Key control flow

Command generation is deterministic: the section loops in the generators, the dependency
set, and the topological-sort seed are all sorted (`PLAN.md` 1.3). Map iteration order must
not leak into generated commands — the golden test will catch it if it does.

`quadctl.IsSystemd` (the `-s`/`-systemd` flag) is the fork point almost everywhere: the same logical operation (`create`, `start`, `stop`, `remove`, `status`, `logs`) has one implementation in `internal/command` and another in `internal/systemd` — one driving `podman` directly with dependency ordering computed by `quadctl` itself, the other installing quadlet files and delegating ordering/lifecycle to systemd. `ps`, `stats`, `images` have no systemd variant — they always inspect live podman state, filtered down to resources belonging to the discovered quadlets.

Rootful vs. rootless (`quadctl.IsRootful`) is derived from `os.Geteuid() == 0`, not a flag — it's what decides which systemd path (`QuadletUserPath` vs `QuadletRootPath`) and `--user` templating gets used.
