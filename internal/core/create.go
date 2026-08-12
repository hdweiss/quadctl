package core

import (
	"fmt"
	"path/filepath"

	"github.com/fkmiec/quadctl/internal/util"
)

// handleCreate generates and executes 'podman create' commands for all resources, but first checks if they exist and prints warnings if they do,
// suggesting to run 'remove' first if intent is to re-create. It also handles special cases like auto-restart configuration warnings.
func HandleCreate(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	for _, q := range quadlets {

		// For .kube, podman kube play will be called in start step. Return a no-op command with a warning here if verbose output is enabled.
		if q.Type == ".kube" && quadctl.IsVerbose {
			cmd := NewCommand(fmt.Sprintf("Creating %s %s", q.Type, q.ID))
			cmd.Warnings = append(cmd.Warnings, fmt.Sprintf("Podman kube play handles creation. Nothing to do for %s %s", q.Type, q.ID))
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
			cmd := NewCommand(fmt.Sprintf("Creating %s %s", q.Type, q.ID))
			cmd.Cmd = args

			for _, w := range warns {
				cmd.Warnings = append(cmd.Warnings, fmt.Sprintf("%s: %s", filepath.Base(q.Filepath), w))
			}

			// Warn about restart policy configuration, if applicable
			if q.RestartPolicy != "" && q.RestartPolicy != "no" {
				cmd.Warnings = append(cmd.Warnings, fmt.Sprintf("[INFO] %s: Restart policy configured (%s). Ensure podman-restart.service is enabled.\n", q.Filepath, q.RestartPolicy))
			}
			// Warn about AutoUpdate configuration, if applicable
			if q.AutoUpdate != "" {
				cmd.Warnings = append(cmd.Warnings, fmt.Sprintf("[INFO] %s: Image AutoUpdate enabled (%s)\n", q.Filepath, q.AutoUpdate))
			}

			commands = append(commands, cmd)

		} else {
			if quadctl.IsVerbose {
				cmd := NewCommand(fmt.Sprintf("Creating %s %s", q.Type, q.ID))
				cmd.Cmd = []string{"echo"}
				cmd.Warnings = append(cmd.Warnings, fmt.Sprintf(" [INFO] %s %s already exists. To force re-creation of ALL resources, run 'quadctl remove' first.\n", q.Type, q.ID))
				commands = append(commands, cmd)
			}
		}
	}
	return commands, nil
}
