package core

import (
	"fmt"

	"github.com/fkmiec/quadctl/internal/util"
)

// handleCreate generates and executes 'podman create' commands for all resources, but first checks if they exist and prints warnings if they do,
// suggesting to run 'remove' first if intent is to re-create. It also handles special cases like auto-restart configuration warnings.
func HandleCreate(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	for _, q := range quadlets {

		// For .kube, podman kube play will be called in start step. Return a no-op command with a warning here if verbose output is enabled.
		if q.Type == ".kube" && quadctl.IsVerbose {
			cmd := NewCommand(quadletLabel("Creating", q))
			cmd.Warnings = append(cmd.Warnings, InfoPrefix+fmt.Sprintf("podman kube play handles creation; nothing to do for %s", q.DisplayName()))
			cmd.Cmd = []string{"echo"}
			commands = append(commands, cmd)
			continue
		}

		// Only create if the resource doesn't exist. The check has to use the name podman
		// will actually give it, not the file's base name: a quadlet with ContainerName= set
		// looked absent on every run, so create ran every time and podman refused the
		// duplicate name (TODO.md section 2).
		if !resourceExists(quadctl.Runner, q.Type, q.ResourceName) {
			// For 'run' command, skip creating containers since 'podman run' will create them if they don't exist.
			if quadctl.Subcommand == "run" && q.Type == ".container" {
				continue
			}
			args, warns := generateCreateCommand(quadctl, q)
			cmd := NewCommand(quadletLabel("Creating", q))
			cmd.Cmd = args

			cmd.Warnings = append(cmd.Warnings, warns...)

			// Warn about restart policy configuration, if applicable
			if q.RestartPolicy != "" && q.RestartPolicy != "no" {
				cmd.Warnings = append(cmd.Warnings, InfoPrefix+fmt.Sprintf("restart policy configured (%s); ensure podman-restart.service is enabled", q.RestartPolicy))
			}
			// Warn about AutoUpdate configuration, if applicable
			if q.AutoUpdate != "" {
				cmd.Warnings = append(cmd.Warnings, InfoPrefix+fmt.Sprintf("image AutoUpdate enabled (%s)", q.AutoUpdate))
			}

			commands = append(commands, cmd)

		} else {
			if quadctl.IsVerbose {
				cmd := NewCommand(quadletLabel("Creating", q))
				cmd.Cmd = []string{"echo"}
				cmd.Warnings = append(cmd.Warnings, InfoPrefix+"already exists; run 'quadctl remove' first to force re-creation of all resources")
				commands = append(commands, cmd)
			}
		}
	}
	return commands, nil
}
