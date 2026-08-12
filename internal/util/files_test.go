package util

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCopyFilePreservesMode covers TODO.md section 2: the destination mode was hardcoded
// 0644, so a deliberately restrictive file - an .env sitting beside the quadlets - became
// world-readable once installed into the generator directory.
func TestCopyFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "secrets.env")
	write(t, src, "TOKEN=hunter2\n")
	if err := os.Chmod(src, 0600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "copy.env")
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	if mode := statMode(t, dst); mode != 0600 {
		t.Errorf("copied file mode = %O, want 0600", mode)
	}

	// Overwriting an existing copy has to update its mode too, not keep the old one.
	if err := os.Chmod(src, 0640); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile (overwrite): %v", err)
	}
	if mode := statMode(t, dst); mode != 0640 {
		t.Errorf("overwritten file mode = %O, want 0640", mode)
	}
	if got := readFile(t, dst); got != "TOKEN=hunter2\n" {
		t.Errorf("contents = %q", got)
	}
}

// TestCopyDirRecurses covers the other half of the same section: CopyDir skipped
// subdirectories, so a drop-in directory or a bind-mounted config/ folder was silently left
// behind when the quadlet directory was installed.
func TestCopyDirRecurses(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "web.container"), "[Container]\nImage=nginx\n")
	if err := os.MkdirAll(filepath.Join(src, "web.container.d"), 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "web.container.d", "10-override.conf"), "[Container]\nEnvironment=EXTRA=1\n")
	if err := os.MkdirAll(filepath.Join(src, "config", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "config", "nested", "nginx.conf"), "server {}\n")

	dst := filepath.Join(t.TempDir(), "installed")
	if err := CopyDir(src, dst); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	for _, rel := range []string{
		"web.container",
		filepath.Join("web.container.d", "10-override.conf"),
		filepath.Join("config", "nested", "nginx.conf"),
	} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("%s was not copied: %v", rel, err)
		}
	}
	if mode := statMode(t, filepath.Join(dst, "config")); mode != 0700 {
		t.Errorf("copied directory mode = %O, want 0700", mode)
	}
}

func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
