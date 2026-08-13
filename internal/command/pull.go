package command

import (
	"fmt"

	"github.com/fkmiec/quadctl/internal/quadlet"
)

func HandlePull(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {
	commands := []Command{}

	images := []string{}
	for _, q := range quadlets {
		switch q.Type {
		case ".container":
			if img := quadlet.LastValue(q.Sections["Container"], "Image"); img != "" {
				images = append(images, img)
			}
		case ".kube":
			for _, res := range q.KubeResources {
				// A container in the k8s YAML may carry no image: key at all.
				if img, ok := res["image"].(string); ok && res["type"] == "container" {
					images = append(images, img)
				}
			}
		}
	}

	for _, img := range images {
		c := NewCommand(fmt.Sprintf("Pulling image %s", img))
		c.Cmd = []string{"podman", "pull", img}
		commands = append(commands, c)
	}

	return commands, nil
}
