package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fkmiec/quadctl/util"
)

type Command struct {
	Label    string
	PreFn    func(*Command)
	RunFn    func(*Command)
	PostFn   func(*Command)
	Spinner  *spinner.Spinner
	Cmd      []string
	Output   []string
	Error    error
	Warnings []string
}

func (c *Command) PreCmd() {
	c.PreFn(c)
}

func (c *Command) RunCmd() {
	c.RunFn(c)
}

func (c *Command) PostCmd() {
	c.PostFn(c)
}

func NewCommand(label string) Command {
	return Command{
		Label:  label,
		PreFn:  DefaultPreFn,
		RunFn:  DefaultRunFn,
		PostFn: DefaultPostFn,
	}
}

// isForegroundRun reports whether cmd is a 'podman run' that will attach to the terminal,
// ie. one that carries neither -d nor --detach. Such a command owns stdio: the spinner
// would interfere with the container's output, and the container's own streams have to be
// wired to ours.
func isForegroundRun(cmd []string) bool {
	return slices.Contains(cmd, "run") &&
		!slices.Contains(cmd, "-d") &&
		!slices.Contains(cmd, "--detach")
}

func DefaultPreFn(c *Command) {
	if isForegroundRun(c.Cmd) {
		return // Skip spinner for 'run' command since it is interactive and the spinner output can interfere with the container's output.
	}
	c.Spinner = spinner.New(spinner.CharSets[14], 100*time.Millisecond) // Build our new spinner
	c.Spinner.Prefix = c.Label + " "
	c.Spinner.Start() // Start the spinner
}

func DefaultRunFn(c *Command) {
	if len(c.Cmd) > 0 {
		//  If user quoted some value becuase it contained spaces, parser.ParseFields() retains quotes regardless of how often called.
		//  They will interfere with execution, so we remove them. The exec.Command call will quote any arg that contains spaces.
		//  In practice this means KEY="some val with spaces" will be stripped and requoted by exec.Command as "KEY=some val with spaces",
		//  which works fine.
		for i, arg := range c.Cmd {
			c.Cmd[i] = strings.Trim(arg, `"`)
		}

		//fmt.Printf("Array of strings in my command:\n%q\n", c.Cmd)
		cmd := exec.Command(c.Cmd[0], c.Cmd[1:]...)

		if isForegroundRun(c.Cmd) {
			fmt.Printf("Running in foreground: %s\n", strings.Join(c.Cmd, " "))
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			c.Error = cmd.Run()
		} else {
			output, err := cmd.CombinedOutput()
			c.Output = []string{string(output)}
			c.Error = err

		}
	}
}

func DefaultPostFn(c *Command) {
	if isForegroundRun(c.Cmd) {
		return // Skip stopping the spinner for 'run' command since it is interactive and the spinner output can interfere with the container's output.
	}
	if c.Error != nil {
		c.Spinner.FinalMSG = fmt.Sprintf("%s... Failed\n", c.Label)
	} else {
		c.Spinner.FinalMSG = fmt.Sprintf("%s... Done\n", c.Label)
	}
	c.Spinner.Stop()
}

// abortingSubcommands are the subcommands where a failed command makes the ones after it
// meaningless: they build resources up in dependency order, so once a step fails the rest
// would either fail in turn or act on a half-created group. Teardown and query subcommands
// (stop, remove, status, logs, ...) run to completion instead and report at the end, since
// each of their commands stands on its own.
var abortingSubcommands = []string{"pull", "create", "start", "run"}

// exitCodeFor maps a failed command's error to the exit code quadctl should return -
// the underlying process's own status where there is one (podman's 125, systemctl's 5,
// ...), otherwise a generic failure.
func exitCodeFor(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}

// Common handling for dry run / verbose output and command execution for all handlers that
// generate commands. Returns the exit code quadctl should terminate with: 0 when every
// command succeeded, otherwise the status of the last command that failed.
func RunCommands(quadctl *util.Quadctl, commands []Command) int {

	exitCode := 0

	if quadctl.IsVerbose {
		isHeaderPrinted := false
		for _, c := range commands {
			if len(c.Warnings) > 0 {
				if !isHeaderPrinted {
					fmt.Printf("\n# --- WARNINGS ---\n\n")
					isHeaderPrinted = true
				}
				for _, w := range c.Warnings {
					fmt.Printf(" => %s\n", w)
				}
			}
		}
	}
	if quadctl.IsPrintOnly && len(commands) > 0 {
		fmt.Printf("\n# --- Print MODE: Commands that would be executed ---\n\n")
		for _, c := range commands {
			if len(c.Cmd) > 0 {
				fmt.Println(strings.Join(c.Cmd, " "))
			} else {
				fmt.Printf("%s\n", c.Label)
				for _, line := range c.Output {
					fmt.Println("  => " + line)
				}
			}
		}
	} else if len(commands) > 0 {
		for i, c := range commands {
			c.PreCmd()
			c.RunCmd()
			c.PostCmd()

			diag := ""
			if c.Error != nil {
				diag = diagnoseFailedSystemctlCommand(quadctl, c.Cmd)
			}

			if c.Output != nil {
				for _, o := range c.Output {
					if diag != "" {
						// We're about to show real systemctl status/journalctl output below,
						// so systemd's generic "go run these yourself" hint just adds noise.
						o = stripSystemdJobFailureHint(o)
					}
					if o == "" {
						continue
					}
					fmt.Fprintf(os.Stdout, "%s\n", o)
				}
			}
			if c.Error != nil {
				if len(c.Cmd) > 0 {
					fmt.Fprintf(os.Stderr, "Error executing command:\n\n  %s\n\n%s\n", strings.Join(c.Cmd, " "), c.Error.Error())
				} else {
					fmt.Fprintf(os.Stderr, "Error: %s failed: %s\n", c.Label, c.Error.Error())
				}
				if diag != "" {
					fmt.Fprint(os.Stderr, diag)
				}

				exitCode = exitCodeFor(c.Error)
				if slices.Contains(abortingSubcommands, quadctl.Subcommand) {
					if remaining := len(commands) - i - 1; remaining > 0 {
						fmt.Fprintf(os.Stderr, "Aborting %s: %d remaining command(s) not run.\n", quadctl.Subcommand, remaining)
					}
					return exitCode
				}
			}
		}
	}

	return exitCode
}

// isSystemctlLifecycleCommand reports whether cmd is a 'systemctl start/stop/restart/try-restart'
// invocation targeting a single unit (the shape HandleSystemdStart/Stop build), as opposed to e.g.
// 'systemctl daemon-reload' or a plain 'systemctl status' query.
func isSystemctlLifecycleCommand(cmd []string) bool {
	if len(cmd) < 2 || cmd[0] != "systemctl" {
		return false
	}
	for _, v := range []string{"start", "stop", "restart", "try-restart"} {
		if slices.Contains(cmd, v) {
			return true
		}
	}
	return false
}

// stripSystemdJobFailureHint removes systemctl's generic "Job for X failed ... See systemctl
// status / journalctl for details" hint from a command's captured output. It's boilerplate that
// always says the same thing regardless of the actual failure, and is redundant once we're
// showing the real systemctl status/journalctl output ourselves right below it.
func stripSystemdJobFailureHint(output string) string {
	lines := strings.Split(output, "\n")
	kept := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Job for ") && strings.Contains(trimmed, "failed") {
			continue
		}
		if strings.HasPrefix(trimmed, `See "systemctl status`) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// diagnoseFailedSystemctlCommand builds troubleshooting context for a failed 'systemctl start/stop/restart'
// invocation by fetching the unit's status and its most recent journal entries directly, rather than
// leaving the user to run "systemctl status" / "journalctl" themselves as systemctl's own error suggests.
func diagnoseFailedSystemctlCommand(quadctl *util.Quadctl, cmd []string) string {
	if !isSystemctlLifecycleCommand(cmd) {
		return ""
	}
	unit := cmd[len(cmd)-1]

	userArgs := []string{}
	if !quadctl.IsRootful {
		userArgs = append(userArgs, "--user")
	}

	statusArgs := append([]string{"systemctl"}, userArgs...)
	// --lines=0 suppresses status's own trailing log excerpt, which would otherwise duplicate
	// the journalctl output below - status is kept for the state/exit-code/ExecStart= summary.
	statusArgs = append(statusArgs, "status", unit, "--no-pager", "-l", "--lines=0")

	journalArgs := append([]string{"journalctl"}, userArgs...)
	if invID := currentInvocationID(userArgs, unit); invID != "" {
		// Scope to exactly this run (systemd's own lifecycle messages carry INVOCATION_ID,
		// the unit's own process output carries the trusted _SYSTEMD_INVOCATION_ID), so we
		// don't dredge up unrelated entries from previous runs of the same unit.
		journalArgs = append(journalArgs, "_SYSTEMD_INVOCATION_ID="+invID, "+", "INVOCATION_ID="+invID, "--no-pager")
	} else {
		// No invocation on record (e.g. the unit was never found/loaded) - fall back to a
		// plain recent-lines view of the unit's log.
		journalArgs = append(journalArgs, "-u", unit, "--no-pager", "-n", "20")
	}

	var b strings.Builder
	if out, _ := exec.Command(statusArgs[0], statusArgs[1:]...).CombinedOutput(); len(out) > 0 {
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", strings.Join(statusArgs, " "), strings.TrimRight(string(out), "\n"))
	}
	if out, _ := exec.Command(journalArgs[0], journalArgs[1:]...).CombinedOutput(); len(out) > 0 {
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", strings.Join(journalArgs, " "), strings.TrimRight(string(out), "\n"))
	}
	return b.String()
}

// currentInvocationID returns the unit's current InvocationID (the ID systemd/journald tag every
// log line from a single start-to-stop run with), or "" if the unit has none on record - e.g. it
// was never found/loaded, or genuinely never ran.
func currentInvocationID(userArgs []string, unit string) string {
	args := append([]string{"systemctl"}, userArgs...)
	args = append(args, "show", unit, "--property=InvocationID", "--value")
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(out))
	if id == "" || strings.Trim(id, "0") == "" {
		return ""
	}
	return id
}

func runCommand(args []string) error {
	if len(args) == 0 {
		return nil
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()

	return err
}

func runCommandSilently(args []string) error {
	//if isRootful && args[0] != "sudo" {
	//	args = append([]string{"sudo"}, args...)
	//}
	cmd := exec.Command(args[0], args[1:]...)
	// Discard output
	err := cmd.Run()
	return err
}

func runCommandCapture(args []string) (string, error) {
	//if isRootful && args[0] != "sudo" {
	//	args = append([]string{"sudo"}, args...)
	//}

	//fmt.Printf("=> Running command: %s\n", strings.Join(args, " "))

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.Output()
	return string(output), err
}
