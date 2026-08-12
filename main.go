package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/fkmiec/quadctl/internal/command"
	"github.com/fkmiec/quadctl/internal/config"
	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/schema"
	"github.com/fkmiec/quadctl/internal/tui"
)

// defaultListDepth is the depth 'list' walks when -d is not given: enough to show the
// quadlet directories under a configured path, not the files inside them.
const defaultListDepth = 2

var (
	quadctl *quadlet.State
)

func main() {
	os.Exit(run())
}

// run is the whole program. It returns the process exit code and is the only place that
// decides one: nothing below main is allowed to call os.Exit, so every failure travels back
// here as an error and every command failure comes back as RunCommands' exit code.
//
// The subcommand itself comes from the registry in registry.go - what flags it takes, what
// it does with an empty search directory, and which handler runs.
func run() int {
	initState() //Create the initial quadctl state object
	// The scratch directories a .quadlets bundle is extracted into are read by the systemd
	// install commands, so they can only go once every command has run.
	defer quadctl.Cleanup()
	handleInterrupts(quadctl)

	registry := newRegistry(quadctl)
	if err := registry.parseGlobalFlags(os.Args[1:]); err != nil {
		return fail(err)
	}

	cfg, err := config.LoadConfig(quadctl.IsRootful)
	if err != nil {
		return fail(err)
	}
	quadctl.Config = cfg
	// A key quadctl doesn't know, or a boolean it couldn't read, is reported rather than
	// dropped: a misspelled setting otherwise looks exactly like one that doesn't work.
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", w)
	}
	if err := resolveSystemdMode(quadctl, cfg); err != nil {
		return fail(err)
	}

	cmd, err := registry.processSubcommand(quadctl)
	if err != nil {
		return fail(err)
	}
	quadctl.QuadletSchemas = schema.AllQuadletOptions()

	quadlets, err := quadlet.InitQuadlets(quadctl)
	if err != nil {
		return fail(err)
	}

	// Nothing to act on and nothing to report on: the user probably didn't notice they were
	// neither in a quadlet directory nor named one. Offer the ones under quadlet.src.path.
	if len(quadlets) < 1 && cmd.NeedsQuadlets {
		// The selector's own failure - an unreadable quadlet.src.path, no directories under
		// it, a cancelled prompt - used to be reported as "no quadlets found in <SearchDir>",
		// which names the wrong directory and the wrong problem (TODO.md section 4).
		if err := displayQuadletSelector(quadctl); err != nil {
			return fail(fmt.Errorf("no quadlets in %s, and no other directory to offer: %w", quadctl.SearchDir, err))
		}
		if quadlets, err = quadlet.InitQuadlets(quadctl); err != nil {
			return fail(err)
		}
	}

	// Several subcommands report on every quadlet under quadlet.src.path rather than just
	// the current directory: on -a/--all, or when the current directory turned up nothing.
	// 'logs' widens only in the latter case - it has no -a.
	widenedBecauseEmpty := cmd.WidensWhenEmpty && len(quadlets) < 1
	if (cmd.WidensOnAll && quadctl.IsShowAll) || widenedBecauseEmpty {
		// Say so. Widening used to be silent, so 'quadctl pull' run from an unrelated
		// directory would quietly pull every image of every quadlet on the machine
		// (TODO.md section 4).
		if widenedBecauseEmpty {
			fmt.Fprintf(os.Stderr, "No quadlets in %s - using all quadlets under %s.\n",
				quadctl.SearchDir, quadctl.Config.QuadletSrcPath)
		} else {
			fmt.Fprintf(os.Stderr, "Using all quadlets under %s.\n", quadctl.Config.QuadletSrcPath)
		}
		if quadlets, err = quadlet.InitAllQuadlets(quadctl); err != nil {
			return fail(err)
		}
	}

	if err := checkRequiredBinaries(quadctl); err != nil {
		return fail(err)
	}

	commands, err := cmd.dispatch(quadctl, quadlets)
	if err != nil {
		return fail(err)
	}

	if len(commands) == 0 {
		// A subcommand that builds commands and built none did nothing, which is worth a word:
		// 'quadctl create' with everything already in place used to print absolutely nothing
		// (TODO.md section 4). The handlers that report rather than build - ps, images, list -
		// have already printed, so they are not it.
		if cmd.NothingToDo != "" {
			fmt.Fprintf(os.Stderr, "Nothing to do: %s\n", cmd.NothingToDo)
		}
		return 0
	}

	return command.RunCommands(quadctl, commands)
}

// handleInterrupts tears down cleanly on Ctrl-C. It lives in main because it has to exit, and
// main is the only place allowed to: the spinner has to stop before anything else prints, and
// the scratch directories have to go, neither of which a default signal disposition would do.
func handleInterrupts(quadctl *quadlet.State) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-ch
		command.StopSpinner()
		fmt.Fprintf(os.Stderr, "\nInterrupted (%s).\n", sig)
		quadctl.Cleanup()
		// 128 + SIGINT, the shell convention for "killed by a signal".
		os.Exit(130)
	}()
}

// checkRequiredBinaries fails early, and with a sentence about what is missing, rather than
// letting whichever code path happens to run first surface
// `exec: "podman": executable file not found in $PATH` (TODO.md section 4). Print mode runs
// nothing, so it needs neither binary.
func checkRequiredBinaries(quadctl *quadlet.State) error {
	if quadctl.IsPrintOnly {
		return nil
	}
	required := []string{"podman"}
	if quadctl.IsSystemd {
		required = append(required, "systemctl")
	}
	for _, bin := range required {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("%s is required but was not found in $PATH", bin)
		}
	}
	return nil
}

// resolveSystemdMode settles whether this run goes through systemd. The config is read after
// the flags - it may name the search directory a flag argument refers to - so systemd.enabled
// is applied here rather than at parse time, and --no-systemd is what makes it overridable
// from the command line (TODO.md section 3).
func resolveSystemdMode(quadctl *quadlet.State, cfg *config.Config) error {
	if quadctl.IsSystemd && quadctl.IsNoSystemd {
		return fmt.Errorf("-s/--systemd and --no-systemd contradict each other; pass one or neither")
	}
	if cfg.SystemdEnabled {
		quadctl.IsSystemd = true
	}
	if quadctl.IsNoSystemd {
		quadctl.IsSystemd = false
	}
	return nil
}

// fail reports err and yields the exit code for it. errUsage means usage has already
// been printed, so it gets no second message; errHelp means the user asked for it.
func fail(err error) int {
	if errors.Is(err, errHelp) {
		return 0
	}
	if !errors.Is(err, errUsage) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	return 1
}

// initState builds the run state. Everything the user configured comes later, from
// config.LoadConfig - what is set here is either derived from the process itself or a default
// the flags may still override.
func initState() {
	quadctl = &quadlet.State{
		Config:         config.DefaultConfig(),
		QuadletSchemas: map[string]map[string]schema.SchemaOption{},
		Runner:         runner.ExecRunner{},
		ListDepth:      defaultListDepth,
		// Rootful is a property of how quadctl was invoked, not a flag: it decides which
		// generator directory is used and whether systemctl gets --user.
		IsRootful: os.Geteuid() == 0,
	}
}

func displayQuadletSelector(quadctl *quadlet.State) error {
	quadletDirs, err := config.ListSubdirectories(quadctl.Config.QuadletSrcPath)
	if err != nil {
		return err
	}

	if len(quadletDirs) == 0 {
		return fmt.Errorf("no quadlet directories found in %s", quadctl.Config.QuadletSrcPath)
	}
	selected, err := tui.SelectFromList(quadletDirs)
	if err != nil {
		return err
	}
	quadctl.SearchDir = filepath.Join(quadctl.Config.QuadletSrcPath, selected)
	return nil
}
