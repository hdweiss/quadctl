package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/fkmiec/quadctl/core"
	"github.com/fkmiec/quadctl/schema"
	"github.com/fkmiec/quadctl/util"
)

// defaultListDepth is the depth 'list' walks when -d is not given: enough to show the
// quadlet directories under a configured path, not the files inside them.
const defaultListDepth = 2

var (
	quadctl *util.Quadctl
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

	registry := newRegistry(quadctl)
	if err := registry.parseGlobalFlags(os.Args[1:]); err != nil {
		return fail(err)
	}
	if err := util.InitConfig(quadctl); err != nil {
		return fail(err)
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

func initState() {

	quadctl = &util.Quadctl{
		QuadletSchemas:    map[string]map[string]schema.SchemaOption{},
		Config:            map[string]string{},
		Runner:            util.ExecRunner{},
		IsRootful:         false,
		IsSystemd:         false,
		IsPrintOnly:       false,
		IsVerbose:         false,
		IsFile:            false,
		ListDepth:         defaultListDepth,
		Subcommand:        "",
		SearchDir:         "",
		PodmanArgs:        "",
		RunCmd:            "",
		QuadletSrcPath:    "",    // Path to the user's source directory containing quadlet folders or files
		UseSubdirectories: true,  // Default to installing quadlets in a subdirectory to keep them organized
		UseSymbolicLinks:  false, // Default to copying files for installation to avoid potential issues with source files being moved or deleted, but can be configured to use symbolic links for a more dynamic setup
		IsReloadSystemd:   true,  // Default to reloading systemd after installation to apply changes immediately
		IsRemoveVolumes:   true,  // Default to removing volumes on uninstall since they are often not needed after uninstall and can be left behind if not removed, but can be configured to keep volumes for data persistence.
		IsRemoveNetworks:  true,  // Default to removing networks on uninstall since they are often not needed after uninstall and can be left behind if not removed, but can be configured to keep volumes for data persistence.
		QuadletRootPath:   "/etc/containers/systemd",
		QuadletUserPath:   "/etc/containers/systemd/users",
	}
	quadctl.SystemdStartTmpl = template.Must(template.New("systemdStart").Parse("systemctl {{.user}} start"))
	quadctl.SystemdStopTmpl = template.Must(template.New("systemdStop").Parse("systemctl {{.user}} stop"))
	quadctl.SystemdStatusTmpl = template.Must(template.New("systemdStatus").Parse("systemctl {{.user}} status"))
	quadctl.SystemdReloadTmpl = template.Must(template.New("systemdReload").Parse("systemctl {{.user}} daemon-reload"))
	quadctl.SystemdLogsTmpl = template.Must(template.New("systemdLogs").Parse("journalctl {{.user}} -xe"))

	// Determine if running as root
	if os.Geteuid() == 0 {
		quadctl.IsRootful = true
	}
}

func displayQuadletSelector(quadctl *util.Quadctl) error {
	quadletDirs, err := util.ListSubdirectories(quadctl.QuadletSrcPath)
	if err != nil {
		return err
	}

	if len(quadletDirs) == 0 {
		return fmt.Errorf("no quadlet directories found in %s", quadctl.QuadletSrcPath)
	}
	selected, err := util.SelectFromList(quadletDirs)
	if err != nil {
		return err
	}
	quadctl.SearchDir = filepath.Join(quadctl.QuadletSrcPath, selected)
	return nil
}
