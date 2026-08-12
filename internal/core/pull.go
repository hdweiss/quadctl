package core

import (
	"fmt"

	"github.com/fkmiec/quadctl/internal/util"
)

func HandlePull(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	images := []string{}
	for _, q := range quadlets {
		if q.Type == ".container" {
			if img := util.LastValue(q.Sections["Container"], "Image"); img != "" {
				images = append(images, img)
			}
		} else if q.Type == ".kube" {
			for _, res := range q.KubeResources {
				// A container in the k8s YAML may carry no image: key at all.
				if img, ok := res["image"].(string); ok && res["type"] == "container" {
					images = append(images, img)
				}
			}
		}
	}

	for _, img := range images {
		//fmt.Printf("=> Pulling image: %s\n", img)
		c := NewCommand(fmt.Sprintf("Pulling image %s", img))
		c.Cmd = []string{"podman", "pull", img}
		commands = append(commands, c)
	}

	return commands, nil
	//RunCommands(quadctl, commands)
}
