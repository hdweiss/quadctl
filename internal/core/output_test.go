package core

import (
	"os"
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/internal/quadlet"
)

// TestLabelDropsTheExtension covers TODO.md section 4: output read "Systemd stopping
// .container app" next to "Starting .container app", printing the raw file extension as
// though it were a word.
func TestLabelDropsTheExtension(t *testing.T) {
	q := &quadlet.Quadlet{ID: "app", Type: ".container", ResourceName: "web-app", ServiceName: "app"}

	if got, want := quadletLabel("Starting", q), "Starting container web-app"; got != want {
		t.Errorf("quadletLabel = %q, want %q", got, want)
	}
	// The systemd variant names the unit systemctl is about to act on, not the podman
	// resource, but reads the same way.
	if got, want := unitLabel("Stopping", q), "Stopping container app"; got != want {
		t.Errorf("unitLabel = %q, want %q", got, want)
	}
	// A .kube has no resource name of its own; the file's base name stands in.
	kube := &quadlet.Quadlet{ID: "site", Type: ".kube"}
	if got, want := quadletLabel("Creating", kube), "Creating kube site"; got != want {
		t.Errorf("quadletLabel(kube) = %q, want %q", got, want)
	}
}

// TestUseColorRespectsNoColor covers the environment side of the colour decision. stdout is
// not a terminal under `go test`, so the TTY check already says no; these assert that the
// explicit opt-outs say no independently of it.
func TestUseColorRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	if UseColor(&quadlet.State{IsNoColor: true}) {
		t.Error("--no-color should disable colour")
	}
	t.Setenv("NO_COLOR", "1")
	if UseColor(&quadlet.State{}) {
		t.Error("NO_COLOR should disable colour")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if UseColor(&quadlet.State{}) {
		t.Error("TERM=dumb should disable colour")
	}
	// And in any case, output that isn't going to a terminal is never coloured.
	t.Setenv("TERM", "xterm-256color")
	if UseColor(&quadlet.State{}) != isTerminal(os.Stdout) {
		t.Error("colour should follow the TTY check once nothing else has vetoed it")
	}
}

// TestPrintWarningsVisibility covers TODO.md section 4: warnings only appeared with -v, so an
// option quadctl could not use vanished without a trace. Commentary marked [INFO] is the part
// that stays behind -v.
func TestPrintWarningsVisibility(t *testing.T) {
	commands := []Command{{
		Label: "Creating container web",
		Warnings: []string{
			"no Image= specified in [Container]",
			InfoPrefix + "restart policy configured (always)",
			"  ragged\n   message  \n",
			"",
		},
	}}

	tests := []struct {
		name    string
		verbose bool
		want    []string
		notWant []string
	}{
		{
			name:    "default verbosity",
			want:    []string{"no Image= specified", "Creating container web"},
			notWant: []string{"restart policy configured"},
		},
		{
			name:    "verbose",
			verbose: true,
			want:    []string{"no Image= specified", "restart policy configured"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStderr(t, func() {
				printWarnings(&quadlet.State{IsVerbose: tt.verbose}, commands)
			})
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("output should mention %q, got:\n%s", want, out)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(out, notWant) {
					t.Errorf("output should not mention %q, got:\n%s", notWant, out)
				}
			}
			// Messages carrying their own newlines and leading spaces used to produce ragged
			// indentation; each one is now a single line.
			if strings.Contains(out, "ragged\n") || strings.Contains(out, "   message") {
				t.Errorf("message was not flattened onto one line:\n%s", out)
			}
			if !strings.Contains(out, "ragged message") {
				t.Errorf("flattened message missing:\n%s", out)
			}
		})
	}
}

// captureStderr collects everything written to os.Stderr while fn runs.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	fn()
	os.Stderr = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}
