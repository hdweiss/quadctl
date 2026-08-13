// Package command turns parsed quadlets into podman commands and runs them.
//
// A Command is one labelled step - the argv to run, or a Go function where the step is a file
// operation - and RunCommands is the single place they are printed, run, or reported on. Every
// handler builds a slice of them and hands it back rather than executing anything itself,
// which is what makes -p (print mode) a property of the runner rather than a flag each
// handler has to remember.
//
// The handlers here are the podman-direct half of quadctl: they compute dependency order
// themselves and shell out to podman. The systemd half lives in internal/systemd, which
// installs quadlet files and lets systemd do the ordering; it builds the same Command values.
package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/briandowns/spinner"
	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
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
	// Runner executes Cmd. RunCommands fills it in from the run state before running, so
	// handlers that build a Command don't have to carry one around.
	Runner runner.Runner
	// ShowSpinner is set by RunCommands from the run state: a spinner redraws its own line,
	// which only means anything on a terminal.
	ShowSpinner bool
	// Stream marks a command whose output the user watches as it happens - podman logs,
	// podman stats, journalctl. It writes straight through to quadctl's own stdout and stderr
	// instead of being captured, and no spinner runs over it.
	//
	// It exists so that these commands can go through RunCommands like everything else. They
	// used to shell out from inside their handler, which is why they alone had no label, no
	// error reporting and no effect on the exit code (TODO.md section 3).
	Stream bool
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
	if c.Stream {
		// The command is about to write to this terminal itself, so say what it is first and
		// then get out of its way.
		fmt.Printf("%s...\n", c.Label)
		return
	}
	if !c.ShowSpinner {
		return
	}
	c.Spinner = spinner.New(spinner.CharSets[14], 100*time.Millisecond) // Build our new spinner
	c.Spinner.Prefix = c.Label + " "
	c.Spinner.Start() // Start the spinner
	setActiveSpinner(c.Spinner)
}

func DefaultRunFn(c *Command) {
	if c.Stream {
		_, c.Error = c.Runner.Run(c.Cmd, runner.Options{Mode: runner.Stream})
		return
	}
	if len(c.Cmd) > 0 {
		// c.Cmd is argv already: ParseFields resolved quoting where the value was written,
		// so each entry is exactly the bytes the program should see. Nothing is stripped
		// here - a quote that survived this far is one the user meant literally.
		if isForegroundRun(c.Cmd) {
			fmt.Printf("Running in foreground: %s\n", strings.Join(c.Cmd, " "))
			_, c.Error = c.Runner.Run(c.Cmd, runner.Options{Mode: runner.Interactive})
		} else {
			output, err := c.Runner.Run(c.Cmd, runner.Options{Mode: runner.CaptureCombined})
			c.Output = []string{output}
			c.Error = err
		}
	}
}

func DefaultPostFn(c *Command) {
	if isForegroundRun(c.Cmd) {
		return // Skip stopping the spinner for 'run' command since it is interactive and the spinner output can interfere with the container's output.
	}
	if c.Stream {
		// Its own output is the outcome, and a failure is reported by RunCommands.
		return
	}
	outcome := "Done"
	if c.Error != nil {
		outcome = "Failed"
	}
	// Without a spinner - piped output, or print mode - the line the spinner would have
	// replaced still has to be written, or the run says nothing at all about what it did.
	if c.Spinner == nil {
		fmt.Printf("%s... %s\n", c.Label, outcome)
		return
	}
	c.Spinner.FinalMSG = fmt.Sprintf("%s... %s\n", c.Label, outcome)
	c.Spinner.Stop()
	setActiveSpinner(nil)
}

// The spinner runs on its own goroutine and owns the terminal line it is drawing on, so a
// signal arriving mid-command has to stop it before anything else prints - otherwise the
// process exits with a half-drawn animation as the last thing on screen. Only one command
// runs at a time, so one pointer is enough.
var (
	activeSpinnerMu sync.Mutex
	activeSpinner   *spinner.Spinner
)

func setActiveSpinner(s *spinner.Spinner) {
	activeSpinnerMu.Lock()
	defer activeSpinnerMu.Unlock()
	activeSpinner = s
}

// StopSpinner tears down whatever spinner is currently running, if any. Safe to call from a
// signal handler, and safe to call when nothing is spinning.
func StopSpinner() {
	activeSpinnerMu.Lock()
	defer activeSpinnerMu.Unlock()
	if activeSpinner != nil {
		activeSpinner.FinalMSG = ""
		activeSpinner.Stop()
		activeSpinner = nil
	}
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

// printWarnings reports what the generators had to say about the commands they built, before
// any of them runs.
//
// Two things used to be wrong here. Warnings were shown only with -v, so a quadlet option
// quadctl could not use vanished without a trace - the exact failure mode that made Phase 0
// necessary. And several messages carried their own newlines and leading spaces, so the block
// came out with ragged indentation and stray blank lines.
//
// Warnings still print up front rather than beside the command they belong to: a spinner
// redraws its own line, so anything interleaved with one is liable to be overwritten, and a
// problem worth knowing about is worth knowing before the first command runs. Each line names
// the command it came from instead, which is what the ordering was standing in for.
func printWarnings(quadctl *quadlet.State, commands []Command) {
	printed := false
	for _, c := range commands {
		for _, w := range c.Warnings {
			// Contains, not HasPrefix: handlers prepend the source file name to the warnings
			// the generators hand back.
			if !quadctl.IsVerbose && strings.Contains(w, InfoPrefix) {
				continue
			}
			// Messages arrive with assorted leading spaces and trailing newlines of their own.
			w = strings.TrimSpace(strings.Join(strings.Fields(w), " "))
			if w == "" {
				continue
			}
			if !printed {
				fmt.Fprintf(os.Stderr, "\n# --- WARNINGS ---\n\n")
				printed = true
			}
			fmt.Fprintf(os.Stderr, "  %s: %s\n", c.Label, w)
		}
	}
	if printed {
		fmt.Fprintln(os.Stderr)
	}
}

// Common handling for dry run / verbose output and command execution for all handlers that
// generate commands. Returns the exit code quadctl should terminate with: 0 when every
// command succeeded, otherwise the status of the last command that failed.
func RunCommands(quadctl *quadlet.State, commands []Command) int {
	exitCode := 0

	printWarnings(quadctl, commands)
	if quadctl.IsPrintOnly && len(commands) > 0 {
		fmt.Printf("\n# --- Print MODE: Commands that would be executed ---\n\n")
		for _, c := range commands {
			if len(c.Cmd) > 0 {
				fmt.Println(ShellQuote(c.Cmd))
			} else {
				fmt.Printf("%s\n", c.Label)
				for _, line := range c.Output {
					fmt.Println("  => " + line)
				}
			}
		}
	} else if len(commands) > 0 {
		spin := useSpinner(quadctl)
		for i, c := range commands {
			c.Runner = quadctl.Runner
			c.ShowSpinner = spin
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
func diagnoseFailedSystemctlCommand(quadctl *quadlet.State, cmd []string) string {
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
	if invID := currentInvocationID(quadctl.Runner, userArgs, unit); invID != "" {
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
	if out, _ := quadctl.Runner.Run(statusArgs, runner.Options{Mode: runner.CaptureCombined}); len(out) > 0 {
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", strings.Join(statusArgs, " "), strings.TrimRight(out, "\n"))
	}
	if out, _ := quadctl.Runner.Run(journalArgs, runner.Options{Mode: runner.CaptureCombined}); len(out) > 0 {
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", strings.Join(journalArgs, " "), strings.TrimRight(out, "\n"))
	}
	return b.String()
}

// currentInvocationID returns the unit's current InvocationID (the ID systemd/journald tag every
// log line from a single start-to-stop run with), or "" if the unit has none on record - e.g. it
// was never found/loaded, or genuinely never ran.
func currentInvocationID(r runner.Runner, userArgs []string, unit string) string {
	args := append([]string{"systemctl"}, userArgs...)
	args = append(args, "show", unit, "--property=InvocationID", "--value")
	out, err := r.Run(args, runner.Options{Mode: runner.CaptureStdout})
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(out)
	if id == "" || strings.Trim(id, "0") == "" {
		return ""
	}
	return id
}

// ShellQuote renders argv as a line that can be pasted into a shell and run. Arguments are
// separate values internally, so anything containing whitespace or shell punctuation is
// quoted here rather than being joined into an ambiguous string: print mode showing
// `--env GREETING=hello world` for what is really two arguments is a command that does
// something different when copied.
func ShellQuote(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\$`&|;<>()*?[]#~") {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}
