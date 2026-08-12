package core

import (
	"fmt"
	"strings"

	"github.com/fkmiec/quadctl/util"
)

// Call handleCreate. Then start.
func HandleStart(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	//Create, if necessary
	cmds, err := HandleCreate(quadctl, quadlets)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmds...)

	// Stop if already running (podman ps -a only returns a list if systemd services are running. Once stopped, it returns empty.)
	if info, err := getContainerPS(quadctl.Runner, quadlets); err == nil && len(info) > 0 {
		if strings.Contains(info[0][3], "Up") {
			cmd, err := HandleStop(quadctl, quadlets)
			if err != nil {
				return nil, err
			}
			commands = append(commands, cmd...)
		}
	}

	//Start
	for _, q := range quadlets {
		// Use generateStartupCommands
		cmd, warns := generateStartupCommand(quadctl, q)

		resType := q.Type
		resName := q.ID
		if q.Type == ".container" {
			resName = q.GeneratedNames["container"]
		} else if q.Type == ".pod" {
			resName = q.GeneratedNames["pod_name"]
		}

		if len(cmd) > 0 {
			c := NewCommand(fmt.Sprintf("Starting %s %s", resType, resName))
			c.Cmd = cmd
			c.Warnings = warns
			commands = append(commands, c)
		}
	}
	return commands, nil
}
