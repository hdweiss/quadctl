package core

import (
	"strings"

	"github.com/fkmiec/quadctl/internal/util"
)

// Call handleCreate. Then start.
func HandleStart(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

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

		if len(cmd) > 0 {
			c := NewCommand(quadletLabel("Starting", q))
			c.Cmd = cmd
			c.Warnings = warns
			commands = append(commands, c)
		}
	}
	return commands, nil
}
