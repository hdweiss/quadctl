package util

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// loadConfigFrom writes an ini into a scratch config directory and loads it, the way
// LoadConfig does on a real host.
func loadConfigFrom(t *testing.T, ini string) *Config {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("QUADCTL_CONFIG_DIR", dir)
	// LoadConfig creates quadlet.src.path and quadlet.user.path, so keep them inside the
	// scratch directory rather than in the developer's home.
	ini = strings.ReplaceAll(ini, "{{dir}}", dir)
	write(t, filepath.Join(dir, "quadctl.ini"), ini)

	cfg, err := LoadConfig(false)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// TestConfigBooleansAreParsedBothWays covers TODO.md section 4: each boolean key used to be
// compared against one hardcoded pair of spellings, in one direction only, so "True" and
// "yes" were dropped without a word and a key could not be turned back on once its default
// was off.
func TestConfigBooleansAreParsedBothWays(t *testing.T) {
	cfg := loadConfigFrom(t, strings.Join([]string{
		"quadlet.src.path={{dir}}/src",
		"quadlet.user.path={{dir}}/user",
		"use_subdirectories=No",
		"use_symbolic_links=YES",
		"auto_reload_systemd=off",
		"remove_volumes=0",
		"remove_networks=On",
		"systemd.enabled=True",
	}, "\n"))

	for _, tt := range []struct {
		key  string
		got  bool
		want bool
	}{
		{"use_subdirectories", cfg.UseSubdirectories, false},
		{"use_symbolic_links", cfg.UseSymbolicLinks, true},
		{"auto_reload_systemd", cfg.IsReloadSystemd, false},
		{"remove_volumes", cfg.IsRemoveVolumes, false},
		{"remove_networks", cfg.IsRemoveNetworks, true},
		{"systemd.enabled", cfg.SystemdEnabled, true},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.key, tt.got, tt.want)
		}
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("expected no warnings, got %q", cfg.Warnings)
	}
}

// TestConfigWarnsOnNonsense covers the other half: a key quadctl doesn't know and a boolean it
// can't read are reported instead of dropped, and neither changes the setting.
func TestConfigWarnsOnNonsense(t *testing.T) {
	cfg := loadConfigFrom(t, strings.Join([]string{
		"quadlet.src.path={{dir}}/src",
		"quadlet.user.path={{dir}}/user",
		"remove_volumes=maybe",
		"use_subdirectorys=false",
	}, "\n"))

	if !cfg.IsRemoveVolumes {
		t.Error("an unreadable boolean should leave the default alone")
	}
	if !cfg.UseSubdirectories {
		t.Error("a misspelled key should not set anything")
	}

	joined := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{"remove_volumes", `"maybe"`, "use_subdirectorys"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings should mention %s, got:\n%s", want, joined)
		}
	}
}

// TestDefaultUserQuadletPathMatchesShippedConfig covers the last part of TODO.md section 4:
// the compiled-in default was /etc/containers/systemd/users while the ini quadctl writes on
// first run says the XDG path, so the two disagreed about where rootless quadlets are
// installed depending on whether the file had been edited.
func TestDefaultUserQuadletPathMatchesShippedConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/someone")

	want := "/home/someone/.config/containers/systemd"
	if got := DefaultUserQuadletPath(); got != want {
		t.Errorf("DefaultUserQuadletPath() = %q, want %q", got, want)
	}

	shipped, err := files.ReadFile("config/quadctl.ini")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(shipped, []byte("quadlet.user.path={{.home}}/.config/containers/systemd")) {
		t.Error("the shipped quadctl.ini no longer writes the XDG path; the default above has to move with it")
	}
}
