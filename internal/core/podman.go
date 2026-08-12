package core

import (
	"path/filepath"
	"strings"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
)

func listSystemdInstalledQuadlets(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([][]string, error) {
	cmd := []string{"podman", "quadlet", "list", "--format", "{{.Name}},{{.Path}},{{.UnitName}},{{.Status}}"}
	output, err := runner.RunCaptured(quadctl.Runner, cmd)
	if err != nil {
		// Older podman releases don't have the `quadlet list` subcommand. Fall back to
		// querying each unit's state via systemctl instead of failing outright.
		return listInstalledQuadletsViaSystemctl(quadctl, quadlets)
	}
	//.Printf("podman quadlet list:\n%s\n", output)
	lines := strings.Split(output, "\n")
	var info [][]string
	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			continue
		}
		//filter for our quadlets
		for _, q := range quadlets {
			name := filepath.Base(q.Filepath)
			if strings.TrimSpace(parts[0]) == name {
				info = append(info, parts)
				break
			}
		}
	}
	return info, nil
}

// listInstalledQuadletsViaSystemctl reproduces the columns produced by
// `podman quadlet list` (name, path, unit name, status) by asking systemctl
// about each quadlet's generated unit directly. Used as a fallback on podman
// versions that predate the `quadlet list` subcommand.
//
// Only quadlets whose unit is actually loaded by systemd are included, matching
// the contract of the primary `podman quadlet list` based implementation above:
// callers (e.g. HandleSystemdStart) rely on a quadlet's absence from this list to
// detect that it still needs to be installed. `systemctl is-active` alone can't be
// used for that check since it reports "inactive" both for a stopped-but-installed
// unit and for a unit that was never installed at all.
func listInstalledQuadletsViaSystemctl(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([][]string, error) {
	var info [][]string
	for _, q := range quadlets {
		loadArgs := []string{"systemctl"}
		if !quadctl.IsRootful {
			loadArgs = append(loadArgs, "--user")
		}
		loadArgs = append(loadArgs, "show", q.ServiceName, "--property=LoadState", "--value")
		loadState, _ := runner.RunCaptured(quadctl.Runner, loadArgs)
		loadState = strings.TrimSpace(loadState)
		if loadState == "" || loadState == "not-found" {
			continue
		}

		activeArgs := []string{"systemctl"}
		if !quadctl.IsRootful {
			activeArgs = append(activeArgs, "--user")
		}
		activeArgs = append(activeArgs, "is-active", q.ServiceName)
		output, _ := runner.RunCaptured(quadctl.Runner, activeArgs)
		status := strings.TrimSpace(output)
		if status == "" {
			status = "unknown"
		}
		info = append(info, []string{filepath.Base(q.Filepath), q.Filepath, q.ServiceName, status})
	}
	return info, nil
}
