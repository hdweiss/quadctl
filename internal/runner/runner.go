package runner

import (
	"os"
	"os/exec"
	"strings"
)

// OutputMode selects what happens to a command's output.
type OutputMode int

const (
	// CaptureStdout collects stdout and returns it; stderr is left on the error. Use for
	// commands whose output is parsed, so a warning on stderr can't corrupt the parse.
	CaptureStdout OutputMode = iota
	// CaptureCombined collects stdout and stderr together. Use for commands whose output is
	// shown to the user after the fact, where the error text matters as much as the result.
	CaptureCombined
	// Stream writes stdout and stderr straight through to quadctl's own, for commands whose
	// output the user watches as it happens.
	Stream
	// Interactive is Stream plus stdin, for a container that takes over the terminal.
	Interactive
)

// Options carries everything about an invocation other than its arguments.
type Options struct {
	Mode OutputMode
	Env  []string // extra variables, appended to the inherited environment
}

// Runner executes external commands (podman, systemctl, journalctl, podman's quadlet
// generator). It is the single seam between quadctl and the host: everything that shells
// out goes through the Runner held on the run state, so a test can drive a handler and
// assert on the exact argv it produces without any of those binaries being installed.
type Runner interface {
	// Run executes args, returning whatever output the mode captures. An empty args slice
	// is a no-op.
	Run(args []string, opts Options) (string, error)
}

// ExecRunner is the real Runner: it shells out.
type ExecRunner struct{}

func (ExecRunner) Run(args []string, opts Options) (string, error) {
	if len(args) == 0 {
		return "", nil
	}

	cmd := exec.Command(args[0], args[1:]...)
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}

	switch opts.Mode {
	case Stream, Interactive:
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if opts.Mode == Interactive {
			cmd.Stdin = os.Stdin
		}
		return "", cmd.Run()
	case CaptureCombined:
		out, err := cmd.CombinedOutput()
		return string(out), err
	default:
		out, err := cmd.Output()
		return string(out), err
	}
}

// The three helpers below are the non-Command shell-outs: side operations a caller needs
// while it is still building its command list, rather than steps in that list.

// RunStreaming runs args with its output going straight to the terminal as it happens.
func RunStreaming(r Runner, args []string) error {
	_, err := r.Run(args, Options{Mode: Stream})
	return err
}

// RunSilent runs args and throws the output away; only success or failure matters.
func RunSilent(r Runner, args []string) error {
	_, err := r.Run(args, Options{Mode: CaptureStdout})
	return err
}

// RunCaptured runs args and returns its stdout for parsing.
func RunCaptured(r Runner, args []string) (string, error) {
	return r.Run(args, Options{Mode: CaptureStdout})
}

// Invocation is one command a Runner was asked to run.
type Invocation struct {
	Args []string
	Opts Options
}

// RunResult is the canned answer a RecordingRunner gives for a command.
type RunResult struct {
	Output string
	Err    error
}

// RecordingRunner is a Runner for tests: it runs nothing, records every invocation, and
// answers from a table keyed by the full command line ("podman ps -a --format ...").
// Commands with no entry get Fallback, whose zero value is empty output and no error.
type RecordingRunner struct {
	Responses   map[string]RunResult
	Fallback    RunResult
	Invocations []Invocation
}

func (r *RecordingRunner) Run(args []string, opts Options) (string, error) {
	r.Invocations = append(r.Invocations, Invocation{
		Args: append([]string(nil), args...),
		Opts: opts,
	})
	if res, ok := r.Responses[strings.Join(args, " ")]; ok {
		return res.Output, res.Err
	}
	return r.Fallback.Output, r.Fallback.Err
}

// Commands returns each recorded invocation as a single command line, in the order they
// were run - the form assertions are usually written against.
func (r *RecordingRunner) Commands() []string {
	cmds := make([]string, 0, len(r.Invocations))
	for _, inv := range r.Invocations {
		cmds = append(cmds, strings.Join(inv.Args, " "))
	}
	return cmds
}
