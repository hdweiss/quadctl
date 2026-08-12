package core

import (
	"github.com/fkmiec/quadctl/internal/quadlet"
)

func HandleRemove(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {

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

		c := NewCommand(quadletLabel("Removing", q))
		c.Cmd = rmCmd
		commands = append(commands, c)
	}
	return commands, nil
}
