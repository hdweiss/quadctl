package core

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/internal/util"
)

// newTestQuadctl returns a run state wired to a RecordingRunner, so nothing in a test
// reaches podman, systemctl or the host at all.
func newTestQuadctl(sub string) (*util.State, *util.RecordingRunner) {
	runner := &util.RecordingRunner{}
	return &util.State{
		Subcommand: sub,
		Runner:     runner,
		IsRootful:  true,
		Config:     util.DefaultConfig(),
	}, runner
}

func container(id, name string) *util.Quadlet {
	return &util.Quadlet{
		ID:             id,
		Type:           ".container",
		Filepath:       id + ".container",
		Sections:       map[string]map[string][]string{"Container": {"Image": {"docker.io/library/alpine:latest"}}},
		GeneratedNames: map[string]string{"container": name},
	}
}

// TestHandleStopArgv is the point of the runner seam: a handler's exact argv, asserted
// without podman installed.
func TestHandleStopArgv(t *testing.T) {
	quadctl, runner := newTestQuadctl("stop")
	quadlets := []*util.Quadlet{container("web", "web"), container("db", "db")}

	cmds, err := HandleStop(quadctl, quadlets)
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	if code := RunCommands(quadctl, cmds); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	// Stops run in reverse dependency order.
	want := []string{"podman stop db", "podman stop web"}
	got := runner.Commands()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestHandleRemoveArgv(t *testing.T) {
	quadctl, runner := newTestQuadctl("remove")
	quadlets := []*util.Quadlet{container("web", "web")}

	cmds, err := HandleRemove(quadctl, quadlets)
	if err != nil {
		t.Fatalf("HandleRemove: %v", err)
	}
	RunCommands(quadctl, cmds)

	want := []string{"podman container rm -f web"}
	if got := runner.Commands(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("commands:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestRunCommandsPrintOnlyRunsNothing guards the promise -p makes.
func TestRunCommandsPrintOnlyRunsNothing(t *testing.T) {
	quadctl, runner := newTestQuadctl("stop")
	quadctl.IsPrintOnly = true

	cmds, err := HandleStop(quadctl, []*util.Quadlet{container("web", "web")})
	if err != nil {
		t.Fatalf("HandleStop: %v", err)
	}
	RunCommands(quadctl, cmds)

	if len(runner.Invocations) != 0 {
		t.Errorf("print mode ran %d command(s): %q", len(runner.Invocations), runner.Commands())
	}
}

// TestRunCommandsExitCodes covers the Phase 0 exit-code policy: teardown subcommands run
// every command and report the last failure, build-up subcommands stop at the first.
func TestRunCommandsExitCodes(t *testing.T) {
	failure := util.RunResult{Err: fakeExitError(t, 125)}

	tests := []struct {
		name     string
		sub      string
		wantRun  int
		wantCode int
	}{
		{"teardown runs everything", "stop", 2, 125},
		{"buildup aborts", "create", 1, 125},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quadctl, runner := newTestQuadctl(tt.sub)
			runner.Responses = map[string]util.RunResult{"podman stop web": failure}

			cmds := []Command{}
			for _, argv := range [][]string{{"podman", "stop", "web"}, {"podman", "stop", "db"}} {
				c := NewCommand("stopping")
				c.Cmd = argv
				c.PreFn = func(*Command) {}
				c.PostFn = func(*Command) {}
				cmds = append(cmds, c)
			}

			if code := RunCommands(quadctl, cmds); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if len(runner.Invocations) != tt.wantRun {
				t.Errorf("ran %d command(s), want %d: %q", len(runner.Invocations), tt.wantRun, runner.Commands())
			}
		})
	}
}

// TestForegroundRunAttachesStdio pins the Phase 0 detached-run fix: a run carrying -d must
// not be given the terminal.
func TestForegroundRunAttachesStdio(t *testing.T) {
	tests := []struct {
		argv []string
		want util.OutputMode
	}{
		{[]string{"podman", "run", "--name", "web", "alpine"}, util.Interactive},
		{[]string{"podman", "run", "--name", "web", "-d", "alpine"}, util.CaptureCombined},
		{[]string{"podman", "run", "--name", "web", "--detach", "alpine"}, util.CaptureCombined},
	}

	for _, tt := range tests {
		quadctl, runner := newTestQuadctl("run")
		c := NewCommand("running")
		c.Cmd = tt.argv
		c.PreFn = func(*Command) {}
		c.PostFn = func(*Command) {}

		RunCommands(quadctl, []Command{c})

		if len(runner.Invocations) != 1 {
			t.Fatalf("%v: got %d invocations", tt.argv, len(runner.Invocations))
		}
		if got := runner.Invocations[0].Opts.Mode; got != tt.want {
			t.Errorf("%v: output mode = %v, want %v", tt.argv, got, tt.want)
		}
	}
}

// fakeExitError produces a real *exec.ExitError carrying the given status, so exitCodeFor
// is exercised the way it is in production rather than against a stand-in error type.
func fakeExitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", "exit "+strconv.Itoa(code)).Run()
	if err == nil {
		t.Fatalf("expected /bin/sh to exit %d", code)
	}
	return err
}
