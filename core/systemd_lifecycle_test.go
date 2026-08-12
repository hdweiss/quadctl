package core

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"github.com/fkmiec/quadctl/util"
)

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
