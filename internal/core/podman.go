package core

import (
	"path/filepath"
	"strings"

	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/util"
)

func resourceExists(r runner.Runner, qType string, name string) bool {
	inspectCmd := []string{"podman"}
	switch qType {
	case ".container":
		inspectCmd = append(inspectCmd, "container", "inspect", name)
	case ".pod":
		inspectCmd = append(inspectCmd, "pod", "inspect", name)
	case ".network":
		inspectCmd = append(inspectCmd, "network", "inspect", name)
	case ".volume":
		inspectCmd = append(inspectCmd, "volume", "inspect", name)
	default:
		return false
	}
	return runner.RunSilent(r, inspectCmd) == nil
}

func listSystemdInstalledQuadlets(quadctl *util.State, quadlets []*util.Quadlet) ([][]string, error) {
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
func listInstalledQuadletsViaSystemctl(quadctl *util.State, quadlets []*util.Quadlet) ([][]string, error) {
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

func getContainerPS(r runner.Runner, quadlets []*util.Quadlet) ([][]string, error) {
	cmd := []string{"podman", "ps", "-a", "--format", "{{.ID}}|{{.Names}}|{{.PodName}}|{{.Status}}|{{.Ports}}|{{.Image}}|{{.Created}}"}
	output, err := runner.RunCaptured(r, cmd)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(output, "\n")
	var psInfo [][]string
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 7 {
			continue
		}
		//filter for containers that match our quadlet definitions by name or parent pod
		for _, q := range quadlets {
			if quadletOwnsContainer(q, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])) {
				psInfo = append(psInfo, parts)
				break
			}
		}
	}
	return psInfo, nil
}

// quadletOwnsContainer reports whether the podman container called name, in the pod called
// pod (both empty-able, as podman prints them), is one this quadlet describes.
//
// Names are compared exactly. The old code asked whether the podman name *ended with* the
// quadlet's, which made the quadlet `web` claim an unrelated container called `myweb`; it was
// only ever a way to paper over quadctl and the quadlet generator disagreeing about names,
// and PLAN.md Phase 4 removed the disagreement.
func quadletOwnsContainer(q *util.Quadlet, name, pod string) bool {
	switch q.Type {
	case ".container":
		if name == q.ResourceName {
			return true
		}
		// A container in a pod is also reported for the pod it belongs to, so that the pod's
		// infra container and any sibling show up alongside it.
		return q.PodResourceName != "" && pod == q.PodResourceName
	case ".kube":
		// 'podman kube play' names the pod after the YAML's metadata.name and each container
		// "<pod>-<container name>". Names here come from user-supplied YAML, so neither key
		// is guaranteed to be present or a string.
		for _, res := range q.KubeResources {
			resName, hasName := res["name"].(string)
			resPod, hasPod := res["pod"].(string)
			if hasPod && pod == resPod {
				return true
			}
			if res["type"] == "container" && hasName && hasPod && name == resPod+"-"+resName {
				return true
			}
			if res["type"] == "pod" && hasName && pod == resName {
				return true
			}
		}
	}
	return false
}
