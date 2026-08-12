package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/fkmiec/quadctl/internal/core"
	"github.com/fkmiec/quadctl/internal/util"
)

// This file is the single declaration of quadctl's command line. A subcommand is one entry
// in the subcommands table below: its names, the flags it accepts (drawn from the flag
// catalogue, so the same flag reads the same way everywhere), its help text, and the
// handler(s) it dispatches to. Adding a subcommand means adding a row here and the handler
// it points at - nothing else (PLAN.md 2.2).

// --- FLAG CATALOGUE ---

// flagSpec is one flag, declared once and referenced by any number of subcommands. Short and
// long forms are a single entry, so they are registered together and rendered together in
// help output rather than appearing as two unrelated flags.
type flagSpec struct {
	Name     string // long form, without dashes
	Short    string // short form, "" if the flag has none
	Arg      string // value placeholder shown in help; empty for booleans
	Default  string // default shown in help; empty to omit
	Usage    string
	register func(fs *flag.FlagSet, quadctl *util.State, name string, usage string)
}

// names renders the flag as it appears in help: "-p, --print", or "    --pargs" when there
// is no short form, so the long forms stay aligned.
func (f flagSpec) names() string {
	if f.Short == "" {
		return "    --" + f.Name
	}
	return "-" + f.Short + ", --" + f.Name
}

func boolFlag(get func(*util.State) *bool) func(*flag.FlagSet, *util.State, string, string) {
	return func(fs *flag.FlagSet, quadctl *util.State, name, usage string) {
		fs.BoolVar(get(quadctl), name, false, usage)
	}
}

func stringFlag(get func(*util.State) *string) func(*flag.FlagSet, *util.State, string, string) {
	return func(fs *flag.FlagSet, quadctl *util.State, name, usage string) {
		fs.StringVar(get(quadctl), name, "", usage)
	}
}

var (
	// flagSystemd is the one global flag: accepted both before the subcommand and after it,
	// since every subcommand's help talks about it.
	flagSystemd = flagSpec{
		Name: "systemd", Short: "s", Default: "false",
		Usage:    "Manage services through systemd rather than podman directly",
		register: boolFlag(func(q *util.State) *bool { return &q.IsSystemd }),
	}
	// flagNoSystemd exists because the config is read after the flags are parsed, so a
	// systemd.enabled=true in quadctl.ini would otherwise turn -s on again no matter what was
	// typed - leaving no way to run a one-off podman-direct command, and 'run' permanently
	// unreachable (TODO.md section 3).
	flagNoSystemd = flagSpec{
		Name: "no-systemd", Default: "false",
		Usage:    "Manage services with podman directly, overriding systemd.enabled in quadctl.ini",
		register: boolFlag(func(q *util.State) *bool { return &q.IsNoSystemd }),
	}
	flagFile = flagSpec{
		Name: "file", Short: "f", Default: "false",
		Usage:    "Treat the given path as a single quadlet file rather than a directory",
		register: boolFlag(func(q *util.State) *bool { return &q.IsFile }),
	}
	flagPrint = flagSpec{
		Name: "print", Short: "p", Default: "false",
		Usage:    "Print the commands that would run, without executing them",
		register: boolFlag(func(q *util.State) *bool { return &q.IsPrintOnly }),
	}
	flagVerbose = flagSpec{
		Name: "verbose", Short: "v", Default: "false",
		Usage:    "Print detailed information about command execution and warnings",
		register: boolFlag(func(q *util.State) *bool { return &q.IsVerbose }),
	}
	flagAll = flagSpec{
		Name: "all", Short: "a", Default: "false",
		Usage:    "Apply to every quadlet under quadlet.src.path, not just the current directory",
		register: boolFlag(func(q *util.State) *bool { return &q.IsShowAll }),
	}
	flagListAll = flagSpec{
		Name: "all", Short: "a", Default: "false",
		Usage:    "List quadlets in all configured paths (src, systemd user and systemd root)",
		register: boolFlag(func(q *util.State) *bool { return &q.IsListAll }),
	}
	flagLong = flagSpec{
		Name: "long", Short: "l", Default: "false",
		Usage:    "Display long format output from systemctl status",
		register: boolFlag(func(q *util.State) *bool { return &q.IsLongStatus }),
	}
	flagDepth = flagSpec{
		Name: "depth", Short: "d", Arg: "int", Default: strconv.Itoa(defaultListDepth),
		Usage: "Depth of the quadlet directory listing",
		register: func(fs *flag.FlagSet, quadctl *util.State, name, usage string) {
			fs.IntVar(&quadctl.ListDepth, name, defaultListDepth, usage)
		},
	}
	flagPodmanArgs = flagSpec{
		Name: "pargs", Arg: "string",
		Usage:    "Additional arguments to pass to podman (e.g. --pargs='--rm -it')",
		register: stringFlag(func(q *util.State) *string { return &q.PodmanArgs }),
	}
	flagExec = flagSpec{
		Name: "exec", Arg: "string",
		Usage:    "Command to execute in the container (e.g. --exec='/bin/bash')",
		register: stringFlag(func(q *util.State) *string { return &q.RunCmd }),
	}
)

// globalFlags are accepted before the subcommand and are also registered on every
// subcommand's flag set, so 'quadctl -s start' and 'quadctl start -s' both work.
var globalFlags = []flagSpec{flagSystemd, flagNoSystemd}

// --- SUBCOMMAND TABLE ---

// handlerFn is the shape every subcommand dispatches through. Handlers that print rather
// than generate commands (ps, stats, images, list) return no commands.
type handlerFn func(*util.State, []*util.Quadlet) ([]core.Command, error)

type subcommand struct {
	Name     string
	Aliases  []string
	Summary  string   // one line, for the top-level command list
	Synopsis string   // first line of the subcommand's own help
	Notes    []string // trailing help paragraph
	Flags    []flagSpec

	// Run is the podman-direct implementation. RunSystemd is the systemd one, used when -s
	// is set; nil means -s makes no difference to this subcommand (ps, stats, images) or is
	// handled inside Run itself (list).
	Run        handlerFn
	RunSystemd handlerFn

	// Wrapper lists the subcommand under "Wrapper commands" in the top-level usage.
	Wrapper bool
	// NeedsQuadlets makes an empty search directory an error worth prompting about: without
	// quadlets there is nothing for these to act on.
	NeedsQuadlets bool
	// WidensWhenEmpty falls back to every quadlet under quadlet.src.path when the search
	// directory turned up nothing; WidensOnAll does the same when -a is given.
	WidensWhenEmpty bool
	WidensOnAll     bool

	flagSet *flag.FlagSet // built by newRegistry
}

// commands returns the subcommand table. Order is the order they appear in the top-level
// usage.
func commands() []*subcommand {
	return []*subcommand{
		{
			Name:            "pull",
			Summary:         "Pull required images",
			Synopsis:        "Pull images defined in quadlet files.",
			Flags:           []flagSpec{flagFile, flagPrint, flagAll},
			Run:             core.HandlePull,
			WidensWhenEmpty: true,
			WidensOnAll:     true,
			Notes: []string{
				"Use -a to pull images for all quadlets under quadlet.src.path, not just the current directory.",
			},
		},
		{
			Name:          "create",
			Summary:       "Create resources (do not start). Use -s to generate quadlets under systemd.",
			Synopsis:      "Create resources (pod, container, volume, network) defined in quadlet files.",
			Flags:         []flagSpec{flagFile, flagPrint, flagVerbose},
			Run:           core.HandleCreate,
			RunSystemd:    core.HandleSystemdCreate,
			NeedsQuadlets: true,
			Notes: []string{
				"Use sudo for rootful quadlets.",
				"Use -s to create under systemd.",
			},
		},
		{
			Name:          "start",
			Summary:       "Create (if missing) and start resources. Use -s to start under systemd.",
			Synopsis:      "Start resources (pod, container, volume, network) defined in quadlet files.",
			Flags:         []flagSpec{flagFile, flagPrint, flagVerbose},
			Run:           core.HandleStart,
			RunSystemd:    core.HandleSystemdStart,
			NeedsQuadlets: true,
			Notes: []string{
				"Use sudo for rootful quadlets.",
				"Use -s to start under systemd.",
			},
		},
		{
			Name:          "run",
			Summary:       "Run a single .container in the foreground. Not supported under systemd.",
			Synopsis:      "Run resources (pod, container, volume, network) defined in quadlet files.",
			Flags:         []flagSpec{flagFile, flagPodmanArgs, flagExec, flagPrint, flagVerbose},
			Run:           core.HandleRun,
			RunSystemd:    refuseSystemd("systemd manages service lifecycles independently. Use 'start' to start the services and ensure your quadlets are configured to run the desired commands on startup"),
			NeedsQuadlets: true,
			Notes: []string{
				"Will run a single .container quadlet in the foreground. Other quadlets will be run in background.",
				"For example, pass '-it' in PodmanArgs for interactive terminal use.",
				"Use --pargs to pass podman args on the quadctl command line. Equivalent to the PodmanArgs quadlet option.",
				"The run command (ie. running in foreground) is incompatible with systemd.",
				"If multiple .container files are found, all but one must have -d (--detach) defined in PodmanArgs.",
				"Using run where all .container quadlets have -d (--detach) in PodmanArgs is same as 'quadctl start'.",
			},
		},
		{
			Name:     "stop",
			Summary:  "Stop running services (do not remove). Use -s to stop under systemd.",
			Synopsis: "Stop resources (pod, container, volume, network) defined in quadlet files.",
			Flags:    []flagSpec{flagFile, flagPrint, flagVerbose},
			Run:      core.HandleStop,
			RunSystemd: func(q *util.State, qs []*util.Quadlet) ([]core.Command, error) {
				return core.HandleSystemdStop(q, qs, false)
			},
			NeedsQuadlets: true,
			Notes: []string{
				"Use sudo for rootful quadlets.",
				"Use -s to stop under systemd.",
			},
		},
		{
			Name:          "remove",
			Aliases:       []string{"rm"},
			Summary:       "Remove stopped resources. Use -s to remove generated quadlets under systemd.",
			Synopsis:      "Remove resources (pod, container, volume, network) defined in quadlet files.",
			Flags:         []flagSpec{flagFile, flagPrint, flagVerbose},
			Run:           core.HandleRemove,
			RunSystemd:    core.HandleSystemdRemove,
			NeedsQuadlets: true,
			Notes: []string{
				"Will stop running resources if needed and remove networks and volumes.",
				"Use sudo for rootful quadlets.",
				"Use -s to remove under systemd.",
			},
		},
		{
			Name:            "logs",
			Summary:         "Show logs of running containers. Use -s to view systemd logs.",
			Synopsis:        "Display logs.",
			Flags:           []flagSpec{flagPrint},
			Run:             core.HandleLogs,
			RunSystemd:      core.HandleSystemdLogs,
			WidensWhenEmpty: true,
			Notes: []string{
				"Helper command to open relevant logs (particularly under systemd with -s).",
				"Use sudo for rootful quadlets.",
			},
		},
		{
			Name:     "list",
			Aliases:  []string{"ls"},
			Summary:  "List quadlets in the configured quadlet path, or the systemd path with -s.",
			Synopsis: "Display a tree view of quadlet directories and files.",
			Flags:    []flagSpec{flagDepth, flagListAll},
			// HandleList reads IsSystemd itself to pick which configured path to walk.
			Run: func(quadctl *util.State, _ []*util.Quadlet) ([]core.Command, error) {
				return nil, core.HandleList(quadctl)
			},
			Notes: []string{
				"'quadctl list' will display quadlets under your configured quadlet.src.path.",
				"'quadctl -s list' will display quadlets under your configured quadlet.user.path.",
				"'sudo quadctl -s list' will display quadlets under your configured quadlet.root.path.",
				"'quadctl list -a' will display quadlets in all three configured paths.",
				"At default depth only quadlet directories are listed. Add -d [3+] to list files.",
			},
		},
		{
			Name:     "images",
			Wrapper:  true,
			Summary:  "Show images defined for the set of related quadlets.",
			Synopsis: "List images defined in quadlet files.",
			Flags:    []flagSpec{flagFile, flagAll},
			Run: func(quadctl *util.State, quadlets []*util.Quadlet) ([]core.Command, error) {
				return nil, core.HandleImages(quadctl.Runner, quadlets)
			},
			WidensWhenEmpty: true,
			WidensOnAll:     true,
			Notes: []string{
				"Displays the same information whether containers are running under systemd or podman.",
				"The -s flag is not required or supported.",
				"Use -a to show images for all quadlets under quadlet.src.path, not just the current directory.",
			},
		},
		{
			Name:     "ps",
			Wrapper:  true,
			Summary:  "Show state of containers.",
			Synopsis: "Display state of containers defined in quadlet files.",
			Flags:    []flagSpec{flagFile, flagAll},
			Run: func(quadctl *util.State, quadlets []*util.Quadlet) ([]core.Command, error) {
				return nil, core.HandlePS(quadctl, quadlets)
			},
			WidensWhenEmpty: true,
			WidensOnAll:     true,
			Notes: []string{
				"Will display state of both running and stopped containers.",
				"Displays the same information whether containers are running under systemd or podman.",
				"The -s flag is not required or supported.",
				"Use -a to show containers for all quadlets under quadlet.src.path, not just the current directory.",
			},
		},
		{
			Name:     "stats",
			Wrapper:  true,
			Summary:  "Show live stats for containers.",
			Synopsis: "Display live stats of running containers defined in quadlet files.",
			Flags:    []flagSpec{flagFile, flagAll},
			Run: func(quadctl *util.State, quadlets []*util.Quadlet) ([]core.Command, error) {
				return nil, core.HandleStats(quadctl, quadlets)
			},
			WidensWhenEmpty: true,
			WidensOnAll:     true,
			Notes: []string{
				"Displays the same information whether containers are running under systemd or podman.",
				"The -s flag is not required or supported.",
				"Use -a to show stats for all quadlets under quadlet.src.path, not just the current directory.",
			},
		},
		{
			Name:     "status",
			Wrapper:  true,
			Summary:  "Show current status. Use -s to see systemd status.",
			Synopsis: "Display status for resources (pod, container, volume, network) defined in quadlet files.",
			Flags:    []flagSpec{flagLong, flagFile, flagPrint, flagAll},
			// Without -s, status is ps.
			Run: func(quadctl *util.State, quadlets []*util.Quadlet) ([]core.Command, error) {
				return nil, core.HandlePS(quadctl, quadlets)
			},
			RunSystemd:      core.HandleSystemdStatus,
			WidensWhenEmpty: true,
			WidensOnAll:     true,
			Notes: []string{
				"Will display systemd status if -s provided. Otherwise, calls 'quadctl ps'.",
				"Calls 'podman quadlet list' for systemd status by default, 'systemctl status' with -l.",
				"Use sudo for rootful quadlets.",
				"Use -a to show status for all quadlets under quadlet.src.path, not just the current directory.",
			},
		},
	}
}

// refuseSystemd builds a RunSystemd that reports the subcommand as unsupported under
// systemd. Refusing is a failure, so it travels back as an error and exits non-zero rather
// than printing an explanation and claiming success.
func refuseSystemd(why string) handlerFn {
	return func(quadctl *util.State, _ []*util.Quadlet) ([]core.Command, error) {
		return nil, fmt.Errorf("'%s' is not supported with systemd (-s): %s", quadctl.Subcommand, why)
	}
}

// --- LOOKUP AND PARSING ---

type registry struct {
	commands []*subcommand
	byName   map[string]*subcommand
	global   *flag.FlagSet
	args     []string // what is left after the global flags: the subcommand and its own arguments
}

// newRegistry builds every flag set up front, before anything is parsed. Registering a flag
// writes its default through the pointer it targets, so a subcommand flag set built after
// the global parse would undo a -s given before the subcommand.
func newRegistry(quadctl *util.State) *registry {
	r := &registry{commands: commands(), byName: map[string]*subcommand{}}

	r.global = flag.NewFlagSet(util.ToolName, flag.ContinueOnError)
	r.global.Usage = r.printUsage
	registerFlags(r.global, quadctl, globalFlags)

	for _, c := range r.commands {
		c.flagSet = flag.NewFlagSet(c.Name, flag.ContinueOnError)
		c.flagSet.Usage = c.printUsage
		registerFlags(c.flagSet, quadctl, append(append([]flagSpec{}, c.Flags...), globalFlags...))

		r.byName[c.Name] = c
		for _, alias := range c.Aliases {
			r.byName[alias] = c
		}
	}
	return r
}

// registerFlags registers each spec under both its long and short form, pointed at the same
// field of the run state.
func registerFlags(fs *flag.FlagSet, quadctl *util.State, flags []flagSpec) {
	for _, f := range flags {
		f.register(fs, quadctl, f.Name, f.Usage)
		if f.Short != "" {
			f.register(fs, quadctl, f.Short, f.Usage)
		}
	}
}

// parseGlobalFlags parses the flags that precede the subcommand. It runs before the config
// is loaded, so nothing here may depend on config values.
func (r *registry) parseGlobalFlags(argv []string) error {
	if err := r.global.Parse(argv); err != nil {
		return usageError(err)
	}
	r.args = r.global.Args()
	if len(r.args) < 1 {
		r.printUsage()
		return util.ErrUsage
	}
	return nil
}

// processSubcommand resolves the subcommand, parses its flags and the path argument, and
// derives the search directory. It runs after the config is loaded because the path
// argument may name a directory under quadlet.src.path.
func (r *registry) processSubcommand(quadctl *util.State) (*subcommand, error) {
	name := strings.ToLower(r.args[0])
	c, ok := r.byName[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", name)
		r.printUsage()
		return nil, util.ErrUsage
	}
	// The canonical name, not the alias the user typed, so downstream comparisons only ever
	// have to know one spelling.
	quadctl.Subcommand = c.Name

	if err := c.flagSet.Parse(r.args[1:]); err != nil {
		return nil, usageError(err)
	}
	// The path argument belongs to the subcommand's own FlagSet, not the global one -
	// flag.Arg(1) would be whatever flag happened to follow the subcommand.
	quadctl.PathArg = c.flagSet.Arg(0)

	searchDir, err := util.ResolveSearchDir(quadctl, quadctl.PathArg)
	if err != nil {
		return nil, err
	}
	quadctl.SearchDir = searchDir

	return c, nil
}

// dispatch runs the subcommand, picking the systemd implementation when one exists and -s
// was given.
func (c *subcommand) dispatch(quadctl *util.State, quadlets []*util.Quadlet) ([]core.Command, error) {
	if quadctl.IsSystemd && c.RunSystemd != nil {
		return c.RunSystemd(quadctl, quadlets)
	}
	return c.Run(quadctl, quadlets)
}

// usageError maps the flag package's outcomes onto quadctl's. flag has already written the
// message and the usage text in both cases; ErrHelp means -h, which is a successful exit.
func usageError(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return errHelp
	}
	return util.ErrUsage
}

// errHelp reports that help was asked for and printed - the one non-error reason to stop
// before doing any work.
var errHelp = errors.New("help requested")

// --- USAGE ---

func (r *registry) printUsage() {
	w := os.Stderr
	fmt.Fprintf(w, "Orchestrator for Podman Quadlets (with and without systemd)\n")
	fmt.Fprintf(w, "Usage: %s [flags] <command> [flags] [path]\n\n", util.ToolName)
	fmt.Fprintf(w, "Flags:\n")
	printFlags(w, globalFlags)

	fmt.Fprintf(w, "\nCommands:\n")
	printCommandList(w, r.commands, false)
	fmt.Fprintf(w, "\nWrapper commands (filtered to defined resources):\n")
	printCommandList(w, r.commands, true)

	fmt.Fprintf(w, "\nRequirements:\n")
	fmt.Fprintf(w, "  A quadctl.ini config file is required. Default location is $HOME/.config/quadctl.\n    A default config file will be created if not found.\n")
	fmt.Fprintf(w, "  Set QUADCTL_CONFIG_DIR=<absolute path to config directory> in /etc/environment to\n    change config location and/or ensure found when using sudo.\n")
	fmt.Fprintf(w, "\nRun '%s <command> -h' for the flags and notes of a single command.\n", util.ToolName)
}

func (c *subcommand) printUsage() {
	w := os.Stderr
	fmt.Fprintf(w, "%s\n", c.Synopsis)
	fmt.Fprintf(w, "Usage: %s [flags] %s [flags] [path]\n\n", util.ToolName, c.displayName())
	fmt.Fprintf(w, "Flags:\n")
	printFlags(w, append(append([]flagSpec{}, c.Flags...), globalFlags...))
	if len(c.Notes) > 0 {
		fmt.Fprintf(w, "\nNotes:\n")
		for _, n := range c.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
}

// displayName is "remove|rm" for a subcommand with aliases, its name otherwise.
func (c *subcommand) displayName() string {
	return strings.Join(append([]string{c.Name}, c.Aliases...), "|")
}

func printCommandList(w io.Writer, cmds []*subcommand, wrappers bool) {
	width := 0
	for _, c := range cmds {
		if c.Wrapper == wrappers && len(c.displayName()) > width {
			width = len(c.displayName())
		}
	}
	for _, c := range cmds {
		if c.Wrapper != wrappers {
			continue
		}
		fmt.Fprintf(w, "  %-*s : %s\n", width, c.displayName(), c.Summary)
	}
}

// printFlags renders short and long forms of each flag as one entry. The flag package's own
// PrintDefaults lists them separately, which is why every flag used to appear twice.
func printFlags(w io.Writer, flags []flagSpec) {
	width := 0
	for _, f := range flags {
		if n := len(f.names()) + len(f.Arg) + 1; n > width {
			width = n
		}
	}
	for _, f := range flags {
		names := f.names()
		if f.Arg != "" {
			names += " " + f.Arg
		}
		line := fmt.Sprintf("  %-*s  %s", width, names, f.Usage)
		if f.Default != "" {
			line += fmt.Sprintf(" (default: %s)", f.Default)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}
