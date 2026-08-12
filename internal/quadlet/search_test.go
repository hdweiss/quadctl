package quadlet

import (
	"github.com/fkmiec/quadctl/internal/config"
	"os"
	"path/filepath"
	"testing"
)

// TestGetSearchDir covers the function behind the Phase 0.1 data-loss bug: a relative
// SearchDir made filepath.Base(SearchDir) "." downstream, which collapsed the systemd
// install directory onto the generator root and deleted everything already installed there.
// Every case therefore asserts an absolute path.
func TestGetSearchDir(t *testing.T) {
	// Layout:
	//   <root>/cwd/app.container      - the working directory
	//   <root>/src/webstack/          - a directory under quadlet.src.path
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	src := filepath.Join(root, "src")
	named := filepath.Join(src, "webstack")
	for _, d := range []string{cwd, named} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(cwd, "app.container")
	if err := os.WriteFile(file, []byte("[Container]\nImage=alpine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "web.container"), []byte("[Container]\nImage=alpine\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(cwd)

	tests := []struct {
		name    string
		arg     string
		want    string
		wantErr bool
	}{
		{"no argument uses the working directory", "", cwd, false},
		{"relative file resolves to its parent, absolute", "app.container", cwd, false},
		{"relative directory", ".", cwd, false},
		{"absolute file resolves to its parent", file, cwd, false},
		{"absolute directory", named, named, false},
		{"name resolved under quadlet.src.path", "webstack", named, false},
		{"file under quadlet.src.path resolves to its parent", "webstack/web.container", named, false},
		{"missing", "nope", "", true},
	}

	quadctl := &State{Config: &config.Config{QuadletSrcPath: src}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSearchDir(quadctl, tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ResolveSearchDir(%q) = %q, want an error", tt.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveSearchDir(%q): %v", tt.arg, err)
			}
			if !filepath.IsAbs(got) {
				t.Errorf("ResolveSearchDir(%q) = %q, which is not absolute", tt.arg, got)
			}
			if got != tt.want {
				t.Errorf("ResolveSearchDir(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}
