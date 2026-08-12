package core

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/fkmiec/quadctl/util"

	"github.com/jedib0t/go-pretty/v6/table"
)

func HandleSystemdStart(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {
	//Ideally, call handleInstall if needed. How to check if the required systemd services are installed?
	/*
		❯ sudo podman quadlet list
		NAME                   UNIT NAME                    PATH ON DISK                                           STATUS      APPLICATION
		homebox-app.container  homebox-app.service          /etc/containers/systemd/homebox/homebox-app.container  Not loaded
		homebox-data.volume    homebox-data-volume.service  /etc/containers/systemd/homebox/homebox-data.volume    Not loaded
		homebox.pod            homebox-pod.service          /etc/containers/systemd/homebox/homebox.pod            Not loaded
	*/

	commands := []Command{}

	// Always (re)install the quadlet definitions, whether or not they're already
	// installed, so that edits to the source files are picked up on every start.
	// CopyFile/CopyDir overwrite existing files in place, so this is a no-op cost
	// wise when nothing has changed. HandleSystemdCreate also reloads systemd
	// after copying so the generator picks up any changes.
	cmd, err := HandleSystemdCreate(quadctl, quadlets)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmd...)

	// Stop if already running (podman ps -a only returns a list if systemd services are running. Once stopped, it returns empty.)
	if info, err := getContainerPS(quadctl.Runner, quadlets); err == nil && len(info) > 0 {
		cmd, err := HandleSystemdStop(quadctl, quadlets, false)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd...)
	}

	// Start the systemd services
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)

	if err = quadctl.SystemdStartTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd start template: %w", err)
	}

	// Only start the pod and any loose containers
	for _, q := range quadlets {
		if (q.Type == ".container" && q.ParentPod == "") || q.Type == ".pod" || q.Type == ".kube" {
			args := util.ParseFields(buf.String())
			args = append(args, q.ServiceName)
			cmd := NewCommand(fmt.Sprintf("Starting %s %s", q.Type, q.ID))
			cmd.Cmd = args
			commands = append(commands, cmd)
		}

		// For networks and volumes, we rely on the fact that systemd will start them automatically when the containers that depend on them are started.
	}
	return commands, nil
}

func HandleSystemdStop(quadctl *util.Quadctl, quadlets []*util.Quadlet, stopNetAndVol bool) ([]Command, error) {

	commands := []Command{}

	// Stop the systemd services
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.SystemdStopTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd stop template: %w", err)
	}

	for _, q := range quadlets {
		var args []string
		// Stop a container directly only if it is not part of a pod.
		if (q.Type == ".container" && q.ParentPod == "") || q.Type == ".pod" || q.Type == ".kube" {
			// Stop the pod and any related containers.
			args = util.ParseFields(buf.String())
			args = append(args, q.ServiceName)
		} else {
			// Stop network and volume services (Only used when called by handleUninstall. Ensures cleanup of volumes and networks).
			if stopNetAndVol && (q.Type == ".network" || q.Type == ".volume") {
				args = util.ParseFields(buf.String())
				args = append(args, q.ServiceName)
			}
		}
		if len(args) == 0 {
			continue
		}
		cmd := NewCommand(fmt.Sprintf("Systemd stopping %s %s", q.Type, q.ID))
		cmd.Cmd = args
		commands = append(commands, cmd)
	}
	return commands, nil
}

func HandleSystemdStatus(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {

	if quadctl.IsLongStatus {
		commands := []Command{}

		var buf bytes.Buffer
		data := systemdTemplateData(quadctl)
		if err := quadctl.SystemdStatusTmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("executing systemd status template: %w", err)
		}
		args := util.ParseFields(buf.String())
		for _, q := range quadlets {
			args = append(args, q.ServiceName)
		}
		if quadctl.IsPrintOnly {
			c := NewCommand("Getting systemd status")
			c.Cmd = args
			commands = append(commands, c)
		} else {
			runCommand(quadctl.Runner, args)
		}
		return commands, nil
	} else {
		if err := displayListOfSystemdInstalledQuadlets(quadctl, quadlets); err != nil {
			return nil, err
		}
		return []Command{}, nil
	}
}

func HandleSystemdLogs(quadctl *util.Quadctl, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	// Only .container and .kube quadlets run a process whose logs are worth tailing.
	var serviceQuadlets []*util.Quadlet
	for _, q := range quadlets {
		if q.Type == ".container" || q.Type == ".kube" {
			serviceQuadlets = append(serviceQuadlets, q)
		}
	}

	var serviceName string
	if len(serviceQuadlets) == 1 {
		serviceName = serviceQuadlets[0].ServiceName
	} else if len(serviceQuadlets) > 1 {
		names := []string{}
		for _, q := range serviceQuadlets {
			names = append(names, q.ServiceName)
		}
		selected, err := util.SelectFromList(names)
		if err != nil {
			return nil, fmt.Errorf("selecting service: %w", err)
		}
		serviceName = selected
	}

	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.SystemdLogsTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd logs template: %w", err)
	}

	cmd := util.ParseFields(buf.String())
	if serviceName != "" {
		cmd = append(cmd, "-u", serviceName)
	}
	if quadctl.IsPrintOnly {
		c := NewCommand("Opening systemd logs")
		c.Cmd = cmd
		commands = append(commands, c)
	} else {
		runCommand(quadctl.Runner, cmd)
	}
	return commands, nil
}

func HandleSystemdReload(quadctl *util.Quadctl) ([]Command, error) {
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.SystemdReloadTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd reload template: %w", err)
	}
	command := util.ParseFields(buf.String())
	cmd := NewCommand("Reloading systemd")
	cmd.Cmd = command
	return []Command{cmd}, nil
}

// systemdTemplateData builds the data passed to the configurable systemd command templates.
// The "user" key is always present, empty when rootful: text/template renders a missing map
// key as the literal "<no value>".
func systemdTemplateData(quadctl *util.Quadctl) map[string]string {
	user := ""
	if !quadctl.IsRootful {
		user = "--user"
	}
	return map[string]string{"user": user}
}

func displayListOfSystemdInstalledQuadlets(quadctl *util.Quadctl, quadlets []*util.Quadlet) error {
	/*
		//podman quadlet list --format "{{.Name}}|{{.UnitName}}|{{.Path}}|{{.Status}}\n"
		cmd := []string{"podman", "quadlet", "list", "--format", "{{.Name}}|{{.UnitName}}|{{.Path}}|{{.Status}}"}
		output, err := runCommandCapture(quadctl.Runner, cmd)
		if err != nil {
			return err
		}
		info := [][]string{}
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			parts := strings.Split(line, "|")
			if len(parts) < 4 {
				continue
			}
			info = append(info, parts)
		}
	*/
	info, err := listSystemdInstalledQuadlets(quadctl, quadlets)
	if err != nil {
		return err
	}
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"NAME", "PATH", "UNIT NAME", "STATUS"})
	for _, quadletInfo := range info {
		if len(quadletInfo) >= 4 {
			t.AppendRow(table.Row{
				strings.TrimSpace(quadletInfo[0]),
				strings.TrimSpace(quadletInfo[1]),
				strings.TrimSpace(quadletInfo[2]),
				strings.TrimSpace(quadletInfo[3]),
			})
		}
	}
	t.SetStyle(table.StyleColoredYellowWhiteOnBlack)
	t.Render()
	return nil
}
