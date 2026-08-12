package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/fkmiec/quadctl/internal/config"
	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/util"
)

func pruneTestQuadctl() *util.State {
	q := &util.State{
		Runner:     &runner.RecordingRunner{},
		Subcommand: "create",
		IsRootful:  true,
		Config:     config.DefaultConfig(),
	}
	q.Config.SystemdStopTmpl = template.Must(template.New("systemdStop").Parse("systemctl {{.user}} stop"))
	return q
}

// TestPruneStaleSystemdFiles covers the removal half of the Phase 0.1 data-loss bug: files
// that vanished from the source directory should be removed from the installed copy, and
// nothing else should ever be touched.
func TestPruneStaleSystemdFiles(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "generator")
	searchDir := filepath.Join(root, "src", "webstack")
	installed := filepath.Join(targetDir, "webstack")

	mkdirAll(t, searchDir, installed)
	// Present in both: kept. Present only in the installed copy: stale.
	writeFile(t, filepath.Join(searchDir, "app.container"), "[Container]\nImage=alpine\n")
	writeFile(t, filepath.Join(installed, "app.container"), "[Container]\nImage=alpine\n")
	writeFile(t, filepath.Join(installed, "gone.container"), "[Container]\nImage=alpine\n")
	writeFile(t, filepath.Join(installed, "notes.txt"), "leftover\n")
	mkdirAll(t, filepath.Join(installed, "olddir"))
	// An unrelated quadlet group installed alongside ours: must not be touched.
	unrelated := filepath.Join(targetDir, "other", "keep.container")
	mkdirAll(t, filepath.Dir(unrelated))
	writeFile(t, unrelated, "[Container]\nImage=alpine\n")

	commands, err := pruneStaleSystemdFiles(pruneTestQuadctl(), targetDir, filepath.Base(searchDir), searchDir)
	if err != nil {
		t.Fatalf("pruneStaleSystemdFiles: %v", err)
	}

	labels := labelsOf(commands)
	for _, want := range []string{"gone.container", "notes.txt", "olddir"} {
		if !containsSubstring(labels, want) {
			t.Errorf("%s should have been pruned; commands were %v", want, labels)
		}
	}
	for _, unwanted := range []string{"app.container", "keep.container", "other"} {
		if containsSubstring(labels, unwanted) {
			t.Errorf("%s should not have been pruned; commands were %v", unwanted, labels)
		}
	}

	// A stale quadlet definition gets its service stopped before the file is deleted.
	if !containsSubstring(commandLines(commands), "systemctl stop") {
		t.Errorf("expected a systemctl stop for the stale quadlet, got %v", commandLines(commands))
	}

	// Running them must leave everything outside the installed subdirectory alone.
	RunCommands(pruneTestQuadctl(), commands)
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("unrelated installed quadlet was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installed, "app.container")); err != nil {
		t.Errorf("still-present quadlet was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installed, "gone.container")); err == nil {
		t.Error("stale quadlet was not removed")
	}
}

// TestPruneStaleSystemdFilesRefusesGeneratorRoot is the guard itself: with a degenerate
// source path, the install directory collapses onto the generator root and every unrelated
// quadlet there looks stale. Nothing may be generated in that case.
func TestPruneStaleSystemdFilesRefusesGeneratorRoot(t *testing.T) {
	targetDir := t.TempDir()
	writeFile(t, filepath.Join(targetDir, "unrelated.container"), "[Container]\nImage=alpine\n")
	mkdirAll(t, filepath.Join(targetDir, "traefik"))

	for _, installName := range []string{".", "..", "/", ""} {
		commands, err := pruneStaleSystemdFiles(pruneTestQuadctl(), targetDir, installName, t.TempDir())
		if err != nil {
			t.Fatalf("installName %q: %v", installName, err)
		}
		if len(commands) != 0 {
			t.Errorf("installName %q generated %v, want nothing", installName, labelsOf(commands))
		}
	}
}

func TestPruneStaleSystemdFilesSkippedWithoutSubdirectories(t *testing.T) {
	quadctl := pruneTestQuadctl()
	quadctl.Config.UseSubdirectories = false

	commands, err := pruneStaleSystemdFiles(quadctl, t.TempDir(), "stack", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 0 {
		t.Errorf("got %v, want nothing when quadlets share the generator directory", labelsOf(commands))
	}
}

func labelsOf(commands []Command) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		out = append(out, c.Label)
	}
	return out
}

func commandLines(commands []Command) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		out = append(out, strings.Join(c.Cmd, " "))
	}
	return out
}

func containsSubstring(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func mkdirAll(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestSystemdInstallNameComesFromTheSourceDirectory pins what the scratch-directory change
// in PLAN.md 3.2 could quietly have broken. A .quadlets bundle is extracted somewhere
// private, but the directory it installs into is still named after the user's own directory:
// name it after the scratch directory and every run installs a new randomly named copy that
// HandleSystemdRemove and the stale-file prune can never find again.
func TestSystemdInstallNameComesFromTheSourceDirectory(t *testing.T) {
	targetDir := t.TempDir()
	srcDir := filepath.Join(t.TempDir(), "webstack")
	scratch := filepath.Join(t.TempDir(), "quadctl-quadlets-123456")
	mkdirAll(t, srcDir, scratch)
	writeFile(t, filepath.Join(scratch, "cache.container"), "[Container]\nImage=alpine\n")

	quadctl := pruneTestQuadctl()
	quadctl.SearchDir = srcDir
	quadctl.DotQuadletsPath = scratch
	quadctl.Config.QuadletRootPath = targetDir

	commands, err := HandleSystemdCreate(quadctl, nil)
	if err != nil {
		t.Fatalf("HandleSystemdCreate: %v", err)
	}

	var installed []string
	for _, c := range commands {
		installed = append(installed, c.Output...)
	}
	if !containsSubstring(installed, filepath.Join(targetDir, "webstack")) {
		t.Errorf("install should go to the source directory's name, got:\n  %s", strings.Join(installed, "\n  "))
	}
	if containsSubstring(installed, "quadctl-quadlets-123456") {
		t.Errorf("install directory is named after the scratch directory:\n  %s", strings.Join(installed, "\n  "))
	}
}
