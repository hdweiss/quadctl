package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fkmiec/quadctl/internal/core"
	"github.com/fkmiec/quadctl/internal/schema"
	"github.com/fkmiec/quadctl/internal/util"
)

// defaultListDepth is the depth 'list' walks when -d is not given: enough to show the
// quadlet directories under a configured path, not the files inside them.
const defaultListDepth = 2

var (
	quadctl *util.State
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

	registry := newRegistry(quadctl)
	if err := registry.parseGlobalFlags(os.Args[1:]); err != nil {
		return fail(err)
	}

	cfg, err := util.LoadConfig(quadctl.IsRootful)
	if err != nil {
		return fail(err)
	}
	quadctl.Config = cfg
	// systemd.enabled makes systemd mode the default. It is applied after the flags because
	// there is no way to turn it back off from the CLI yet (TODO.md section 3).
	if cfg.SystemdEnabled {
		quadctl.IsSystemd = true
	}

	cmd, err := registry.processSubcommand(quadctl)
	if err != nil {
		return fail(err)
	}
	quadctl.QuadletSchemas = util.GetQuadletSchemas()

	quadlets, err := util.InitQuadlets(quadctl)
	if err != nil {
		return fail(err)
	}

	// Nothing to act on and nothing to report on: the user probably didn't notice they were
	// neither in a quadlet directory nor named one. Offer the ones under quadlet.src.path.
	if len(quadlets) < 1 && cmd.NeedsQuadlets {
		if err := displayQuadletSelector(quadctl); err != nil {
			return fail(fmt.Errorf("no quadlets found in directory: %s", quadctl.SearchDir))
		}
		if quadlets, err = util.InitQuadlets(quadctl); err != nil {
			return fail(err)
		}
	}

	// Several subcommands report on every quadlet under quadlet.src.path rather than just
	// the current directory: on -a/--all, or when the current directory turned up nothing.
	// 'logs' widens only in the latter case - it has no -a.
	if (cmd.WidensOnAll && quadctl.IsShowAll) || (cmd.WidensWhenEmpty && len(quadlets) < 1) {
		if quadlets, err = util.InitAllQuadlets(quadctl); err != nil {
			return fail(err)
		}
	}

	commands, err := cmd.dispatch(quadctl, quadlets)
	if err != nil {
		return fail(err)
	}

	if len(commands) > 0 {
		return core.RunCommands(quadctl, commands)
	}

	return 0
}

// fail reports err and yields the exit code for it. util.ErrUsage means usage has already
// been printed, so it gets no second message; errHelp means the user asked for it.
func fail(err error) int {
	if errors.Is(err, errHelp) {
		return 0
	}
	if !errors.Is(err, util.ErrUsage) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	return 1
}

// initState builds the run state. Everything the user configured comes later, from
// util.LoadConfig - what is set here is either derived from the process itself or a default
// the flags may still override.
func initState() {
	quadctl = &util.State{
		Config:         util.DefaultConfig(),
		QuadletSchemas: map[string]map[string]schema.SchemaOption{},
		Runner:         util.ExecRunner{},
		ListDepth:      defaultListDepth,
		// Rootful is a property of how quadctl was invoked, not a flag: it decides which
		// generator directory is used and whether systemctl gets --user.
		IsRootful: os.Geteuid() == 0,
	}
}

func displayQuadletSelector(quadctl *util.State) error {
	quadletDirs, err := util.ListSubdirectories(quadctl.Config.QuadletSrcPath)
	if err != nil {
		return err
	}

	if len(quadletDirs) == 0 {
		return fmt.Errorf("no quadlet directories found in %s", quadctl.Config.QuadletSrcPath)
	}
	selected, err := util.SelectFromList(quadletDirs)
	if err != nil {
		return err
	}
	quadctl.SearchDir = filepath.Join(quadctl.Config.QuadletSrcPath, selected)
	return nil
}
