# TODO — bugs, fixes and inconsistencies

Working list of defects, rough edges and inconsistencies found by reading through
`main.go` and the packages under `internal/`, plus exercising the built binary. Locations
cited below are where a defect was found; several name files that later moves have since
renamed or split (`PLAN.md` 2.1, 2.5 and 6.1).

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

- [x] **Any single-valued option whose value contains spaces is silently dropped.** **[verified]**
  `parseIniFile` splits every value on whitespace via `ParseFields` (`util/parser.go:526`),
  so `Exec=/bin/sh -c "echo hi"` becomes three values; `generateCreateCommand` then hits
  `!opt.AllowMultiple && len(vals) > 1` and `continue`s (`core/handlers.go:1481`). The
  container is created with the image's default command and **nothing is printed unless
  `-v` is passed**. Same for `HealthCmd`, `Entrypoint`, `Annotation`, `Label`, etc.
  *Fixed by `PLAN.md` 3.1:* the parser records each assignment as written; the schema decides
  at use time whether a line is one value or several, and the last assignment to a
  single-valued option wins. The warning that reported the drop is gone with the drop.
  **Residual gap:** for a repeatable option the split is unconditional, so `AddCapability=A B`
  gives two values (right) but `Volume=/my path:/data` gives two as well (wrong) unless the
  value is quoted. Podman distinguishes these per option; the schema has one `AllowMultiple`
  flag and cannot. Fixing it means a second schema field — worth doing when something needs
  it, not before.

- [x] **Failed commands still exit 0.** **[verified]**
  `RunCommands` prints the error and moves on (`core/commands.go:126-160`); `main` returns
  normally (`main.go:122-125`). `quadctl stop` on a non-existent container prints
  `exit status 125` and exits `0`. Nothing that wraps quadctl in a script or CI job can
  detect failure. Track failures and exit non-zero; decide and document whether a failing
  command aborts the remaining ones (probably yes for `start`, no for `stop`/`rm`).

- [ ] **`.kube` option schema is never loaded — two stacked bugs.**
  1. `schema.QuadletOptions` has no `"kube"` case (`schema/options.go`), so it returns
     `nil` and `AllQuadletOptions()["kube"]` is a nil map. Every `[Kube]` key except the hand-handled
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

- [x] **Existence check uses the wrong name** (`core/handlers.go:74`, `:1670`).
  `resourceExists(q.Type, q.ID)` checks the *file* base name, but the resource is created
  as `ContainerName=` / `PodName=` when set. For `app.container` with `ContainerName=myapp`,
  the check always reports "doesn't exist", so `create` re-runs on every invocation and
  podman fails with "name already in use".
  *Fixed by `PLAN.md` Phase 4:* the check uses `q.ResourceName`.

- [x] **`--name` is emitted twice for containers.** **[verified]**
  Once explicitly (`core/handlers.go:1446`) and again through the schema mapping of
  `ContainerName`. Output:
  `podman container create --name myapp --restart always --name myapp … alpine`.
  *Fixed by `PLAN.md` Phase 4:* `ContainerName` is folded into the single explicit `--name`.

- [x] **`VolumeName=` / `NetworkName=` are ignored in podman-direct mode.** **[verified]**
  Both keys were `continue`d in `generateCreateCommand` and the resource was created under
  the file's base name. Under systemd the same quadlet produces `systemd-<id>` or the
  configured name — and `HandleSystemdRemove` looks for exactly those. So the same quadlet
  yielded different resource names depending on `-s`, and volumes created in direct mode
  were never cleaned up by `-s rm`.
  *Fixed by `PLAN.md` Phase 4:* one rule on both paths — `XName=` when the file gives one,
  `systemd-<id>` when it doesn't (`quadlet.ResolveResourceName`), resolved once at parse time
  into `Quadlet.ResourceName`. References between quadlets (`Volume=data.volume:/srv`,
  `Network=front.network`, `Pod=stack.pod`) resolve to the referenced quadlet's name via
  `Quadlet.RefNames`; a value that names no quadlet is passed through as written. Documented
  in `README.md` under "Resource naming".

- [ ] **`remove_volumes` / `remove_networks` are ignored by the non-systemd `remove`.**
  `HandleRemove` (`core/handlers.go:241-279`) unconditionally emits `podman volume rm` /
  `network rm`; only the systemd path honors the config. `quadctl rm` destroys data the
  user explicitly asked to keep.

- [x] **`DotQuadletsPath` leaks across directories** (`util/parser.go:191`, set but never cleared).
  `InitAllQuadlets` loops over every directory under `quadlet.src.path` reusing the same
  `Quadctl`; once one directory contains a `.quadlets` file, every *subsequent* directory's
  files get copied into that stale temp dir and parsed from there. Affects `-a` and every
  "nothing here, so scan everything" fallback in `main.go`.
  *Fixed by `PLAN.md` 3.2:* `discoverAndParseQuadlets` answers it fresh for every directory,
  with a regression test.

- [x] **`.quadlets` extraction uses a predictable shared temp path** (`util/parser.go:263-279`).
  `os.TempDir()/<parent dir name>` is `os.RemoveAll`'d and recreated — it can clobber an
  unrelated `/tmp/<name>`, and on a multi-user host it's trivially pre-creatable/symlinkable
  by another user. Use `os.MkdirTemp` and clean up at exit.
  *Fixed by `PLAN.md` 3.2:* `State.newScratchDir` uses `os.MkdirTemp` (mode 0700) and `main`
  defers `State.Cleanup`. The install directory is now named from the *source* directory
  rather than the extraction path, which the scratch rename would otherwise have broken.
  **Still open:** extraction copies *every* sibling file (including `.env` files with
  secrets) into the scratch directory.

- [x] **`list` creates directories, with a broken mode** (`core/handlers.go:1262`).
  A read-only listing command shouldn't `MkdirAll` at all, and `0660` had no execute bit,
  so the directory it just created couldn't be traversed. `quadctl ls -a` as a normal user
  also tried to create `/etc/containers/systemd`.
  *Fixed by `PLAN.md` Phase 4:* `listQuadlets` reports a missing directory instead of
  creating one.

- [ ] **`list` ignores its `[path]` argument but still validates it.** **[verified]**
  `HandleList` only ever uses the configured paths (`core/handlers.go:1225-1255`), yet
  `getSearchDir` exits 1 when the path doesn't resolve. `quadctl ls traefik` silently
  prints the *root* systemd tree; `quadctl ls doesnotexist` errors. Either honor the arg
  or reject it explicitly.

- [x] **`list` shows dotfiles.** **[verified]** — `.git` appeared in the tree even though
  the directory selector deliberately skips dot-prefixed entries.
  *Fixed by `PLAN.md` Phase 4:* the tree skips them too.

- [ ] **"Is it already running?" checks are wrong on both paths.**
  Non-systemd `HandleStart` inspects only the *first* ps row (`core/handlers.go:120-121`) —
  one stopped container at the head means nothing gets restarted; one running container
  means everything gets stopped. The systemd variant (`:700`) stops whenever ps returns any
  row at all, including `Exited` ones.

- [x] **ps/stats/images/logs match containers by suffix** (`core/handlers.go:1772`).
  `strings.HasSuffix(parts[1], q.GeneratedNames["container"])` made quadlet `web` match an
  unrelated container named `myweb`. The `||` clause was also outside the type guard
  (`&&` binds tighter), so pod-name matching wasn't scoped to `.container` quadlets.
  *Fixed by `PLAN.md` Phase 4:* `podman.quadletOwnsContainer` compares names exactly, scoped
  per quadlet type. Suffix matching was only ever covering for the naming divergence the
  same phase removed; `.kube` matches on the pod podman kube play creates and on the
  `<pod>-<container>` name it gives each container.

- [x] **`quadctl images` silently hides short image names** (`core/handlers.go:1092`, `:1122`, `:1144`).
  `if len(name) < 12 { continue }` — presumably meant to reject truncated image *IDs*, but
  it was applied to image *names*, so `alpine`, `nginx`, `caddy` etc. never showed up.
  *Fixed by `PLAN.md` Phase 4:* all three sites skip an empty name and nothing else.

- [x] **Leftover debug print** — `fmt.Printf("generateStartupCommand(%s): %v\n", …)`
  fires on every `.kube` start (`core/handlers.go:1563`).

- [x] **`HandleList`'s error is discarded** at the call site (`main.go:85`).

- [x] **Detached `podman run` is treated as foreground** (`core/commands.go:49`, `:70`, `:86`).
  `slices.Contains(c.Cmd,"run") && (!slices.Contains(c.Cmd,"-d") || !slices.Contains(c.Cmd,"--detach"))`
  — the `||` is true unless *both* spellings are present, so any `-d`-only run skips the
  spinner and attaches stdio. Should be `&&`. The same three-way copy of this condition
  should be one helper.

- [x] **All `"` characters are stripped from every argument** before exec
  (`core/commands.go:63-65`), corrupting values that legitimately contain quotes.
  *Fixed by `PLAN.md` 3.1:* quoting is resolved once, where the value is written, so what
  reaches exec is argv and nothing needs stripping. Print mode shell-quotes it back
  (`command.ShellQuote`), so what it shows is what would run.

- [x] **`html/template` used for shell commands and the config file**
  (`main.go:5`, `util/parser.go:8`, `util/files.go:7`). These are not HTML; the escaping
  will mangle `"`, `&`, `<`, `>` in user-configured `systemd.*` command templates and in
  `$HOME` when the default config is written. Should be `text/template` everywhere
  (`schema/` already uses `text/template`).

- [x] **`parseIniFile` doesn't implement systemd INI semantics** (`util/parser.go:494-537`):
  no `\` line continuation (common in real quadlets with long `Exec=`/`PodmanArgs=`), no
  "empty value resets the list", no handling of repeated section headers beyond merging.
  Drop-in parse errors are swallowed entirely (`util/parser.go:432`).
  *Fixed by `PLAN.md` 3.1:* continuations are joined, an empty assignment resets the key,
  drop-ins are applied in name order and a drop-in that can't be read is now an error.
  Repeated section headers still merge, which matches systemd.

- [ ] **`.image` and `.build` quadlets are ignored without a word.**
  Full schemas exist (`schema/image.go`, `schema/build.go`) but the extension map
  (`util/parser.go:18`) doesn't list them. Also: any unrecognized extension in a quadlet
  directory is skipped silently — worth at least a `-v` warning.

- [ ] **`[Pod] Volume=` is not treated as a dependency** (`quadlet/parser.go`, `extractDependencies` only
  looks at `Network=` for pods), so a pod's volume may not be created first.

- [ ] **k8s YAML handling is fragile** (`quadlet/parser.go`, `readK8sYaml`): the read error is
  discarded (`yml, _ := readYamlFile(...)`), only a single `kind: Pod` document is
  supported (multi-document YAML is the norm for `podman kube play`), and every failure is
  an `os.Exit(1)` with a bare message. A `[Kube]` section without `Yaml=` calls
  `readK8sYaml("")` and dies with a confusing error.
  *Partly addressed by `PLAN.md` 1.2:* the failures are errors now, naming the file. The
  discarded read error and single-document limitation are still open.

- [ ] **`podman quadlet list` output is split on `,`** (`systemd/list.go`) — breaks on
  any path containing a comma. The systemctl fallback puts `ServiceName` in the
  "UNIT NAME" column without the `.service` suffix (`:1750`), so the two code paths
  produce different tables.

- [ ] **Writability check tests permission bits, not access** (`systemd/install.go`, `HandleCreate`):
  `perm&0200 != 0200 && perm&0020 != 0020 && perm&0002 != 0002` ignores ownership entirely.
  Just attempt the write (or use `unix.Access`) and report the real error.

- [x] **`CopyDir` skips nested subdirectories** (`util/files.go:224`), so any non-drop-in
  subdirectory in a quadlet app dir (a `config/` folder that gets bind-mounted, for
  instance) was not installed under systemd — silently.
  *Fixed by `PLAN.md` Phase 4:* `CopyDir` recurses. The separate drop-in copy is now only
  needed when files are installed individually rather than as a directory.

- [x] **File/dir modes are hardcoded** (`util/files.go:164` `0770`, `:194` `0644`): source
  modes weren't preserved, and secret-bearing files (`.env`) became world-readable in the
  generator directory. *Fixed by `PLAN.md` Phase 4:* `CopyFile` and `CopyDir` carry the
  source mode across, including when overwriting an existing installed copy.

- [x] **`GetConfig` creates `quadlet.user.path` and `quadlet.src.path` but never
  `quadlet.root.path`** (`util/files.go:129-136`), and under `sudo` it created the *user*
  path as root, leaving root-owned directories in the user's home.
  *Fixed by `PLAN.md` Phase 4:* `LoadConfig` creates `quadlet.src.path` plus whichever
  generator directory this invocation could use — root's when rootful, the user's when not.

## 3. CLI / flag handling

- [x] **`-s` only works before the subcommand.** **[verified]**
  `quadctl start -s` → `flag provided but not defined: -s`, exit 2 — while
  `quadctl start --help` says "Use -s to start under systemd". Register the global flags in
  every subcommand `FlagSet` (or parse them out first).
  *Fixed by `PLAN.md` 2.2:* `globalFlags` is registered on every subcommand's `FlagSet`.

- [x] **`systemd.enabled=true` in the config can't be overridden from the CLI.** **[verified]**
  `InitConfig` only ever set `IsSystemd = true` and ran *after* flag parsing. On a host
  configured that way there was no way to run a one-off podman-direct command, and
  `quadctl run` was permanently unreachable.
  *Fixed by `PLAN.md` Phase 4:* `--no-systemd` is a global flag, `main.resolveSystemdMode`
  settles the two against each other, and passing `-s` and `--no-systemd` together is an
  error rather than a silent winner.

- [x] **`quadctl -s run` prints an explanation and exits 0** (`main.go:99-101`). Should exit
  non-zero — it's a refused command, not a successful one.
  *Fixed by `PLAN.md` 2.2:* `run`'s `RunSystemd` returns an error, so it exits 1.

- [x] **Unreachable dead branch + inconsistent messages.** `ProcessSubcommand` already exits
  on an unknown subcommand with `Invalid command: x` and *no* usage (`util/flags.go:331`),
  so `main.go:116-119`'s `Unknown command: x` + `PrintUsage()` can never run. Keep one, and
  it should be the one that prints usage.
  *Fixed by `PLAN.md` 2.2:* one lookup in `registry.processSubcommand`, and it prints usage.

- [x] **`PrintLogsUsage` prints the `stats` flag set** (`util/flags.go:319`).
  *Fixed by `PLAN.md` 2.2:* help is rendered from the subcommand's own row in the registry.

- [ ] **Flag sets are inconsistent across subcommands.**
  `-p/--print` is missing from `ps`/`stats`/`images`/`list`; `-v/--verbose` exists only on
  `create/start/run/stop/remove`; `-f/--file` exists on almost everything *except* `logs`;
  `-a` means "all quadlets under src.path" (`IsShowAll`) for most commands but "all three
  configured paths" (`IsListAll`) for `list`.
  *Half done by `PLAN.md` 2.2:* the table exists (`registry.go`) and each flag is declared
  once, so the help text no longer drifts. **Still open:** which subcommands take which flag
  is unchanged, and `-a` still means two different things. Phase 4.

- [x] **Every flag is listed twice in help output** because short and long forms are
  registered as separate flags (`quadctl ls -h` shows `-a` and `-all` as two entries with
  the same text). A small custom usage printer would collapse these.
  *Fixed by `PLAN.md` 2.2:* `printFlags` renders one entry per `flagSpec` ("-a, --all").

- [ ] **`status -p` has no effect** on the non-systemd path (it calls `HandlePS` directly),
  and `-l/--long` is accepted but meaningless there (`main.go:56-64`).

- [ ] **`status`/`logs` bypass the command pipeline** when not in print mode
  (`core/handlers.go:938`, `:995` call `runCommand` directly), so they don't get the
  spinner/label/error handling everything else has.

## 4. Output & UX

- [x] **Scope silently widens from "this directory" to "everything".**
  `ps`, `stats`, `images`, `status`, `pull`, `logs` fell back to `InitAllQuadlets` when the
  CWD had no quadlets, with no indication, so `quadctl pull` run from an unrelated directory
  would happily pull every image of every quadlet on the system.
  *Fixed by `PLAN.md` Phase 4:* `main` says which directories it is about to use, whether the
  widening came from `-a` or from an empty directory.

- [x] **`quadctl create` prints nothing at all when everything already exists** — no
  commands are generated, so `RunCommands` was never called. *Fixed by `PLAN.md` Phase 4:*
  each subcommand that builds commands carries a `NothingToDo` line in the registry, printed
  when it built none.

- [x] **Warnings are hidden by default and formatted raggedly.** They only appeared with
  `-v`, were printed *before* the commands they belong to, and several messages embedded
  their own `\n` and leading spaces so the block came out with inconsistent blank lines and
  indentation.
  *Fixed by `PLAN.md` Phase 4:* the default flipped. Anything reporting that quadctl could
  not use part of a quadlet file is shown at default verbosity; commentary on how a command
  was built carries `command.InfoPrefix` and stays behind `-v`. Each line is flattened onto one
  line and prefixed with the command it belongs to.
  **Deliberately not changed:** warnings still print as a block before the first command
  runs, rather than beside it. A spinner redraws its own line, so anything interleaved with
  one is liable to be overwritten — naming the command on each line addresses what the
  ordering was standing in for.

- [x] **ANSI colour is emitted unconditionally**, including when stdout is a pipe or file —
  every table used `table.StyleColoredYellowWhiteOnBlack` and the spinner ran regardless of
  TTY. *Fixed by `PLAN.md` Phase 4:* `command.UseColor` honours `--no-color`, `NO_COLOR` and
  `TERM=dumb`, and otherwise only colours a terminal; the spinner runs only on a terminal,
  and prints its outcome line plainly when there isn't one.

- [x] **Fatal errors inside a running spinner leave it mid-animation.**
  `HandleSystemdCreate`'s file operations `os.Exit(1)` from inside `RunFn` while the spinner
  is spinning (`core/handlers.go:329`, `:357`, `:370`, `:402`, `:411`, `:432`) —
  `validateQuadletGenerationCommand` documents this exact problem and works around it
  locally (`:616-618`) instead of fixing it globally.
  *Fixed by `PLAN.md` 1.2:* those file operations and the generator validation now report a
  failed command instead of exiting, so the spinner is always torn down. Ctrl-C is handled
  too as of `PLAN.md` Phase 4: `main.handleInterrupts` stops the spinner, removes the scratch
  directories and exits 130.

- [x] **`quadctl logs` with nothing running runs `podman logs` with no arguments.** **[verified]**
  Output: `Error: specify at least one container name or ID to log`, exit 0.
  *Fixed by `PLAN.md` Phase 4:* it says no containers were found and stops.

- [x] **`stats` exits 1 when nothing matches while `ps` prints an empty table** for exactly
  the same situation. *Fixed by `PLAN.md` Phase 4:* `ps`, `stats`, `images` and `logs` all
  report "No containers found for the quadlets in \<dir\>" and exit 0.

- [x] **Selector error message misattributes the failure** (`main.go:36`): printed
  "No quadlets found in directory: `<SearchDir>`" even when the real error came from
  reading `quadlet.src.path`. *Fixed by `PLAN.md` Phase 4:* the selector's own error is
  wrapped rather than replaced.

- [x] **`list -a` prints three unlabeled trees** (`core/handlers.go:1237-1252`) — nothing
  said which was src / user / root, and the three identical error messages all read
  "Error listing quadlets in search directory". *Fixed by `PLAN.md` Phase 4:* each tree is
  headed with its role and config key, one unreadable path no longer hides the other two,
  and only all three failing is an error.

- [x] **Inconsistent labels and naming in output**: "Systemd stopping .container app" vs
  "Starting .container app" vs "Systemd installing quadlets to …"; the raw dotted type
  (`.container`) was printed as if it were a word. *Fixed by `PLAN.md` Phase 4:*
  `command.Label` builds every one of them, so they all read `Starting container app`. The
  systemd variants name the unit systemctl acts on rather than the podman resource.

- [x] **Command generation is nondeterministic.** **[verified]** *Fixed by `PLAN.md` 1.3.*
  Map iteration in
  `generateCreateCommand` (`core/handlers.go:1474`) and in `topologicalSort`'s seed loop
  (`util/parser.go:631`) reorders both the podman arguments and independent quadlets
  between runs:
  ```
  run 1: podman container create --name myapp --restart always --env FOO=bar … --network web --publish 8080:80 --label app=demo
  run 2: podman container create --name myapp --restart always --publish 8080:80 --env FOO=bar … --label app=demo -v data:/data --network web
  ```
  Makes `-p` output undiffable and start ordering unstable. Sort keys (and sort the
  topological seed by ID) for stable output.

- [x] **No preflight check for `podman` / `systemctl`.** Missing binaries surfaced as
  `exec: "podman": executable file not found in $PATH` from whichever code path happened to
  run first. *Fixed by `PLAN.md` Phase 4:* `main.checkRequiredBinaries` looks them up before
  dispatch — `systemctl` only under `-s`, and neither in print mode, which runs nothing.

- [ ] **No `--version`.** Release binaries carry no version at all
  (`.github/workflows/build_release.yml` has no `-ldflags -X`), so bug reports can't
  identify a build.

- [x] **Default paths disagree between code and shipped config**: `initState` used
  `/etc/containers/systemd/users` for `QuadletUserPath`, the template ini
  `{{.home}}/.config/containers/systemd`. *Fixed by `PLAN.md` Phase 4:*
  `config.DefaultUserQuadletPath` is the XDG path the ini writes — the other one is not
  writable by the rootless user quadctl was about to create it as. A test fails if the two
  drift apart again.

- [x] **Config parsing is silently forgiving** (`util/files.go:24-50`): booleans were
  one-way and case-sensitive (`use_symbolic_links` only reacted to `true`/`1`,
  `use_subdirectories` only to `false`/`0`), so `True`, `yes`, `on` were ignored without
  a word; unknown/misspelled keys were dropped silently.
  *Fixed by `PLAN.md` Phase 4:* `parseConfigBool` takes true/false, yes/no, on/off and 1/0
  in any case and in both directions; a value it can't read and a key quadctl doesn't know
  both land in `Config.Warnings`, which `main` prints before doing anything else.

## 5. Structure & maintainability

- [x] **Split `core/handlers.go` (1827 lines) into per-command files.**
  *Done in `PLAN.md` 2.1*, to the layout suggested here: `core/command.go`, `pull.go`,
  `create.go`, `start.go`, `run.go`, `stop.go`, `remove.go`, `systemd_install.go`,
  `systemd_lifecycle.go`, `inspect.go`, `list.go`, `generate.go`, `podman.go`. Pure moves,
  no behavior change.

- [x] **Replace the three parallel switches with one command registry.** A subcommand was
  declared in three places that had to be kept in sync — `flagSets` + `Print*Usage`
  (`util/flags.go`), the dispatch switch (`main.go:45-120`), and the handler pair in `core`.
  *Done in `PLAN.md` 2.2:* `registry.go` is the one table. See §3 for what it fixed on the
  way through.

- [x] **Stop calling `os.Exit` from library code** — 54 call sites across `util/` and
  `core/`. It makes handlers untestable, skips temp-dir cleanup, and is why exit codes are
  inconsistent. Return errors; let `main` decide the exit code. *(`PLAN.md` 1.2 — the count
  was 60 by the time it was done; `main()` is now the only exit.)*

- [x] **Factor out the repeated "resolve display name for a quadlet" block** — the same
  `resName/resType` derivation appeared in six places. *Done in `PLAN.md` Phase 4:* the name
  is resolved once at parse time and read back through `Quadlet.DisplayName`.

- [ ] **Dead code to delete or wire up.** `schema/validator.go` (~256 lines) was deleted in
  `PLAN.md` 2.3: its `Handler`/`AttributeSchema` machinery was a parallel model that shared
  nothing with the live `SchemaOption` one, so a future `validate` command (`FEATURES.md`)
  gains nothing from it. **Still unused, still open:**
  `GetKubeSchema`/`GetImageSchema`/`GetBuildSchema`/`GetVolumeSchema`, `ValidateSchema`,
  `config.ListFiles`, `config.DeleteFile`, `optKubeAutoUpdate` (`schema/kube.go:58`, shadowed
  by `optAutoUpdate()` in the list). `GetPodmanOptionsMap`/`assemblePodmanOptionsMap` went in
  `PLAN.md` 6.1, when the option indexing moved into `schema`.

- [x] **Drop the dot-imports** (`main.go:10-11`, `util/options.go:4`) — `. "…/core"`,
  `. "…/schema"` make it impossible to tell where `Command`, `SchemaOption`,
  `GetContainerOptions` come from. *Done in `PLAN.md` 2.3.*

- [x] **Commented-out code blocks** left in place: `util/parser.go:683-725` (`ParseDurationToSeconds`),
  `:740-747`, `util/tui.go:98-116`, `core/handlers.go:1790-1806`, `core/commands.go:271-273`,
  `:285`. *Done in `PLAN.md` 2.3* — also the stale `.quadlets` design note at
  `util/parser.go:181-193`, which describes work that has since shipped.

- [x] **Nothing is under `internal/`.** `core`, `util` and `schema` are all importable as
  `github.com/fkmiec/quadctl/…`. This is a CLI with no library consumers, and `internal/` is
  the only layout convention the compiler enforces — everything below `main` belongs there,
  which is also what stops any later signature change from being an API break.
  `main.go`/`registry.go` stay at the root: that is the documented shape for a single
  command, and one binary does not need `cmd/quadctl/`. *Done in `PLAN.md` 2.5.*

- [x] **`util` is a grab-bag, and the name is the documented anti-pattern.**
  [go.dev/blog/package-names](https://go.dev/blog/package-names) calls out `util`/`common`/
  `misc` by name: no context for the client, unfocused dependencies, collides with every
  other project's `util`. Here it holds five unrelated things — the domain model and INI
  parser (`parser.go`, 738 lines), config discovery and file I/O (`files.go`), the
  podman option mapping (`options.go`), a bubbletea selector (`tui.go`) and the `Runner`
  seam (`runner.go`) — plus what's left of `flags.go` (68 lines, just `ResolveSearchDir`).
  *Done in `PLAN.md` 6.1:* `util` is gone, split into `quadlet` (model, parser, `State`,
  search dir), `config` (quadctl.ini plus file I/O), `podman` (option rendering, live-state
  queries), `runner` and `tui`; the option indexing went into `schema`. `core` went with it —
  `internal/command` for the podman-direct handlers and the shared `Command` machinery,
  `internal/systemd` for the install/lifecycle half.

- [x] **No package doc comments anywhere.** Not on `core`, `util` or `schema`, and no
  `doc.go`. Every package should have one, in exactly one file. *Done in `PLAN.md` 6.2,*
  after 6.1, so the boundaries documented are the final ones.

## 6. Tests, CI, repo hygiene

- [x] **`go vet ./...` currently fails** — `schema/validator.go:181-182`, duplicate
  `json:"ignore-empty"` tags. CI (`.github/workflows/go.yml`) doesn't run vet, so it went
  unnoticed. Add `go vet` to CI. *Fixed by `PLAN.md` 2.3:* the file is gone and CI vets.

- [x] **Test coverage was one file** (`util/options_test.go`, now `podman/options_test.go`) covering schema→podman option
  templating only — nothing over the parser, dependency resolution, topological sort, path
  resolution or command generation, which is where every bug in §1 lived. *Done in `PLAN.md`
  1.4 and 2.x:* eight test files now, including `quadlet/search_test.go` (search-dir
  table test), `quadlet/parser_test.go` (fixtures under `quadlet/testdata/`),
  `command/generate_test.go` against the `command/testdata/commands.golden` golden file,
  `systemd/prune_test.go` against a temp dir, and `registry_test.go`. Layout follows the Go convention: tests beside the code,
  fixtures in `testdata/`, which the go tool ignores.

- [x] **`.gitignore` blocked test fixtures**: `*.container`, `*.pod`, `*.network`,
  `*.volume` were ignored at every level, so fixture quadlets couldn't be committed — which
  is why there were none. *Fixed in `PLAN.md` 1.4:* the patterns are anchored to the repo
  root (`/*.container`, …). Don't widen them back.

- [ ] **`go.mod` isn't tidy** — `bubbletea`, `spinner` and `goccy/go-yaml` are imported
  directly but marked `// indirect`. `go mod tidy` moves 10 lines as of this writing.

- [ ] **Release workflow doesn't run tests or vet** before publishing binaries
  (`.github/workflows/build_release.yml:25`). Phase 5. `build.sh` now builds the package
  (`PLAN.md` 2.2 — it had to, once `main` was more than one file).

- [ ] **CI only triggers on `main`** (`.github/workflows/go.yml:6-10`), so nothing runs on
  this branch.

- [ ] **No linter beyond `go vet`.** `golangci-lint` is the de facto standard meta-linter;
  `unused` alone would find the dead code §5 still tracks by hand. Pin it as a `go.mod`
  `tool` dependency (`go get -tool`, Go 1.24+) rather than installing it ad hoc, so CI and
  local runs use the same version. *`PLAN.md` 6.3 — independent of the refactor.*

- [ ] **The release workflow hand-rolls what GoReleaser does.**
  `.github/workflows/build_release.yml` runs three cross-compiles and three `tar`
  invocations by hand and still carries the workflow template's `# Replace this with your
  actual build command` comment. There are no checksums and no changelog. A
  `.goreleaser.yaml` replaces the lot and does Phase 5's `-ldflags -X` version stamping on
  the way. *`PLAN.md` 6.4.*

- [ ] **CI details.** `go test` runs without `-race`; both workflows are on
  `actions/setup-go@v4` (v5+ is current); and the Go version is pinned `'1.26.3'` in
  `go.yml` but `'1.26'` in `build_release.yml`. *`PLAN.md` 6.5.*

- [ ] **No task runner — and deliberately none yet.** `go build ./...`, `go test ./...` and
  `go vet ./...` are the build system; a Makefile wrapping only those is noise. Revisit once
  6.3–6.5 create real targets (lint, cover, cross-build, release dry-run). *`PLAN.md` 6.6.*

## 7. Documentation

- [ ] **README usage block is stale** (`README.md:70-101`): it predates `-a`, `-p`, `-v`,
  `-l`, `-d`, `-f`, `--pargs`, `--exec` and the `pull` command's flags, and the "Flags"
  section only lists `-s`.

- [ ] **Config keys are undocumented** — README describes only the three path settings
  (`README.md:64-66`); `systemd.enabled`, `use_subdirectories`, `use_symbolic_links`,
  `remove_volumes`, `remove_networks` and the `systemd.*` command templates aren't
  mentioned anywhere outside the ini comments.

- [x] **Truncated comment in the shipped config**: "Replace  with explicit values"
  (`util/config/quadctl.ini:12` and `:32`) — a word was missing, and it should say which
  variables are not expanded. *Fixed by `PLAN.md` Phase 4* while reconciling the
  `quadlet.user.path` default: both copies now name `$HOME`, `$XDG_CONFIG_HOME`,
  `$XDG_RUNTIME_DIR` and `${UID}`.

- [x] **Document the podman-direct vs systemd naming difference** once §2's
  `VolumeName`/`NetworkName` inconsistency is resolved. *Done in `PLAN.md` Phase 4:* the
  difference is gone rather than documented — both paths use quadlet's rule — and
  `README.md` "Resource naming" says what the resulting names are and why they aren't the
  file names.
