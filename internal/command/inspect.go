package command

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fkmiec/quadctl/internal/podman"
	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/tui"
	"github.com/jedib0t/go-pretty/v6/table"
)

func HandlePS(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {
	// In print mode, say what would be run rather than running it and printing a table: -p
	// used to be accepted by 'status' and change nothing at all (TODO.md section 3). The
	// filtering down to these quadlets' containers happens in quadctl, so the command shown is
	// the query, not the whole answer.
	if quadctl.IsPrintOnly {
		c := NewCommand("Listing containers for these quadlets")
		c.Cmd = podman.PSArgs()
		return []Command{c}, nil
	}

	psInfo, err := podman.ContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		return nil, err
	}

	if len(psInfo) == 0 {
		fmt.Fprintf(os.Stderr, "No containers found for the quadlets in %s.\n", quadctl.SearchDir)
		return nil, nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"CONTAINER ID", "NAME", "POD", "STATE", "PORTS", "IMAGE", "CREATED"})
	format := "2006-01-02 15:04:05.999999999 -0700 MST"
	for _, info := range psInfo {
		if len(info) >= 7 {
			createdDatetime, err := time.Parse(format, strings.TrimSpace(info[6]))
			createdDuration := "unknown"
			if err == nil {
				createdDuration = time.Since(createdDatetime).Round(time.Second).String() + " ago"
			}
			t.AppendRow(table.Row{
				strings.TrimSpace(info[0]),
				strings.TrimSpace(info[1]),
				strings.TrimSpace(info[2]),
				strings.TrimSpace(info[3]),
				strings.TrimSpace(info[4]),
				strings.TrimSpace(info[5]),
				strings.TrimSpace(createdDuration),
			})
		}
	}
	t.SetStyle(TableStyle(quadctl))
	t.Render()

	return nil, nil
}

func HandleStats(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {
	psInfo, err := podman.ContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		return nil, err
	}

	// Nothing matching is not a failure - ps says so and exits 0 for exactly the same
	// situation, and the two used to disagree (TODO.md section 4).
	if len(psInfo) < 1 {
		fmt.Fprintf(os.Stderr, "No containers found for the quadlets in %s.\n", quadctl.SearchDir)
		return nil, nil
	}

	cmd := []string{"podman", "stats"}
	for _, info := range psInfo {
		cmd = append(cmd, strings.TrimSpace(info[0]))
	}

	c := NewCommand("Showing live container stats")
	c.Cmd = cmd
	c.Stream = true
	return []Command{c}, nil
}

func HandleImages(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) error {
	r := quadctl.Runner

	//REPOSITORY                                 TAG         IMAGE ID      CREATED       SIZE
	cmd := []string{"podman", "images", "--noheading", "--filter", "reference=ADD_ID_HERE", "--format", "{{.Repository}}|{{.Tag}}|{{.ID}}|{{.Created}}|{{.Size}}"}
	imageInfo := [][]string{}

	// Fetch image info for each container
	psInfo, err := podman.ContainerPS(r, quadlets)
	if err != nil {
		return err
	}

	if len(psInfo) > 0 {
		for _, info := range psInfo {
			// The image the container was created from, as podman reports it: a reference
			// like "docker.io/library/alpine:3.20", not an ID. The old code skipped anything
			// under 12 characters here, presumably guarding against truncated IDs, which hid
			// every short image name there is - alpine, nginx, caddy.
			name := strings.TrimSpace(info[5])
			if name == "" {
				continue
			}
			cmd[4] = "reference=" + name
			output, err := runner.RunCaptured(r, cmd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error fetching image info for container %s: %v\n", info[0], err)
				continue
			}
			lines := strings.Split(output, "\n")
			for _, line := range lines {
				parts := strings.Split(line, "|")
				if len(parts) >= 5 {
					imageInfo = append(imageInfo, parts)
				} else {
					// Typically an empty newline
					//fmt.Printf("Warning: Unexpected output from podman ps. Expected 5 or more values. Got: %s\n", line)
					continue
				}
			}
		}
	} else {
		// If no containers are found, we can still fetch image info for the quadlet files
		fmt.Fprintf(os.Stderr, "No containers found, fetching image info from quadlet files...\n")
		for _, q := range quadlets {
			// Images only pertain to containers
			if q.Type == ".container" {
				name := strings.TrimSpace(quadlet.LastValue(q.Sections["Container"], "Image"))
				if name == "" {
					continue
				}
				cmd[4] = "reference=" + name
				output, err := runner.RunCaptured(r, cmd)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error fetching image info for quadlet %s: %v\n", q.ID, err)
					continue
				}
				for _, line := range strings.Split(output, "\n") {
					parts := strings.Split(line, "|")
					if len(parts) >= 5 {
						imageInfo = append(imageInfo, parts)
					}
				}
			} else if q.Type == ".kube" {
				for _, res := range q.KubeResources {
					if res["type"] == "container" {
						// A container in the k8s YAML may have no image: key at all
						image, ok := res["image"].(string)
						if !ok {
							continue
						}
						name := strings.TrimSpace(image)
						if name == "" {
							continue
						}
						cmd[4] = "reference=" + name
						output, err := runner.RunCaptured(r, cmd)
						if err != nil {
							resName, _ := res["name"].(string)
							fmt.Fprintf(os.Stderr, "Error fetching image info for .kube %s container %s: %v\n", q.ID, resName, err)
							continue
						}
						lines := strings.Split(output, "\n")
						for _, line := range lines {
							parts := strings.Split(line, "|")
							if len(parts) >= 5 {
								imageInfo = append(imageInfo, parts)
							}
						}
					}
				}
			}
		}
	}
	if len(imageInfo) == 0 {
		fmt.Fprintf(os.Stderr, "No images found for the quadlets in %s.\n", quadctl.SearchDir)
		return nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"REPOSITORY", "TAG", "IMAGE ID", "CREATED", "SIZE"})
	for _, info := range imageInfo {
		if len(info) >= 5 {
			t.AppendRow(table.Row{
				strings.TrimSpace(info[0]),
				strings.TrimSpace(info[1]),
				strings.TrimSpace(info[2]),
				strings.TrimSpace(info[3]),
				strings.TrimSpace(info[4]),
			})
		}
	}
	t.SetStyle(TableStyle(quadctl))
	t.Render()

	return nil
}

func HandleLogs(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]Command, error) {
	var commands []Command

	cmd := []string{"podman", "logs"}
	var containerName string

	// Fetch image info for each container
	psInfo, err := podman.ContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return commands, nil
	}

	// With nothing to name, this used to run a bare 'podman logs' and hand the user podman's
	// "specify at least one container name or ID" - while exiting 0 (TODO.md section 4).
	if len(psInfo) == 0 {
		fmt.Fprintf(os.Stderr, "No containers found for the quadlets in %s.\n", quadctl.SearchDir)
		return nil, nil
	}

	if len(psInfo) == 1 {
		containerName = strings.TrimSpace(psInfo[0][1])
	} else {
		names := []string{}
		for _, info := range psInfo {
			names = append(names, strings.TrimSpace(info[1]))
		}
		selected, err := tui.SelectFromList(names)
		if err != nil {
			return nil, fmt.Errorf("selecting container: %w", err)
		}
		containerName = selected
	}
	cmd = append(cmd, containerName)

	c := NewCommand(fmt.Sprintf("Opening podman logs for container %s", containerName))
	c.Cmd = cmd
	c.Stream = true
	return append(commands, c), nil
}
