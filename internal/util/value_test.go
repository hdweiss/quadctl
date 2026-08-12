package util

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/internal/schema"
)

// TestParseFields covers the tokenizer every written value goes through on its way to argv.
// Quotes are syntax here, not content: what comes out is what the program should see.
func TestParseFields(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   \t ", nil},
		{"plain words", "-d --rm", []string{"-d", "--rm"}},
		{"tabs separate too", "-d\t--rm", []string{"-d", "--rm"}},
		{"double quotes hold a space", `sh -c "echo hi"`, []string{"sh", "-c", "echo hi"}},
		{"single quotes hold a space", `sh -c 'echo hi'`, []string{"sh", "-c", "echo hi"}},
		{"quotes are dropped, not kept", `GREETING="hello world"`, []string{"GREETING=hello world"}},
		{"quote inside a word", `--label owner="the platform team"`, []string{"--label", "owner=the platform team"}},
		{"a quoted empty string is an argument", `--entrypoint ""`, []string{"--entrypoint", ""}},
		{"the other quote survives inside", `-c "it's fine"`, []string{"-c", "it's fine"}},
		{"collapses runs of spaces", "a   b", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseFields(tt.input); !slices.Equal(got, tt.want) {
				t.Errorf("ParseFields(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

// TestOptionValues covers the rule that replaced tokenizing at parse time: the schema decides
// whether a written line is one value or several.
func TestOptionValues(t *testing.T) {
	options := GetQuadletOptionsMap("container")

	tests := []struct {
		name string
		key  string
		raw  []string
		want []string
	}{
		{
			name: "a single-valued option keeps its spaces",
			key:  "Exec",
			raw:  []string{`/bin/sh -c "echo hi"`},
			want: []string{`/bin/sh -c "echo hi"`},
		},
		{
			name: "the last assignment to a single-valued option wins",
			key:  "Image",
			raw:  []string{"alpine:3.19", "alpine:3.20"},
			want: []string{"alpine:3.20"},
		},
		{
			name: "a list option splits one line on whitespace",
			key:  "AddCapability",
			raw:  []string{"NET_ADMIN NET_RAW"},
			want: []string{"NET_ADMIN", "NET_RAW"},
		},
		{
			name: "a list option accumulates across lines",
			key:  "Environment",
			raw:  []string{"FOO=bar", "TZ=UTC"},
			want: []string{"FOO=bar", "TZ=UTC"},
		},
		{
			name: "a quoted space survives splitting a list option",
			key:  "Environment",
			raw:  []string{`GREETING="hello world"`},
			want: []string{"GREETING=hello world"},
		},
		{
			name: "an option the schema doesn't know is left alone",
			key:  "NoSuchOption",
			raw:  []string{"a b c"},
			want: []string{"a b c"},
		},
		{"nothing set", "Image", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OptionValues(options, tt.key, tt.raw); !slices.Equal(got, tt.want) {
				t.Errorf("OptionValues(%s, %#v) = %#v, want %#v", tt.key, tt.raw, got, tt.want)
			}
		})
	}
}

// TestQuadletOptionToPodmanKeepsValuesWhole is the argv-boundary test: the rendered template
// is cut into arguments before the value is put back, so a value containing a space is one
// argument no matter which shape of template carries it.
func TestQuadletOptionToPodmanKeepsValuesWhole(t *testing.T) {
	options := GetQuadletOptionsMap("container")

	tests := []struct {
		key   string
		value string
		want  []string
	}{
		// "{{.Key}} {{.Value}}" - flag then value.
		{"Environment", "GREETING=hello world", []string{"--env", "GREETING=hello world"}},
		{"HealthCmd", "curl -f http://localhost/health", []string{"--health-cmd", "curl -f http://localhost/health"}},
		// "--security-opt apparmor={{.Value}}" - the value is embedded in a token.
		{"AppArmor", "my profile", []string{"--security-opt", "apparmor=my profile"}},
		// "{{.Value}}" alone.
		{"Image", "docker.io/library/alpine:3.20", []string{"docker.io/library/alpine:3.20"}},
		// A template that never mentions the value still renders its flag.
		{"ReadOnly", "true", []string{"--read-only"}},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, err := QuadletOptionToPodman("container", options, tt.key, tt.value)
			if err != nil {
				t.Fatalf("QuadletOptionToPodman: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}

	if _, err := QuadletOptionToPodman("container", options, "NoSuchOption", "x"); err == nil {
		t.Error("expected an error for an option the schema doesn't define")
	}
}

// TestParseIniFileValueSemantics covers what the INI layer does and, as importantly, what it
// no longer does: it records assignments verbatim and leaves every question of "one value or
// several" to the schema.
func TestParseIniFileValueSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.container")
	write(t, path, `[Container]
Image=alpine
Exec=/bin/sh -c "echo hello world"
Environment=FOO=bar
Environment=TZ=UTC
Label=owner=platform \
      tier=demo
AddCapability=NET_ADMIN
AddCapability=
`)

	q, err := ParseQuadletFile(path)
	if err != nil {
		t.Fatalf("ParseQuadletFile: %v", err)
	}
	cont := q.Sections["Container"]

	if got := cont["Exec"]; len(got) != 1 || got[0] != `/bin/sh -c "echo hello world"` {
		t.Errorf("Exec recorded as %#v, want the line unchanged", got)
	}
	if got := cont["Environment"]; !slices.Equal(got, []string{"FOO=bar", "TZ=UTC"}) {
		t.Errorf("repeated key recorded as %#v", got)
	}
	if got := cont["Label"]; len(got) != 1 || got[0] != "owner=platform tier=demo" {
		t.Errorf("continuation recorded as %#v, want one joined line", got)
	}
	if got, ok := cont["AddCapability"]; ok {
		t.Errorf("an empty assignment should reset the key, got %#v", got)
	}
}

// TestParseQuadletDropInErrorsAreReported covers the other half of PLAN.md 3.1: a drop-in
// that can't be read changes what the quadlet means, and used to be swallowed.
func TestParseQuadletDropInErrorsAreReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.container")
	write(t, path, "[Container]\nImage=alpine\n")

	// A directory where a .conf is expected: openable, unreadable, and not something file
	// permissions can fake away when the tests happen to run as root.
	if err := os.MkdirAll(filepath.Join(path+".d", "10-override.conf"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := ParseQuadletFile(path)
	if err == nil {
		t.Fatal("expected an unreadable drop-in to be reported, not swallowed")
	}
	if !strings.Contains(err.Error(), "10-override.conf") {
		t.Errorf("error should name the drop-in it could not read, got: %v", err)
	}
}

// TestDropInOverridesBaseFile is the reason drop-ins are applied in name order and an empty
// assignment resets: together they let an override replace a list rather than extend it.
func TestDropInOverridesBaseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.container")
	write(t, path, "[Container]\nImage=alpine\nEnvironment=FOO=base\n")

	dropIn := path + ".d"
	if err := os.MkdirAll(dropIn, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dropIn, "10-reset.conf"), "[Container]\nEnvironment=\n")
	write(t, filepath.Join(dropIn, "20-set.conf"), "[Container]\nEnvironment=FOO=override\n")

	q, err := ParseQuadletFile(path)
	if err != nil {
		t.Fatal(err)
	}
	options := map[string]schema.SchemaOption{}
	if got := OptionValues(options, "Environment", q.Sections["Container"]["Environment"]); !slices.Equal(got, []string{"FOO=override"}) {
		t.Errorf("Environment = %#v, want just the override", got)
	}
}
