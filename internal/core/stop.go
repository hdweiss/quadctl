package core

import (
	"github.com/fkmiec/quadctl/internal/util"
)

func HandleStop(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	// Reverse order for safe stopping
	for i := len(quadlets) - 1; i >= 0; i-- {
		q := quadlets[i]
		cmd := generateStopCommand(quadctl, q)
		if len(cmd) > 0 {
			c := NewCommand(quadletLabel("Stopping", q))
			c.Cmd = cmd
			commands = append(commands, c)
		}
	}
	return commands, nil
}
