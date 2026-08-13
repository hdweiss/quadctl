package command

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
			// remove_networks/remove_volumes are what the user asked to keep. The systemd path
			// has always honored them; this one used to destroy the data anyway (TODO.md
			// section 2).
			if !quadctl.Config.IsRemoveNetworks {
				commands = append(commands, keptResource(q, "remove_networks=false"))
				continue
			}
			rmCmd = append(rmCmd, "network", "rm", resName)
		case ".volume":
			if !quadctl.Config.IsRemoveVolumes {
				commands = append(commands, keptResource(q, "remove_volumes=false"))
				continue
			}
			rmCmd = append(rmCmd, "volume", "rm", resName)
		}

		c := NewCommand(quadletLabel("Removing", q))
		c.Cmd = rmCmd
		commands = append(commands, c)
	}
	return commands, nil
}

// keptResource is the no-op command that stands in for a removal the configuration asked
// quadctl not to perform. It carries no argv, so it runs nothing and reports what it skipped
// and why - silence here reads as "removed" to anyone watching the output.
func keptResource(q *quadlet.Quadlet, reason string) Command {
	c := NewCommand(quadletLabel("Keeping", q))
	c.Output = []string{"not removed: " + reason + " in quadctl.ini"}
	return c
}
