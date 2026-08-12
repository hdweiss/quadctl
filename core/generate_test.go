package core

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/util"
)

var update = flag.Bool("update", false, "rewrite the golden files with the current output")

// TestGenerateCommandsGolden pins the full podman command line for every quadlet type. It
// is the change-detector for the command generators: PLAN.md 3.1 rewrites the value model
// underneath them, and this is what makes any difference in the resulting argv visible.
//
// The golden file records today's output, defects included - the duplicated --name is
// TODO.md section 2, still open. Fixing those should show up here as a deliberate diff.
//
// Regenerate with: go test ./core/ -run TestGenerateCommandsGolden -update
func TestGenerateCommandsGolden(t *testing.T) {
	quadctl := &util.Quadctl{
		Runner:         &util.RecordingRunner{},
		QuadletSchemas: util.GetQuadletSchemas(),
		SearchDir:      "testdata/stack",
	}

	quadlets, err := util.InitQuadlets(quadctl)
	if err != nil {
		t.Fatalf("InitQuadlets: %v", err)
	}

	var b strings.Builder
	for _, q := range quadlets {
		create, warnings := generateCreateCommand(quadctl, q)
		startup, _ := generateStartupCommand(quadctl, q)
		stop := generateStopCommand(quadctl, q)

		b.WriteString("## " + q.ID + q.Type + "\n")
		writeLine(&b, "create ", create)
		writeLine(&b, "start  ", startup)
		writeLine(&b, "stop   ", stop)
		for _, w := range warnings {
			b.WriteString("warn   " + strings.TrimSpace(w) + "\n")
		}
		b.WriteString("\n")
	}

	golden := filepath.Join("testdata", "commands.golden")
	got := b.String()

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("generated commands differ from %s.\n--- want ---\n%s\n--- got ---\n%s", golden, want, got)
	}
}

func writeLine(b *strings.Builder, label string, argv []string) {
	if len(argv) == 0 {
		b.WriteString(label + "-\n")
		return
	}
	b.WriteString(label + strings.Join(argv, " ") + "\n")
}
