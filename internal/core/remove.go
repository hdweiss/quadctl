package core

import (
	"fmt"

	"github.com/fkmiec/quadctl/internal/util"
)

func HandleRemove(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	// Reverse order for safe removal
	for i := len(quadlets) - 1; i >= 0; i-- {
		q := quadlets[i]
		resName := q.DisplayName()

		rmCmd := []string{"podman"}
		switch q.Type {
		case ".kube":
			if quadctl.Config.IsRemoveVolumes || quadctl.Config.IsRemoveNetworks || kubeDownForce(q) {
				rmCmd = append(rmCmd, "play", "kube", "--down", "--force", q.KubernetesYaml)
			} else {
				rmCmd = append(rmCmd, "play", "kube", "--down", q.KubernetesYaml)
			}
		case ".container":
			rmCmd = append(rmCmd, "container", "rm", "-f", resName)
		case ".pod":
			rmCmd = append(rmCmd, "pod", "rm", "-f", resName)
		case ".network":
			rmCmd = append(rmCmd, "network", "rm", resName)
		case ".volume":
			rmCmd = append(rmCmd, "volume", "rm", resName)
		}

		c := NewCommand(fmt.Sprintf("Removing %s %s", q.Type, resName))
		c.Cmd = rmCmd
		commands = append(commands, c)
	}
	return commands, nil
}
