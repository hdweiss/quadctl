package core

import (
	"fmt"

	"github.com/fkmiec/quadctl/util"
)

func HandleStop(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	// Reverse order for safe stopping
	for i := len(quadlets) - 1; i >= 0; i-- {
		q := quadlets[i]
		resType := q.Type
		resName := q.ID
		if q.Type == ".container" {
			resName = q.GeneratedNames["container"]
		} else if q.Type == ".pod" {
			resName = q.GeneratedNames["pod_name"]
		}
		cmd := generateStopCommand(quadctl, q)
		if len(cmd) > 0 {
			c := NewCommand(fmt.Sprintf("Stopping %s %s", resType, resName))
			c.Cmd = cmd
			commands = append(commands, c)
		}
	}
	return commands, nil
}
