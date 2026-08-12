# Refactor plan

Companion to `TODO.md` (the defect list) and `FEATURES.md` (the parking lot). This is the
order of operations: refactor, not rewrite, with two contained subsystem rewrites along
the way.

**Status:** Phases 0 through 3 are done. Next up is Phase 4.

## Principles

1. **Critical bug fixes are not gated behind the refactor.** The §1 items in `TODO.md` are
   a few lines each and one of them destroys data. They ship first, as small diffs, on the
   current structure.
2. **Seams before moves.** Nothing in `core`/`util` is testable today: it shells out
   directly, calls `os.Exit` from 54 places, and generates commands in random order. Fix
   those three things and the rest of the refactor has a net under it.
3. **Moves are pure moves.** When code relocates, nothing else changes in the same commit,
   so review is `git diff -M` and the eye can skip it.
4. **Each phase is independently shippable.** No phase leaves the tool in a worse state
   than it found it.

Sizes below are relative: **S** = a sitting, **M** = a day-ish, **L** = multi-day.

---

## Phase 0 — Stop the bleeding — **done**

Small, surgical fixes on the existing structure. No restructuring, no test infrastructure
yet — each is hand-verifiable with `-p` print mode. Cut a release at the end.

| # | Fix | Where | Verify |
|---|-----|-------|--------|
| 0.1 | Absolutize `SearchDir` in both branches; make `pruneStaleSystemdFiles` refuse when `dest == targetDir` or `Base(searchDir)` is `.`/`..`/empty | `util/flags.go:351,363`, `core/handlers.go:481` | `quadctl create -p <file>` must not print "Copying directory . to \<generator root\>" or any "Removing …" lines for unrelated quadlets |
| 0.2 | Read the `-f` target from the subcommand FlagSet; error when the named file isn't among the parsed quadlets | `util/parser.go:92` | `create -p -f aaa.container` in a two-container dir prints one create |
| 0.3 | Guard the four panics (nil pod lookup, `KubeDownForce[0]` ×2, `createCmd[3:]`, the two `.(string)` asserts) | `util/parser.go:564`, `core/handlers.go:259,1628,1644,1143,1778` | A `.container` with `Pod=missing.pod` errors cleanly instead of panicking |
| 0.4 | Track command failures; exit non-zero | `core/commands.go:126-160`, `main.go:122` | `quadctl stop` against a non-existent container exits 125-ish, not 0 |
| 0.5 | `&&` instead of `||` in the detached-run check (3 copies → 1 helper) | `core/commands.go:49,70,86` | `PodmanArgs=-d` no longer attaches stdio |
| 0.6 | Delete the debug `Printf` in `generateStartupCommand` | `core/handlers.go:1563` | `.kube` start output is clean |
| 0.7 | `text/template` instead of `html/template` | `main.go:5`, `util/parser.go:8`, `util/files.go:7` | A `systemd.logs` template containing quotes survives round-trip |

**Not** in Phase 0: the `Exec=` dropping bug. It's §1-critical but it's a symptom of the
value model, which gets rewritten in Phase 3 — patching it here means a per-key exception
that Phase 3 deletes. Interim mitigation: promote the "does not accept multiple values"
warning to default verbosity (`core/handlers.go:1481`) so it stops being *silent* while the
real fix lands. **S.**

**Done when:** all seven fixed, warning promoted, release tagged.

---

## Phase 1 — Carve the seams — **done**

The enabling work. Order matters: each unlocks the next.

**1.1 — Runner interface (M).** Replace direct `exec.Command` calls with a small interface
(`Run(args []string) (stdout string, err error)`) held on the run state. Real
implementation shells out; test implementation records invocations and returns canned
output. Touches `core/commands.go` (`DefaultRunFn`, `runCommand`, `runCommandSilently`,
`runCommandCapture`) and the direct `exec.Command` calls in `diagnoseFailedSystemctlCommand`
and `validateQuadletGenerationCommand`.
*Done when:* a test can assert on the exact `podman` argv a handler produces, without podman
installed.

**1.2 — Errors instead of `os.Exit` (M).** 54 call sites across `util/` and `core/`.
Handlers return `([]Command, error)`; `main` owns exit codes. Mechanical, large diff, no
behavior change beyond §3's exit-code inconsistencies disappearing.
*Done when:* `grep -c os.Exit util/ core/` is 0 and `main.go` has a single exit path.

**1.3 — Determinism (S).** Sort the map iterations in `generateCreateCommand`
(`core/handlers.go:1474`) and the topological-sort seed loop (`util/parser.go:631`).
*Done when:* the same input produces byte-identical `-p` output across 10 runs.

**1.4 — First tests (M).** Now possible, and targeted at where the bugs actually were:
- `getSearchDir` table test — relative dir, absolute dir, file path, name under
  `quadlet.src.path`, missing. Would have caught 0.1.
- `parseQuadlet`/`parseIniFile` fixtures — quoted values, drop-ins, `ServiceName=`
  overrides, `.quadlets` extraction.
- `generateCreateCommand` golden outputs (needs 1.3).
- `pruneStaleSystemdFiles` against a temp dir.

Requires narrowing `.gitignore` first (it currently blocks `*.container`/`*.pod`/
`*.network`/`*.volume` at every level, so fixtures can't be committed — `TODO.md` §6).

**Done when:** Phase 0's fixes all have regression tests behind them.

---

## Phase 2 — Structural moves — **done**

Pure relocation, now safe.

**2.1 — Split `core/handlers.go` (1827 lines) (M).** Target layout:

```
core/command.go            Command, RunCommands, runner helpers  (today's commands.go)
core/pull.go               HandlePull
core/create.go             HandleCreate
core/start.go              HandleStart
core/run.go                HandleRun
core/stop.go               HandleStop
core/remove.go             HandleRemove
core/systemd_install.go    HandleSystemdCreate/Remove, prune, generator validation
core/systemd_lifecycle.go  HandleSystemdStart/Stop/Status/Logs/Reload
core/inspect.go            HandlePS/Stats/Images/Logs
core/list.go               tree listing
core/generate.go           generateCreate/Startup/Run/StopCommand
core/podman.go             resourceExists, getContainerPS, listSystemdInstalledQuadlets
```

*Constraint:* no edits in the move commits. Verify with `git diff -M --stat` showing
renames only.

**2.2 — Command registry (M).** One table replacing three parallel switches — `flagSets` +
`Print*Usage` (`util/flags.go`), the dispatch switch (`main.go:45-120`), and the handler
pairs in `core`:

```go
type Subcommand struct {
    Name, Aliases   []string
    Flags           FlagSpec        // shared flags declared once, not per-command
    Usage           func()
    Run, RunSystemd func(*State, []*Quadlet) ([]Command, error)
}
```

Structurally fixes, as a side effect: `-s` rejected after the subcommand (§3), the
unreachable `default:` branch and its divergent error message, `PrintLogsUsage` printing
the `stats` flag set, and the copy-paste flag drift across subcommands.

**2.3 — Housekeeping (S).** Drop the dot-imports (`main.go:10-11`, `util/options.go:4`),
delete the commented-out blocks listed in `TODO.md` §5, decide on `schema/validator.go` —
either wire it into a `validate` command (`FEATURES.md`) or delete the ~256 dead lines. Fix
the `go vet` failure (`schema/validator.go:181-182`) and add `go vet` to CI.

**Done when:** adding a subcommand touches one table plus one file. — *Met: `registry.go`
is the single table, `validator.go` is gone, `go vet ./...` is clean and runs in CI.*

---

## Phase 2.5 — `internal/` move — **done**

*Added after Phase 2 shipped, from an audit against the official Go layout guidance
([go.dev/doc/modules/layout](https://go.dev/doc/modules/layout)). It sits here, and not in
Phase 6 with the rest of that audit, because it is a pure rename that gets more expensive
with every commit that touches an import line.*

**2.5 — Move `core/`, `util/` and `schema/` under `internal/` (S).** All three are currently
importable as `github.com/fkmiec/quadctl/core` and friends. This is a CLI with no library
consumers, and `internal/` is the only layout convention the compiler actually enforces —
putting them there makes Phases 3, 4 and 6 free to change any exported signature without it
being an API break.

`main.go` and `registry.go` stay at the repo root; that is the documented shape for a single
command, and there is no reason to invent a `cmd/quadctl/` for one binary. No `pkg/` — it
appears nowhere in the official guidance.

*Constraint:* rename plus import rewrite, nothing else. `git diff -M --stat` shows renames
and import lines only.

**Done when:** `go list ./...` shows every non-`main` package under `internal/`. — *Met:
`internal/core`, `internal/schema`, `internal/util`; `main.go` and `registry.go` stayed at the
root.*

---

## Phase 3 — The two subsystem rewrites — **done**

Contained, and both are root causes rather than symptoms.

**3.1 — Value model (M).** `parseIniFile` tokenizes every value on whitespace at parse time
(`util/parser.go:526`), destroying information nothing downstream can recover. Replace with:
raw values preserved on `Quadlet`, tokenization at use time, per-option semantics from the
schema (`AllowMultiple` decides splitting, not a post-hoc length check).

Fixes together: dropped `Exec=`/`HealthCmd=`, mangled quoting, the `strings.Split(vals[0], " ")`
hack (`core/handlers.go:1490`), and the blanket `strings.Trim(arg, '"')` at exec time
(`core/commands.go:63-65`). Then add the missing INI semantics: `\` line continuations,
empty-value-resets-list, surfaced drop-in parse errors.

**3.2 — Split the state struct (S).** `util.Quadctl` is config + parsed flags + run state in
one mutable object threaded everywhere — which is why `DotQuadletsPath` leaks across
directories in `InitAllQuadlets`. Split into immutable `Config` and per-run `State`; give
`.quadlets` extraction a proper `os.MkdirTemp` scratch dir with cleanup, replacing the
predictable `/tmp/<dirname>` that gets `RemoveAll`'d.

**Done when:** `Exec=/bin/sh -c "echo hi"` produces the right argv, verified by a golden test.
— *Met: `TestExecBecomesArgv` asserts the three arguments directly, and `commands.golden` now
quotes any argument containing whitespace, so a change in argv boundaries shows up as a diff
rather than hiding inside a space-joined line.*

*One thing 3.1 turned up that the plan didn't anticipate:* keeping the value whole through
parsing isn't enough on its own, because the podman template output was being re-split
afterwards — `"{{.Key}} {{.Value}}"` rendered to a string and then cut on whitespace loses the
value's spaces just as thoroughly. `QuadletOptionToPodman` now returns argv, rendering with a
placeholder and substituting the value after the cut.

*And one 3.2 turned up:* the installed directory under the generator root was taking its name
from wherever the files happened to be copied from, which was harmless only because the
`.quadlets` scratch path used to be named after the source directory. Replacing it with
`os.MkdirTemp` would have given every run a fresh, randomly named install directory. The
install name now comes from the source directory and the source path only says where to read.

**Residual, recorded in `TODO.md`:** splitting a repeatable option's line is unconditional, so
a `Volume=` path containing spaces needs quoting. Podman decides that per option; the schema
has a single `AllowMultiple` flag and cannot. Worth a second schema field when something needs
it.

---

## Phase 4 — Consistency and UX

The `TODO.md` §2–§4 long tail, now cheap and test-backed. Grouped by theme so each lands as
one coherent change:

- **Naming (M).** Resolve `VolumeName=`/`NetworkName=` being ignored in podman-direct mode
  and the resulting divergence from systemd's `systemd-<id>`. Pick one rule, apply it in
  both paths, document it. Then fix `resourceExists` to use the resolved name, which
  removes the duplicate `--name` and the re-create-every-time bug.
- **Matching (S).** Exact container matching in `getContainerPS` instead of `HasSuffix`;
  drop the `len(name) < 12` filter in `images`.
- **Config honesty (S).** Proper boolean parsing, warn on unknown keys, `--no-systemd` so
  `systemd.enabled=true` is overridable, reconcile the two different `QuadletUserPath`
  defaults.
- **Output (M).** Scope announcements when the search widens to all quadlets, `NO_COLOR`/TTY
  detection, consistent labels, warnings at default verbosity, spinner teardown on fatal
  errors and on Ctrl-C, "nothing to do" messages instead of silence.
- **Path/permission hygiene (S).** `list` no longer creates directories, `0660` → `0755`,
  preserve source file modes, recurse in `CopyDir`, create `quadlet.root.path`.

---

## Phase 5 — Docs and release

- README usage block regenerated from the registry (Phase 2.2 makes this mechanical), config
  keys documented, the truncated "Replace  with explicit values" comment fixed.
- `--version` via `-ldflags -X`, tests + vet in the release workflow, CI triggering on all
  branches.

*Overlap note:* the release half of this is largely subsumed by 6.4 — GoReleaser does the
`-ldflags -X` stamping and the cross-compile matrix in one config. Do 6.4 and this becomes
docs plus a `needs:` edge from the test job.

---

## Phase 6 — Go idiom and tooling alignment

*Added 2026-08-12 from an audit against the official Go guidance:
[layout](https://go.dev/doc/modules/layout), [package
names](https://go.dev/blog/package-names), [Code Review
Comments](https://go.dev/wiki/CodeReviewComments), [doc
comments](https://go.dev/doc/comment). Phase 2.5 is the piece of that audit worth doing
immediately; everything below either waits on Phase 3 or is independent of the refactor
entirely.*

**6.1 — Break up `util` (M). Requires 3.2.** `util` is the package name the Go blog names as
an anti-pattern, and here it is literal: five unrelated responsibilities sharing a directory
because there was nowhere else to put them. Target:

```
internal/quadlet   parser.go + flags.go   Quadlet, Option, INI parsing, search-dir resolution
internal/config    files.go               config discovery, embedded quadctl.ini, file I/O
internal/podman    options.go             quadlet option -> podman flag mapping
internal/tui       tui.go                 the bubbletea selector
internal/exec      runner.go              Runner/ExecRunner/RecordingRunner
```

`core` has the same vague-name problem with cohesive contents; `internal/command` is the
honest name for "dispatch plus handlers", and splitting the 823 lines of `systemd_*.go` into
`internal/systemd` is defensible but optional. `schema` keeps its name — it is a real noun
for what it holds.

*Why it waits:* `util.Quadctl` lives in `parser.go` and is config + flags + run state in one
struct. 3.2 splits it into `Config` and `State`, which is what decides whether it lands in
`internal/config` or stays with `main`. Doing 6.1 first means splitting that struct twice.

**6.2 — Package doc comments (S).** Every package should have one, in exactly one file
(`doc.go` when it runs long). There are currently none — not on `core`, `util`, or `schema`.
Best done at the end of 6.1, when the package boundaries are the ones that will last.

**6.3 — `golangci-lint`, pinned as a tool dependency (S). Independent.** `go vet` is the
floor and it is now in CI; a meta-linter is the ceiling. `unused` alone would have found the
dead code §5 still tracks by hand. Pin it with `go get -tool` rather than installing it ad
hoc, so CI and local runs agree — Go 1.24 added the `go.mod` `tool` directive for exactly
this and it retires the old `tools.go` blank-import trick. Config in `.golangci.yml`.

**6.4 — GoReleaser (S). Independent.** `build_release.yml` hand-rolls three cross-compiles
and three `tar` invocations, and still carries the workflow template's `# Replace this with
your actual build command`. A `.goreleaser.yaml` replaces the whole block and adds checksums
and a changelog, neither of which exist today. It also does Phase 5's `-ldflags -X` version
stamping, so treat the two as one task. Pin it via the same `tool` directive as 6.3.

**6.5 — CI details (S). Independent.**
- `go test` runs without `-race`.
- Both workflows use `actions/setup-go@v4`; v5+ is current.
- `go.yml` pins `'1.26.3'`, `build_release.yml` pins `'1.26'`. Pick one.
- `go mod tidy` is still pending (§6) — `tidy` moves 10 lines today.

**6.6 — A task runner, once there is something to wrap (S). Last.** There is deliberately
nothing here yet: `go build ./...`, `go test ./...` and `go vet ./...` are the build system,
and a Makefile that only wraps them is noise. After 6.3–6.5 there are real targets (lint,
cover, cross-build, release dry-run) and one earns its place. Make is the safe default;
Task (`Taskfile.yml`) if cross-platform matters.

**Not doing:** `pkg/`, and `cmd/quadctl/` for a single binary. Neither appears in the
official layout guidance for a repo this shape.

**Done when:** no package is named for its role in the build rather than its subject, and
`golangci-lint` + tests + vet all gate a release that GoReleaser cuts.

---

## Sequencing summary

```
Phase 0  bleeding      ──▶ release                                        done
Phase 1  seams         ──▶ runner iface → errors → determinism → tests    done
Phase 2  moves         ──▶ split files → registry → housekeeping          done
Phase 2.5 internal/    ──▶ pure rename                                    done
Phase 3  rewrites      ──▶ value model → state split                      done
Phase 4  consistency   ──▶ naming → matching → config → output → paths     next
Phase 5  docs/release
Phase 6  go idiom      ──▶ util split → package docs → lint → release → tasks
                          (6.3–6.5 are independent; they can land any time)
```

**Don't reorder these:** 1.1 before 1.4 (no tests without a fake runner), 1.3 before golden
tests, 1.2 before 2.2 (a registry of functions that call `os.Exit` isn't a registry),
2.1 before 2.2 (move first, then rewire), 3.1 after 1.4 (the value model rewrite is the
change most likely to break something quietly), 2.5 before 3 (the rename is cheapest when
the fewest commits are in flight), 3.2 before 6.1 (otherwise the state struct gets split
twice), 6.1 before 6.2 (documenting package boundaries that are about to move is wasted
work), 6.4 before 5's release bullet (GoReleaser subsumes it).

**One thing to resist:** doing Phase 4's naming work early because it's the most
*interesting* problem. It touches both execution paths and has no test coverage until
Phase 1 lands.
