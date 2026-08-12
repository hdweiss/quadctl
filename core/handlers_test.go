package core

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"github.com/fkmiec/quadctl/util"
)

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

// TestSystemdTemplateDataAlwaysSetsUser guards the text/template swap (Phase 0.7): a missing
// map key renders as the literal "<no value>", so "user" has to be present even when empty.
func TestSystemdTemplateDataAlwaysSetsUser(t *testing.T) {
	tests := []struct {
		rootful bool
		want    string
	}{
		{true, "systemctl  stop"},
		{false, "systemctl --user stop"},
	}

	tmpl := template.Must(template.New("stop").Parse("systemctl {{.user}} stop"))
	for _, tt := range tests {
		data := systemdTemplateData(&util.Quadctl{IsRootful: tt.rootful})
		if _, ok := data["user"]; !ok {
			t.Fatalf("rootful=%v: template data has no \"user\" key", tt.rootful)
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			t.Fatal(err)
		}
		if got := buf.String(); got != tt.want {
			t.Errorf("rootful=%v: rendered %q, want %q", tt.rootful, got, tt.want)
		}
		if strings.Contains(buf.String(), "<no value>") {
			t.Errorf("rootful=%v: rendered the literal <no value>", tt.rootful)
		}
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
