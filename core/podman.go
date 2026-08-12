package core

import (
	"path/filepath"
	"strings"

	"github.com/fkmiec/quadctl/util"
)

func resourceExists(runner util.Runner, qType string, name string) bool {
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
	return runCommandSilently(runner, inspectCmd) == nil
}

func listSystemdInstalledQuadlets(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([][]string, error) {
	cmd := []string{"podman", "quadlet", "list", "--format", "{{.Name}},{{.Path}},{{.UnitName}},{{.Status}}"}
	output, err := runCommandCapture(quadctl.Runner, cmd)
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
func listInstalledQuadletsViaSystemctl(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([][]string, error) {
	var info [][]string
	for _, q := range quadlets {
		loadArgs := []string{"systemctl"}
		if !quadctl.IsRootful {
			loadArgs = append(loadArgs, "--user")
		}
		loadArgs = append(loadArgs, "show", q.ServiceName, "--property=LoadState", "--value")
		loadState, _ := runCommandCapture(quadctl.Runner, loadArgs)
		loadState = strings.TrimSpace(loadState)
		if loadState == "" || loadState == "not-found" {
			continue
		}

		activeArgs := []string{"systemctl"}
		if !quadctl.IsRootful {
			activeArgs = append(activeArgs, "--user")
		}
		activeArgs = append(activeArgs, "is-active", q.ServiceName)
		output, _ := runCommandCapture(quadctl.Runner, activeArgs)
		status := strings.TrimSpace(output)
		if status == "" {
			status = "unknown"
		}
		info = append(info, []string{filepath.Base(q.Filepath), q.Filepath, q.ServiceName, status})
	}
	return info, nil
}

func getContainerPS(runner util.Runner, quadlets []*util.Quadlet) ([][]string, error) {
	cmd := []string{"podman", "ps", "-a", "--format", "{{.ID}}|{{.Names}}|{{.PodName}}|{{.Status}}|{{.Ports}}|{{.Image}}|{{.Created}}"}
	output, err := runCommandCapture(runner, cmd)
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
			//if q.Type == ".container" && strings.HasSuffix(parts[1], q.GeneratedNames["container"]) || (q.ParentPod != "" && strings.HasSuffix(parts[2], q.ParentPod)) {
			if q.Type == ".container" && strings.HasSuffix(parts[1], q.GeneratedNames["container"]) || (q.ParentPod != "" && strings.HasSuffix(parts[2], q.GeneratedNames["pod_name"])) {
				psInfo = append(psInfo, parts)
				break
			}
			if q.Type == ".kube" {
				for _, res := range q.KubeResources {
					// Names come from user-supplied k8s YAML, so neither key is guaranteed
					// to be present or a string.
					resName, hasName := res["name"].(string)
					podName, hasPod := res["pod"].(string)
					if (res["type"] == "container" && hasName && strings.HasSuffix(parts[1], resName)) || (hasPod && strings.HasSuffix(parts[2], podName)) {
						psInfo = append(psInfo, parts)
						break
					}
				}
			}
		}
	}
	return psInfo, nil
}
