package command

import (
	"github.com/fkmiec/quadctl/internal/podman"
	"github.com/fkmiec/quadctl/internal/quadlet"
)

// Call handleCreate. Then start.
func HandleStart(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {
	commands := []Command{}

	//Create, if necessary
	cmds, err := HandleCreate(quadctl, quadlets)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmds...)

	// Stop first if any of these quadlets' containers is up, so that a restart is a clean
	// stop-then-start rather than a start on top of what is already running.
	if info, err := podman.ContainerPS(quadctl.Runner, quadlets); err == nil && podman.AnyRunning(info) {
		cmd, err := HandleStop(quadctl, quadlets)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd...)
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
