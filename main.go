package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/template"

	. "github.com/fkmiec/quadctl/core"
	. "github.com/fkmiec/quadctl/schema"
	"github.com/fkmiec/quadctl/util"
)

var (
	quadctl *util.Quadctl
)

func main() {
	os.Exit(run())
}

// run is the whole program. It returns the process exit code and is the only place that
// decides one: nothing below main is allowed to call os.Exit, so every failure travels back
// here as an error and every command failure comes back as RunCommands' exit code.
func run() int {
	initState() //Create the initial quadctl state object

	if err := util.InitFlags(quadctl); err != nil {
		return fail(err)
	}
	if err := util.InitConfig(quadctl); err != nil {
		return fail(err)
	}
	if err := util.ProcessSubcommand(quadctl); err != nil {
		return fail(err)
	}
	quadctl.QuadletSchemas = util.GetQuadletSchemas()

	quadlets, err := util.InitQuadlets(quadctl)
	if err != nil {
		return fail(err)
	}

	// If no quadlets at this point, only list|ls is still a valid command.
	// Abort with a message. User probably didn't notice they were neither in a quadlet directory nor specified one as argument.
	if len(quadlets) < 1 && !(slices.Contains([]string{"list", "ls", "logs", "ps", "pull", "images", "status", "stats"}, quadctl.Subcommand)) {
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
	widens := slices.Contains([]string{"ps", "stats", "status", "images", "pull"}, quadctl.Subcommand) && (quadctl.IsShowAll || len(quadlets) < 1)
	if widens || (quadctl.Subcommand == "logs" && len(quadlets) < 1) {
		if quadlets, err = util.InitAllQuadlets(quadctl); err != nil {
			return fail(err)
		}
	}

	var commands []Command

	// Route to appropriate subcommand handler
	switch quadctl.Subcommand {
	case "ps":
		err = HandlePS(quadctl, quadlets)
	case "stats":
		err = HandleStats(quadctl, quadlets)
	case "status":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdStatus(quadctl, quadlets)
		} else {
			err = HandlePS(quadctl, quadlets)
		}
	case "logs":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdLogs(quadctl, quadlets)
		} else {
			commands, err = HandleLogs(quadctl, quadlets)
		}
	case "images":
		err = HandleImages(quadctl.Runner, quadlets)
	case "pull":
		commands, err = HandlePull(quadctl, quadlets)
	case "list", "ls":
		err = HandleList(quadctl)
	case "create":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdCreate(quadctl, quadlets)
		} else {
			commands, err = HandleCreate(quadctl, quadlets)
		}
	case "start":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdStart(quadctl, quadlets)
		} else {
			commands, err = HandleStart(quadctl, quadlets)
		}
	case "run":
		if quadctl.IsSystemd {
			fmt.Printf("Running containers with systemd (ie. 'quadctl -s run') is not supported since systemd manages the lifecycle of services independently. Use 'start' to start the services and ensure your quadlets are configured to run the desired commands on startup.\n")
		} else {
			commands, err = HandleRun(quadctl, quadlets)
		}
	case "stop":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdStop(quadctl, quadlets, false)
		} else {
			commands, err = HandleStop(quadctl, quadlets)
		}
	case "remove", "rm":
		if quadctl.IsSystemd {
			commands, err = HandleSystemdRemove(quadctl, quadlets)
		} else {
			commands, err = HandleRemove(quadctl, quadlets)
		}
	default:
		// Unreachable: ProcessSubcommand rejects anything not in the flag-set table.
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", quadctl.Subcommand)
		util.PrintUsage()
		return 1
	}

	if err != nil {
		return fail(err)
	}

	if len(commands) > 0 {
		return RunCommands(quadctl, commands)
	}

	return 0
}

// fail reports err and yields the exit code for it. util.ErrUsage means usage has already
// been printed, so it gets no second message.
func fail(err error) int {
	if !errors.Is(err, util.ErrUsage) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	return 1
}

func initState() {

	quadctl = &util.Quadctl{
		QuadletSchemas:    map[string]map[string]SchemaOption{},
		Config:            map[string]string{},
		Runner:            util.ExecRunner{},
		IsRootful:         false,
		IsSystemd:         false,
		IsPrintOnly:       false,
		IsVerbose:         false,
		IsFile:            false,
		ListDepth:         2,
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
