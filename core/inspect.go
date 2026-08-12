package core

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fkmiec/quadctl/util"

	"github.com/jedib0t/go-pretty/v6/table"
)

func HandlePS(quadctl *util.Quadctl, quadlets []*util.Quadlet) error {

	psInfo, err := getContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		return err
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
	t.SetStyle(table.StyleColoredYellowWhiteOnBlack)
	t.Render()

	return nil
}

func HandleStats(quadctl *util.Quadctl, quadlets []*util.Quadlet) error {

	psInfo, err := getContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		return err
	}

	if len(psInfo) < 1 {
		return fmt.Errorf("found no containers running or created that are related to quadlets in directory: %s", quadctl.SearchDir)
	}

	//cmd := []string{"podman", "stats", "--no-stream"}
	cmd := []string{"podman", "stats"}

	for _, info := range psInfo {
		id := strings.TrimSpace(info[0])
		cmd = append(cmd, id)
	}

	return runCommand(quadctl.Runner, cmd)
}

func HandleImages(runner util.Runner, quadlets []*util.Quadlet) error {

	//REPOSITORY                                 TAG         IMAGE ID      CREATED       SIZE
	cmd := []string{"podman", "images", "--noheading", "--filter", "reference=ADD_ID_HERE", "--format", "{{.Repository}}|{{.Tag}}|{{.ID}}|{{.Created}}|{{.Size}}"}
	imageInfo := [][]string{}

	// Fetch image info for each container
	psInfo, err := getContainerPS(runner, quadlets)
	if err != nil {
		return err
	}

	if len(psInfo) > 0 {
		for _, info := range psInfo {
			name := strings.TrimSpace(info[5]) // IMAGE ID from ps output
			if len(name) < 12 {
				continue
			}
			cmd[4] = "reference=" + name
			output, err := runCommandCapture(runner, cmd)
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
				if imgSec, ok := q.Sections["Container"]; ok {
					if imgList, ok := imgSec["Image"]; ok && len(imgList) > 0 {
						name := strings.TrimSpace(imgList[0]) // IMAGE ID from quadlet file
						if len(name) < 12 {
							continue
						}
						cmd[4] = "reference=" + name
						output, err := runCommandCapture(runner, cmd)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Error fetching image info for quadlet %s: %v\n", q.ID, err)
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
			} else if q.Type == ".kube" {
				for _, res := range q.KubeResources {
					if res["type"] == "container" {
						// A container in the k8s YAML may have no image: key at all
						image, ok := res["image"].(string)
						if !ok {
							continue
						}
						name := strings.TrimSpace(image) // IMAGE ID from quadlet file
						if len(name) < 12 {
							continue
						}
						cmd[4] = "reference=" + name
						output, err := runCommandCapture(runner, cmd)
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
	t.SetStyle(table.StyleColoredYellowWhiteOnBlack)
	t.Render()

	return nil
}

func HandleLogs(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {

	var commands []Command

	cmd := []string{"podman", "logs"}
	var containerName string

	// Fetch image info for each container
	psInfo, err := getContainerPS(quadctl.Runner, quadlets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return commands, nil
	}

	if len(psInfo) > 0 {
		if len(psInfo) == 1 {
			containerName = psInfo[0][1]
		} else {
			names := []string{}
			for _, info := range psInfo {
				names = append(names, info[1])
			}
			selected, err := util.SelectFromList(names)
			if err != nil {
				return nil, fmt.Errorf("selecting container: %w", err)
			}
			containerName = selected
		}
		cmd = append(cmd, containerName)
	}

	if quadctl.IsPrintOnly {
		c := NewCommand(fmt.Sprintf("Opening podman logs for %s\n", containerName))
		c.Cmd = cmd
		commands = append(commands, c)
	} else {
		runCommand(quadctl.Runner, cmd)
	}
	return commands, nil
}
