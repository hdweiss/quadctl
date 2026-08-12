# TODO — bugs, fixes and inconsistencies

Working list of defects, rough edges and inconsistencies found by reading through
`main.go`, `core/`, `util/` and `schema/`, plus exercising the built binary.

Items marked **[verified]** were reproduced against the built binary on this machine
(mostly via `-p` print mode, so nothing was actually created or destroyed). Everything
else is read from the code.

New feature ideas are deliberately **not** here — see `FEATURES.md`.

---

## 1. Critical — data loss / silent wrong behavior

- [x] **`quadctl -s create|start <file>` wipes the whole quadlet generator directory.** **[verified]**
  `getSearchDir` (`util/flags.go:351`) sets `dir = filepath.Dir(path)` for a file argument
  and never makes it absolute, so `SearchDir` becomes the relative string `"."`.
  `HandleSystemdCreate` then computes `dest := filepath.Join(targetDir, filepath.Base(searchDir))`
  (`core/handlers.go:397`) — `filepath.Base(".")` is `"."`, so `dest == targetDir`. The files
  are copied into the *root* of the generator dir, and `pruneStaleSystemdFiles`
  (`core/handlers.go:488`) then treats every unrelated installed quadlet as stale and deletes it.
  Repro (print mode, from a dir with `aaa.container`):
  ```
  $ quadctl create -p -f aaa.container
  Systemd installing quadlets to /etc/containers/systemd
    => Copying directory . to /etc/containers/systemd
  Removing hello (no longer present in .)
  systemctl stop portainer
  Removing portainer.container (no longer present in .)
  Removing traefik (no longer present in .)
  Removing users (no longer present in .)          <- os.RemoveAll on the users/ tree
  ```
  Fixes needed on both sides: absolutize in `getSearchDir` (both the file and the
  relative-to-src branches), and make `pruneStaleSystemdFiles` refuse to run when
  `dest == targetDir` (or when `filepath.Base(searchDir)` is `.`/`..`/empty).

- [x] **`-f`/`--file` has never worked.** **[verified]**
  `InitQuadlets` reads the file name from `flag.Arg(1)` (`util/parser.go:92`), i.e. the
  *global* argument list — which is `["start", "-f", "foo.container"]`, so `flag.Arg(1)`
  is the literal string `-f`. The lookup misses, the filter is skipped silently, and the
  command runs against every quadlet in the directory:
  ```
  $ quadctl create -p -f aaa.container
  podman container create --name bbb ...
  podman container create --name aaa ...
  ```
  Should read the positional arg from the subcommand's own `FlagSet`
  (`flagSets[sub].Arg(0)`), and error out (not silently continue) when the named file
  isn't among the parsed quadlets.

- [ ] **Any single-valued option whose value contains spaces is silently dropped.** **[verified]**
  `parseIniFile` splits every value on whitespace via `ParseFields` (`util/parser.go:526`),
  so `Exec=/bin/sh -c "echo hi"` becomes three values; `generateCreateCommand` then hits
  `!opt.AllowMultiple && len(vals) > 1` and `continue`s (`core/handlers.go:1481`). The
  container is created with the image's default command and **nothing is printed unless
  `-v` is passed**. Same for `HealthCmd`, `Entrypoint`, `Annotation`, `Label`, etc.
  Quoting the whole value doesn't help either: the single value is then re-split on `" "`
  (`core/handlers.go:1490`) and the quotes are stripped per-arg at exec time
  (`core/commands.go:63-65`), producing `sh -c 'echo` + `hi'`.
  Needs proper shell-style tokenization applied at *use* time, not at parse time, and
  `Exec`/`HealthCmd` need to keep their raw value.
  *Partially mitigated:* the drop is no longer silent — the warning is tagged `[WARN]` and
  prints without `-v` (`PLAN.md` Phase 0). The value model itself is `PLAN.md` 3.1.

- [x] **Failed commands still exit 0.** **[verified]**
  `RunCommands` prints the error and moves on (`core/commands.go:126-160`); `main` returns
  normally (`main.go:122-125`). `quadctl stop` on a non-existent container prints
  `exit status 125` and exits `0`. Nothing that wraps quadctl in a script or CI job can
  detect failure. Track failures and exit non-zero; decide and document whether a failing
  command aborts the remaining ones (probably yes for `start`, no for `stop`/`rm`).

- [ ] **`.kube` option schema is never loaded — two stacked bugs.**
  1. `GetQuadletOptionsMap` has no `"kube"` case (`util/options.go:18-31`), so it returns
     `nil` and `schemas["kube"]` is a nil map. Every `[Kube]` key except the hand-handled
     `Yaml`/`ServiceName`/`PodmanArgs` is dropped with a "Quadlet kube option not defined"
     warning that's invisible without `-v`. `ConfigMap`, `PublishPort`, `Network`,
     `UserNS`, `LogDriver`, `ExitCodePropagation`, `SetWorkingDirectory` are all ignored.
  2. Even once registered it would panic: `GetKubeOptions` assigns the parsed templates to
     the loop *copy* (`option.QuadletTemplateParsed = …`, `schema/kube.go:26-27`) instead of
     `options[i].…` like every other schema file, leaving `PodmanTemplateParsed` nil →
     nil dereference at `util/parser.go:731`.

- [x] **Panics reachable from ordinary input.**
  - `util/parser.go:564` — `all[podID].GeneratedNames["pod_name"]` dereferences a nil
    `*Quadlet` when a container's `Pod=` names a pod file that isn't in the directory
    (typo, or the pod file was deleted).
  - `core/handlers.go:259` and `core/handlers.go:1644` — `q.Sections["Kube"]["KubeDownForce"][0]`
    indexes a nil slice. Only saved today by `||` short-circuit on the defaults; set
    `remove_volumes=false` *and* `remove_networks=false` and any `.kube` stop/remove panics.
  - `core/handlers.go:1628` — `createCmd[3:]` panics when `generateCreateCommand` returns
    an empty slice (missing schema, unhandled type).
  - `core/handlers.go:1143` / `:1778` — `res["image"].(string)` / `res["name"].(string)`
    type-assert without the `, ok` form; a k8s container without `image:` panics.
    (A sixth site, `HandlePull`, has the same bug — found while fixing the others.)

## 2. Functional bugs

- [ ] **Existence check uses the wrong name** (`core/handlers.go:74`, `:1670`).
  `resourceExists(q.Type, q.ID)` checks the *file* base name, but the resource is created
  as `ContainerName=` / `PodName=` when set. For `app.container` with `ContainerName=myapp`,
  the check always reports "doesn't exist", so `create` re-runs on every invocation and
  podman fails with "name already in use". Should use `q.GeneratedNames[...]`.

- [ ] **`--name` is emitted twice for containers.** **[verified]**
  Once explicitly (`core/handlers.go:1446`) and again through the schema mapping of
  `ContainerName`. Output:
  `podman container create --name myapp --restart always --name myapp … alpine`.

- [ ] **`VolumeName=` / `NetworkName=` are ignored in podman-direct mode.** **[verified]**
  Both keys are `continue`d in `generateCreateCommand` (`core/handlers.go:1349`, `:1381`)
  and the resource is created under the file's base name (`cmd = append(cmd, q.ID)` at
  `:1366`, `:1400`). Under systemd the same quadlet produces `systemd-<id>` or the
  configured name — and `HandleSystemdRemove` looks for exactly those (`:863`, `:878`).
  So the same quadlet yields different resource names depending on `-s`, and volumes
  created in direct mode are never cleaned up by `-s rm`.

- [ ] **`remove_volumes` / `remove_networks` are ignored by the non-systemd `remove`.**
  `HandleRemove` (`core/handlers.go:241-279`) unconditionally emits `podman volume rm` /
  `network rm`; only the systemd path honors the config. `quadctl rm` destroys data the
  user explicitly asked to keep.

- [ ] **`DotQuadletsPath` leaks across directories** (`util/parser.go:191`, set but never cleared).
  `InitAllQuadlets` loops over every directory under `quadlet.src.path` reusing the same
  `Quadctl`; once one directory contains a `.quadlets` file, every *subsequent* directory's
  files get copied into that stale temp dir and parsed from there. Affects `-a` and every
  "nothing here, so scan everything" fallback in `main.go`.

- [ ] **`.quadlets` extraction uses a predictable shared temp path** (`util/parser.go:263-279`).
  `os.TempDir()/<parent dir name>` is `os.RemoveAll`'d and recreated — it can clobber an
  unrelated `/tmp/<name>`, and on a multi-user host it's trivially pre-creatable/symlinkable
  by another user. Use `os.MkdirTemp` and clean up at exit. Related: extraction copies
  *every* sibling file (including `.env` files with secrets) into that world-readable dir.

- [ ] **`list` creates directories, with a broken mode** (`core/handlers.go:1262`).
  A read-only listing command shouldn't `MkdirAll` at all, and `0660` has no execute bit,
  so the directory it just created can't be traversed. `quadctl ls -a` as a normal user
  also tries to create `/etc/containers/systemd`.

- [ ] **`list` ignores its `[path]` argument but still validates it.** **[verified]**
  `HandleList` only ever uses the configured paths (`core/handlers.go:1225-1255`), yet
  `getSearchDir` exits 1 when the path doesn't resolve. `quadctl ls traefik` silently
  prints the *root* systemd tree; `quadctl ls doesnotexist` errors. Either honor the arg
  or reject it explicitly.

- [ ] **`list` shows dotfiles.** **[verified]** — `.git` appears in the tree
  (`core/handlers.go:1299`) even though the directory selector deliberately skips
  dot-prefixed entries (`util/files.go:244`).

- [ ] **"Is it already running?" checks are wrong on both paths.**
  Non-systemd `HandleStart` inspects only the *first* ps row (`core/handlers.go:120-121`) —
  one stopped container at the head means nothing gets restarted; one running container
  means everything gets stopped. The systemd variant (`:700`) stops whenever ps returns any
  row at all, including `Exited` ones.

- [ ] **ps/stats/images/logs match containers by suffix** (`core/handlers.go:1772`).
  `strings.HasSuffix(parts[1], q.GeneratedNames["container"])` makes quadlet `web` match an
  unrelated container named `myweb`. The `||` clause is also outside the type guard
  (`&&` binds tighter), so pod-name matching isn't scoped to `.container` quadlets. Use
  exact name matching, or filter with `podman ps --filter`.

- [ ] **`quadctl images` silently hides short image names** (`core/handlers.go:1092`, `:1122`, `:1144`).
  `if len(name) < 12 { continue }` — presumably meant to reject truncated image *IDs*, but
  it's applied to image *names*, so `alpine`, `nginx`, `caddy` etc. never show up.

- [x] **Leftover debug print** — `fmt.Printf("generateStartupCommand(%s): %v\n", …)`
  fires on every `.kube` start (`core/handlers.go:1563`).

- [x] **`HandleList`'s error is discarded** at the call site (`main.go:85`).

- [x] **Detached `podman run` is treated as foreground** (`core/commands.go:49`, `:70`, `:86`).
  `slices.Contains(c.Cmd,"run") && (!slices.Contains(c.Cmd,"-d") || !slices.Contains(c.Cmd,"--detach"))`
  — the `||` is true unless *both* spellings are present, so any `-d`-only run skips the
  spinner and attaches stdio. Should be `&&`. The same three-way copy of this condition
  should be one helper.

- [ ] **All `"` characters are stripped from every argument** before exec
  (`core/commands.go:63-65`), corrupting values that legitimately contain quotes.

- [x] **`html/template` used for shell commands and the config file**
  (`main.go:5`, `util/parser.go:8`, `util/files.go:7`). These are not HTML; the escaping
  will mangle `"`, `&`, `<`, `>` in user-configured `systemd.*` command templates and in
  `$HOME` when the default config is written. Should be `text/template` everywhere
  (`schema/` already uses `text/template`).

- [ ] **`parseIniFile` doesn't implement systemd INI semantics** (`util/parser.go:494-537`):
  no `\` line continuation (common in real quadlets with long `Exec=`/`PodmanArgs=`), no
  "empty value resets the list", no handling of repeated section headers beyond merging.
  Drop-in parse errors are swallowed entirely (`util/parser.go:432`).

- [ ] **`.image` and `.build` quadlets are ignored without a word.**
  Full schemas exist (`schema/image.go`, `schema/build.go`) but the extension map
  (`util/parser.go:18`) doesn't list them. Also: any unrecognized extension in a quadlet
  directory is skipped silently — worth at least a `-v` warning.

- [ ] **`[Pod] Volume=` is not treated as a dependency** (`util/parser.go:585-593` only
  looks at `Network=` for pods), so a pod's volume may not be created first.

- [ ] **k8s YAML handling is fragile** (`util/parser.go:758-811`): the read error is
  discarded (`yml, _ := readYamlFile(...)`), only a single `kind: Pod` document is
  supported (multi-document YAML is the norm for `podman kube play`), and every failure is
  an `os.Exit(1)` with a bare message. A `[Kube]` section without `Yaml=` calls
  `readK8sYaml("")` and dies with a confusing error.
  *Partly addressed by `PLAN.md` 1.2:* the failures are errors now, naming the file. The
  discarded read error and single-document limitation are still open.

- [ ] **`podman quadlet list` output is split on `,`** (`core/handlers.go:1688`) — breaks on
  any path containing a comma. The systemctl fallback puts `ServiceName` in the
  "UNIT NAME" column without the `.service` suffix (`:1750`), so the two code paths
  produce different tables.

- [ ] **Writability check tests permission bits, not access** (`core/handlers.go:307`):
  `perm&0200 != 0200 && perm&0020 != 0020 && perm&0002 != 0002` ignores ownership entirely.
  Just attempt the write (or use `unix.Access`) and report the real error.

- [ ] **`CopyDir` skips nested subdirectories** (`util/files.go:224`), so any non-drop-in
  subdirectory in a quadlet app dir (a `config/` folder that gets bind-mounted, for
  instance) is not installed under systemd — silently.

- [ ] **File/dir modes are hardcoded** (`util/files.go:164` `0770`, `:194` `0644`): source
  modes aren't preserved, and secret-bearing files (`.env`) become world-readable in the
  generator directory.

- [ ] **`GetConfig` creates `quadlet.user.path` and `quadlet.src.path` but never
  `quadlet.root.path`** (`util/files.go:129-136`), and under `sudo` it creates the *user*
  path as root, leaving root-owned directories in the user's home.

## 3. CLI / flag handling

- [ ] **`-s` only works before the subcommand.** **[verified]**
  `quadctl start -s` → `flag provided but not defined: -s`, exit 2 — while
  `quadctl start --help` says "Use -s to start under systemd". Register the global flags in
  every subcommand `FlagSet` (or parse them out first).

- [ ] **`systemd.enabled=true` in the config can't be overridden from the CLI.** **[verified]**
  `InitConfig` only ever sets `IsSystemd = true` (`util/files.go:48`) and runs *after* flag
  parsing. On a host configured that way there is no way to run a one-off podman-direct
  command, and `quadctl run` becomes permanently unreachable (`main.go:99`). Needs a
  `--no-systemd` (or `-s=false` honored after config load).

- [ ] **`quadctl -s run` prints an explanation and exits 0** (`main.go:99-101`). Should exit
  non-zero — it's a refused command, not a successful one.

- [ ] **Unreachable dead branch + inconsistent messages.** `ProcessSubcommand` already exits
  on an unknown subcommand with `Invalid command: x` and *no* usage (`util/flags.go:331`),
  so `main.go:116-119`'s `Unknown command: x` + `PrintUsage()` can never run. Keep one, and
  it should be the one that prints usage.

- [ ] **`PrintLogsUsage` prints the `stats` flag set** (`util/flags.go:319`).

- [ ] **Flag sets are inconsistent across subcommands.**
  `-p/--print` is missing from `ps`/`stats`/`images`/`list`; `-v/--verbose` exists only on
  `create/start/run/stop/remove`; `-f/--file` exists on almost everything *except* `logs`;
  `-a` means "all quadlets under src.path" (`IsShowAll`) for most commands but "all three
  configured paths" (`IsListAll`) for `list`. Worth a single table of
  command → supported flags, then generating the flag sets from it.

- [ ] **Every flag is listed twice in help output** because short and long forms are
  registered as separate flags (`quadctl ls -h` shows `-a` and `-all` as two entries with
  the same text). A small custom usage printer would collapse these.

- [ ] **`status -p` has no effect** on the non-systemd path (it calls `HandlePS` directly),
  and `-l/--long` is accepted but meaningless there (`main.go:56-64`).

- [ ] **`status`/`logs` bypass the command pipeline** when not in print mode
  (`core/handlers.go:938`, `:995` call `runCommand` directly), so they don't get the
  spinner/label/error handling everything else has.

## 4. Output & UX

- [ ] **Scope silently widens from "this directory" to "everything".**
  `ps`, `stats`, `images`, `status`, `pull`, `logs` fall back to `InitAllQuadlets` when the
  CWD has no quadlets (`main.go:46-83`) with no indication. `quadctl pull` run from an
  unrelated directory will happily pull every image of every quadlet on the system. At
  minimum print `No quadlets in <dir> — showing all quadlets under <src.path>`.

- [ ] **`quadctl create` prints nothing at all when everything already exists** — no
  commands are generated, so `RunCommands` is never called (`main.go:122`). Say something
  ("all resources already exist").

- [ ] **Warnings are hidden by default and formatted raggedly.** They only appear with `-v`
  (`core/commands.go:100-113`), are printed *before* the commands they belong to, and
  several messages embed their own `\n` and leading spaces (`core/handlers.go:89`, `:102`,
  `:1612`) so the block comes out with inconsistent blank lines and indentation. Dropping
  a user's `Exec=` (see §1) should be a visible warning at default verbosity.

- [ ] **ANSI colour is emitted unconditionally**, including when stdout is a pipe or file —
  every table uses `table.StyleColoredYellowWhiteOnBlack` (`core/handlers.go:1048`, `:1179`,
  `:1824`) and the spinner runs regardless of TTY. Support `NO_COLOR`/`--no-color` and
  detect a non-TTY.

- [x] **Fatal errors inside a running spinner leave it mid-animation.**
  `HandleSystemdCreate`'s file operations `os.Exit(1)` from inside `RunFn` while the spinner
  is spinning (`core/handlers.go:329`, `:357`, `:370`, `:402`, `:411`, `:432`) —
  `validateQuadletGenerationCommand` documents this exact problem and works around it
  locally (`:616-618`) instead of fixing it globally.
  *Fixed by `PLAN.md` 1.2:* those file operations and the generator validation now report a
  failed command instead of exiting, so the spinner is always torn down. **Still open:**
  Ctrl-C has no signal handler (`PLAN.md` Phase 4, Output).

- [ ] **`quadctl logs` with nothing running runs `podman logs` with no arguments.** **[verified]**
  Output: `Error: specify at least one container name or ID to log`, exit 0
  (`core/handlers.go:1183-1222`). Should say "no containers found for these quadlets".

- [ ] **`stats` exits 1 when nothing matches while `ps` prints an empty table** for exactly
  the same situation (`core/handlers.go:1060`). Pick one behavior.

- [ ] **Selector error message misattributes the failure** (`main.go:36`): prints
  "No quadlets found in directory: `<SearchDir>`" even when the real error came from
  reading `quadlet.src.path`.

- [ ] **`list -a` prints three unlabeled trees** (`core/handlers.go:1237-1252`) — nothing
  says which is src / user / root, and the three identical error messages all read
  "Error listing quadlets in search directory".

- [ ] **Inconsistent labels and naming in output**: "Systemd stopping .container app" vs
  "Starting .container app" vs "Systemd installing quadlets to …"; the raw dotted type
  (`.container`) is printed as if it were a word. Normalize to something like
  `Starting container app`.

- [ ] **Command generation is nondeterministic.** **[verified]** Map iteration in
  `generateCreateCommand` (`core/handlers.go:1474`) and in `topologicalSort`'s seed loop
  (`util/parser.go:631`) reorders both the podman arguments and independent quadlets
  between runs:
  ```
  run 1: podman container create --name myapp --restart always --env FOO=bar … --network web --publish 8080:80 --label app=demo
  run 2: podman container create --name myapp --restart always --publish 8080:80 --env FOO=bar … --label app=demo -v data:/data --network web
  ```
  Makes `-p` output undiffable and start ordering unstable. Sort keys (and sort the
  topological seed by ID) for stable output.

- [ ] **No preflight check for `podman` / `systemctl`.** Missing binaries surface as
  `exec: "podman": executable file not found in $PATH` from whichever code path happens to
  run first. One `exec.LookPath` check with a clear message would do.

- [ ] **No `--version`.** Release binaries carry no version at all
  (`.github/workflows/build_release.yml` has no `-ldflags -X`), so bug reports can't
  identify a build.

- [ ] **Default paths disagree between code and shipped config**: `initState` uses
  `/etc/containers/systemd/users` for `QuadletUserPath` (`main.go:149`), the template ini
  uses `{{.home}}/.config/containers/systemd` (`util/config/quadctl.ini:35`).

- [ ] **Config parsing is silently forgiving** (`util/files.go:24-50`): booleans are
  one-way and case-sensitive (`use_symbolic_links` only reacts to `true`/`1`,
  `use_subdirectories` only to `false`/`0`), so `True`, `yes`, `on` are ignored without
  a word; unknown/misspelled keys are dropped silently. Parse booleans properly and warn
  on unrecognized keys.

## 5. Structure & maintainability

- [ ] **Split `core/handlers.go` (1827 lines) into per-command files.** Suggested layout:
  - `core/command.go` — `Command`, `RunCommands`, `runCommand*` helpers (mostly today's `commands.go`)
  - `core/pull.go`, `core/create.go`, `core/start.go`, `core/run.go`, `core/stop.go`, `core/remove.go` — podman path
  - `core/systemd_install.go` — `HandleSystemdCreate`/`Remove`, prune, generator validation
  - `core/systemd_lifecycle.go` — `HandleSystemdStart`/`Stop`/`Status`/`Logs`/`Reload`
  - `core/inspect.go` — `HandlePS`/`Stats`/`Images`/`Logs`
  - `core/list.go` — tree listing
  - `core/generate.go` — `generateCreateCommand`/`StartupCommand`/`RunCommand`/`StopCommand`
  - `core/podman.go` — `resourceExists`, `getContainerPS`, `listSystemdInstalledQuadlets`
  Pure file moves first, no behavior change, so the diff stays reviewable.

- [ ] **Replace the three parallel switches with one command registry.** A subcommand is
  currently declared in three places that must be kept in sync — `flagSets` +
  `Print*Usage` (`util/flags.go`), the dispatch switch (`main.go:45-120`), and the
  handler pair in `core`. A `[]Command{Name, Aliases, Flags, Usage, Run, RunSystemd}`
  table would collapse them and structurally fix the "unknown command" duplication, the
  `-s`-placement problem and the copy-paste flag drift above.

- [x] **Stop calling `os.Exit` from library code** — 54 call sites across `util/` and
  `core/`. It makes handlers untestable, skips temp-dir cleanup, and is why exit codes are
  inconsistent. Return errors; let `main` decide the exit code. *(`PLAN.md` 1.2 — the count
  was 60 by the time it was done; `main()` is now the only exit.)*

- [ ] **Factor out the repeated "resolve display name for a quadlet" block** — the same
  `resName/resType` derivation appears at `core/handlers.go:132-138`, `185-190`, `226-230`,
  `248-254`, `1551-1596`, `1635-1640`.

- [ ] **Dead code to delete or wire up.** `schema/validator.go` (~256 lines) is entirely
  self-referential — the `Handler`/`Validate` machinery is never called, so no option value
  is ever validated despite every schema option carrying validators. Also unused:
  `GetKubeSchema`/`GetImageSchema`/`GetBuildSchema`, `ValidateSchema`,
  `GetPodmanOptionsMap`/`assemblePodmanOptionsMap`, `util.ListFiles`, `util.DeleteFile`,
  `optKubeAutoUpdate` (`schema/kube.go:58`, shadowed by `optAutoUpdate()` in the list).
  Decide: wire validation up (see `FEATURES.md`) or drop it.

- [ ] **Drop the dot-imports** (`main.go:10-11`, `util/options.go:4`) — `. "…/core"`,
  `. "…/schema"` make it impossible to tell where `Command`, `SchemaOption`,
  `GetContainerOptions` come from.

- [ ] **Commented-out code blocks** left in place: `util/parser.go:683-725` (`ParseDurationToSeconds`),
  `:740-747`, `util/tui.go:98-116`, `core/handlers.go:1790-1806`, `core/commands.go:271-273`,
  `:285`.

## 6. Tests, CI, repo hygiene

- [ ] **`go vet ./...` currently fails** — `schema/validator.go:181-182`, duplicate
  `json:"ignore-empty"` tags. CI (`.github/workflows/go.yml`) doesn't run vet, so it went
  unnoticed. Add `go vet` to CI.

- [ ] **Test coverage is one file** (`util/options_test.go`) covering schema→podman option
  templating only. Nothing covers the parser, dependency resolution, topological sort,
  path resolution, or command generation — which is where every bug in §1 lives.
  Highest-value additions:
  - `getSearchDir` table test (relative dir, absolute dir, file path, name under
    `quadlet.src.path`, missing) — would have caught the `.` bug.
  - `parseQuadlet`/`parseIniFile` fixtures: quoted values, spaces, continuations, drop-ins,
    `ServiceName=` overrides, `.quadlets` extraction.
  - `generateCreateCommand` golden-output tests (also forces deterministic ordering).
  - `pruneStaleSystemdFiles` against a temp dir.

- [ ] **`.gitignore` blocks test fixtures**: `*.container`, `*.pod`, `*.network`, `*.volume`
  are ignored at every level, so example/fixture quadlets can't be committed —
  which is probably why there are none. Narrow to the repo root, and note `.kube`/`.quadlets`
  aren't ignored (inconsistent).

- [ ] **`go.mod` isn't tidy** — `bubbletea`, `spinner` and `goccy/go-yaml` are imported
  directly but marked `// indirect`.

- [ ] **Release workflow doesn't run tests or vet** before publishing binaries
  (`.github/workflows/build_release.yml:25`), and `build.sh` builds `main.go` rather than
  the package (`go build -o quadctl .`).

- [ ] **CI only triggers on `main`** (`.github/workflows/go.yml:6-10`), so nothing runs on
  this branch.

## 7. Documentation

- [ ] **README usage block is stale** (`README.md:70-101`): it predates `-a`, `-p`, `-v`,
  `-l`, `-d`, `-f`, `--pargs`, `--exec` and the `pull` command's flags, and the "Flags"
  section only lists `-s`.

- [ ] **Config keys are undocumented** — README describes only the three path settings
  (`README.md:64-66`); `systemd.enabled`, `use_subdirectories`, `use_symbolic_links`,
  `remove_volumes`, `remove_networks` and the `systemd.*` command templates aren't
  mentioned anywhere outside the ini comments.

- [ ] **Truncated comment in the shipped config**: "Replace  with explicit values"
  (`util/config/quadctl.ini:12` and `:32`) — a word is missing, and it should say which
  variables (`$HOME`, `$XDG_*`) are not expanded.

- [ ] **Document the podman-direct vs systemd naming difference** once §2's
  `VolumeName`/`NetworkName` inconsistency is resolved — users need to know that
  `-s` and non-`-s` runs may address different podman resources.
