package core

import (
	"fmt"
	"slices"

	"github.com/fkmiec/quadctl/internal/quadlet"
)

// Call handleCreate. Then start.
func HandleRun(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {

	//Check how many .container quadlets there are and how many with --detach or -d podman args.
	//If more than one .container and more than one of them don't have --detach or -d,
	//print a warning and exit.
	nonDetachedContainers := 0
	var foregroundQuadlet *quadlet.Quadlet
	var foregroundQuadletCommand Command
	for _, q := range quadlets {
		if q.Type == ".container" {
			// PodmanArgs is written as a command line fragment, so split it before looking
			// for the detach flag - "PodmanArgs=-d --rm" is one written value, two args.
			pArgs := getRawPodmanArgs(q.Sections["Container"])
			if !slices.Contains(pArgs, "--detach") && !slices.Contains(pArgs, "-d") {
				foregroundQuadlet = q
				nonDetachedContainers++
			}
		}
	}
	if nonDetachedContainers > 1 {
		return nil, fmt.Errorf("'quadctl run' can only run one container in the foreground. Add --detach or -d to PodmanArgs for all other .container quadlets. Execute quadctl run --help for details")
	}

	commands := []Command{}

	//Create non-container resources, if necessary (HandleCreate will skip .container quadlets for the 'run' command, but create volumes, networks, pods if needed)
	c, err := HandleCreate(quadctl, quadlets)
	if err != nil {
		return nil, err
	}
	commands = append(commands, c...)

	//Start
	for _, q := range quadlets {
		// Only run containers and kubes. Pods, networks and volumes will be started/created as needed by the containers.
		if q.Type != ".container" && q.Type != ".kube" {
			continue
		}
		// For 'run' command, we need to generate 'podman run' commands instead of 'podman start' for containers.
		cmd, warns := generateRunCommand(quadctl, q)
		if len(cmd) > 0 {
			command := NewCommand(quadletLabel("Running", q))
			command.Cmd = cmd
			command.Warnings = warns

			if foregroundQuadlet != nil && q.ID == foregroundQuadlet.ID {
				foregroundQuadletCommand = command
				continue
			}
			commands = append(commands, command)
		}
	}
	if foregroundQuadlet != nil {
		// Run the foreground container command last since it will block and we want all other containers to be up before it runs.
		commands = append(commands, foregroundQuadletCommand)
	}
	return commands, nil
}
