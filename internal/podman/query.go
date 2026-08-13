package podman

import (
	"strings"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
)

func ResourceExists(r runner.Runner, qType string, name string) bool {
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

// PSArgs is the query ContainerPS runs. Exported so that print mode can show the command a
// listing came from instead of silently running it anyway.
func PSArgs() []string {
	return []string{"podman", "ps", "-a", "--format", "{{.ID}}|{{.Names}}|{{.PodName}}|{{.Status}}|{{.Ports}}|{{.Image}}|{{.Created}}"}
}

func ContainerPS(r runner.Runner, quadlets []*quadlet.Quadlet) ([][]string, error) {
	cmd := PSArgs()
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

// statusColumn is where ContainerPS puts podman's Status field in each row.
const statusColumn = 3

// AnyRunning reports whether any row ContainerPS returned describes a container that is
// currently up. podman writes "Up 3 minutes", "Up 2 hours (healthy)", "Exited (0) 5 minutes
// ago", "Created", ... - so the question is what the status starts with.
//
// 'start' used to ask this of the first row only, which answered for whichever container
// podman happened to list first: a stopped one at the head meant a running stack was never
// restarted (TODO.md section 2). The systemd path asked only whether there were any rows at
// all, and 'podman ps -a' lists exited containers too.
func AnyRunning(psInfo [][]string) bool {
	for _, row := range psInfo {
		if len(row) > statusColumn && strings.HasPrefix(strings.TrimSpace(row[statusColumn]), "Up") {
			return true
		}
	}
	return false
}

// quadletOwnsContainer reports whether the podman container called name, in the pod called
// pod (both empty-able, as podman prints them), is one this quadlet describes.
//
// Names are compared exactly. The old code asked whether the podman name *ended with* the
// quadlet's, which made the quadlet `web` claim an unrelated container called `myweb`; it was
// only ever a way to paper over quadctl and the quadlet generator disagreeing about names,
// and PLAN.md Phase 4 removed the disagreement.
func quadletOwnsContainer(q *quadlet.Quadlet, name, pod string) bool {
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
