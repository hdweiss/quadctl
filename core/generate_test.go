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

// TestKubeDownForce covers a Phase 0.3 panic: KubeDownForce is optional, so both the
// section and the value slice may be absent, and the old code indexed [0] regardless.
func TestKubeDownForce(t *testing.T) {
	tests := []struct {
		name     string
		sections map[string]map[string][]string
		want     bool
	}{
		{"no sections at all", nil, false},
		{"no Kube section", map[string]map[string][]string{"Container": {}}, false},
		{"no KubeDownForce key", map[string]map[string][]string{"Kube": {"Yaml": {"app.yaml"}}}, false},
		{"empty value", map[string]map[string][]string{"Kube": {"KubeDownForce": {}}}, false},
		{"false", map[string]map[string][]string{"Kube": {"KubeDownForce": {"false"}}}, false},
		{"true", map[string]map[string][]string{"Kube": {"KubeDownForce": {"true"}}}, true},
		{"True", map[string]map[string][]string{"Kube": {"KubeDownForce": {"True"}}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &util.Quadlet{ID: "app", Type: ".kube", Sections: tt.sections}
			if got := kubeDownForce(q); got != tt.want {
				t.Errorf("kubeDownForce = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGenerateStopCommandKubeNoPanic drives the two call sites that used to index the nil
// slice, with the config that removed the || short-circuit hiding it.
func TestGenerateStopCommandKubeNoPanic(t *testing.T) {
	quadctl := &util.Quadctl{
		Runner:           &util.RecordingRunner{},
		IsRemoveVolumes:  false,
		IsRemoveNetworks: false,
	}
	q := &util.Quadlet{
		ID:             "app",
		Type:           ".kube",
		Sections:       map[string]map[string][]string{"Kube": {"Yaml": {"app.yaml"}}},
		KubernetesYaml: "/tmp/app.yaml",
		GeneratedNames: map[string]string{},
	}

	stop := generateStopCommand(quadctl, q)
	if got := strings.Join(stop, " "); got != "podman play kube --down /tmp/app.yaml" {
		t.Errorf("stop command = %q", got)
	}

	commands, err := HandleRemove(quadctl, []*util.Quadlet{q})
	if err != nil {
		t.Fatalf("HandleRemove: %v", err)
	}
	if len(commands) != 1 || strings.Join(commands[0].Cmd, " ") != "podman play kube --down /tmp/app.yaml" {
		t.Errorf("remove commands = %v", commandLines(commands))
	}
}

// TestGenerateRunCommandWithoutCreate covers the Phase 0.3 slice panic: generateRunCommand
// used to do createCmd[3:] on whatever generateCreateCommand handed back, including nothing.
func TestGenerateRunCommandWithoutCreate(t *testing.T) {
	quadctl := &util.Quadctl{
		Runner:         &util.RecordingRunner{},
		QuadletSchemas: util.GetQuadletSchemas(),
	}
	// A type generateCreateCommand doesn't handle, so it returns an empty slice.
	q := &util.Quadlet{ID: "base", Type: ".image", Sections: map[string]map[string][]string{}, GeneratedNames: map[string]string{}}

	cmd, warnings := generateRunCommand(quadctl, q)
	if len(cmd) != 0 {
		t.Errorf("run command = %v, want none", cmd)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning when no run command could be generated")
	}
}

// TestGenerateStartupCommandKubeIsClean covers Phase 0.6: the generator used to print a
// half-built command to stdout on every .kube start.
func TestGenerateStartupCommandKubeIsClean(t *testing.T) {
	quadctl := &util.Quadctl{
		Runner:         &util.RecordingRunner{},
		QuadletSchemas: util.GetQuadletSchemas(),
	}
	q := &util.Quadlet{
		ID:             "app",
		Type:           ".kube",
		Sections:       map[string]map[string][]string{"Kube": {"Yaml": {"app.yaml"}}},
		KubernetesYaml: "/tmp/app.yaml",
		GeneratedNames: map[string]string{},
	}

	cmd, _ := generateStartupCommand(quadctl, q)
	if got := strings.Join(cmd, " "); got != "podman play kube /tmp/app.yaml" {
		t.Errorf("startup command = %q", got)
	}
}
