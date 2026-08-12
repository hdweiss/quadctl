# Feature ideas

Not a priority — bug fixes and consistency work live in `TODO.md`. This is a parking lot
for things quadctl doesn't do yet, roughly ordered by how much they'd improve the daily
workflow relative to the effort.

## Missing basics

- **`restart` subcommand.** Today the only way to restart is `stop` + `start`, or relying
  on the fact that `-s start` reinstalls and restarts. `quadctl restart` (and
  `systemctl restart` under `-s`) is the obvious gap next to compose.

- **`enable` / `disable`.** Whether a stack comes up at boot is currently controlled only
  by hand-written `[Install] WantedBy=` sections. `quadctl -s enable <app>` would round out
  the systemd story (and for rootless, remind about `loginctl enable-linger`).

- **`--version` / `version`.** Needs `-ldflags -X main.version=$(git describe)` in the
  release workflow. Also useful: `quadctl version` reporting the detected podman version
  and whether the quadlet generator was found.

- **`logs -f` / `--tail N` / `--since`.** Both log paths are one-shot today
  (`podman logs <name>`, `journalctl -xe`). Following logs is the single most common thing
  you want after `start`.

- **`quadctl exec <container> -- cmd`** — a thin wrapper over `podman exec` that resolves
  the container name from the quadlet, so you don't need to know the generated name.

- **Shell completion** (bash/zsh/fish) for subcommands, flags and — most usefully —
  quadlet directory names under `quadlet.src.path`.

## Better feedback

- **`quadctl validate` / `lint`.** The schema already carries per-option validators and
  known-value lists (`schema/validator.go`, `PopulateValidators`), and none of it is used.
  A `validate` command that checks option names, value formats, and cross-references
  (`Pod=`, `Network=`, `Volume=` pointing at files that exist) would catch most quadlet
  mistakes before podman or the generator does. This would also give the dead code in
  `schema/validator.go` a purpose.

- **`--format json` (or `-o json`) for `ps`, `status`, `images`, `list`.** The tables are
  pretty but unparseable; JSON output would make quadctl scriptable.

- **`quadctl diff`** — show what would change between the source quadlets and what's
  currently installed under the systemd generator directory, before `-s start` overwrites
  it. Complements the existing stale-file pruning.

- **A real dry-run summary.** `-p` prints raw commands; a higher-level "will create X,
  start Y, remove Z" summary would be more readable for `-s` flows where most of the work
  is file operations.

- **Status roll-up.** `quadctl status -a` currently lists units; a per-application summary
  (app → running/stopped/failed, container count, uptime) would be a better default view.

## Broader quadlet support

- **`.image` and `.build` quadlets.** Schemas already exist (`schema/image.go`,
  `schema/build.go`) — they just need wiring into the extension map, `pull` (`.image`),
  and a `podman build` path for `.build`.

- **Multi-document / non-Pod Kubernetes YAML.** `readK8sYaml` handles exactly one
  `kind: Pod` document; real `podman kube play` inputs routinely contain Deployments,
  Services, ConfigMaps and multiple documents.

- **Quadlet drop-in directories beyond `*.conf`**, and systemd's `%i`/`%h`-style specifier
  expansion, so parsed values match what the generator actually sees.

## Workflow

- **`quadctl new <name>` / `init`** — scaffold a quadlet app directory (a `.container`
  with sensible `[Service]`/`[Install]` boilerplate, optionally a pod and volume), so
  people don't start from a blank file.

- **`quadctl import`** — generate quadlets from an existing `compose.yaml` or from a
  `podman run …` command line (podlet does this; wrapping or vendoring it would remove the
  biggest onboarding barrier for the compose crowd).

- **Watch mode** — `quadctl -s start --watch` reinstalls and restarts when the source
  quadlet files change. Fits the "edit locally, iterate fast" story the README pitches, and
  is a safer alternative to the symlink workflow the README currently warns against.

- **Environment variable expansion in the config** — `quadlet.src.path=$HOME/.local/quadlets`
  currently has to be spelled out literally, which is also why the shipped config has to
  template `{{.home}}` at install time and why `sudo` needs `QUADCTL_CONFIG_DIR`.

- **Per-project config** — a `.quadctl.ini` in the quadlet directory overriding the global
  one (install paths, symlink behavior) for stacks with different needs.

- **Remote hosts** — pass `--connection`/`--url` through to podman so a stack can be
  brought up on a remote machine, mirroring `DOCKER_HOST`.

## Testing infrastructure

- **Fake-podman integration harness.** A `podman` shim on `PATH` that records the command
  lines it was invoked with, plus fixture quadlet directories, would let the whole
  create/start/stop/remove flow be tested end to end without touching the real container
  runtime — including the systemd install path against a temp "generator" directory.
  This is what would make the §1 bugs in `TODO.md` regression-proof.
