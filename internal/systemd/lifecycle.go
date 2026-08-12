package systemd

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/fkmiec/quadctl/internal/command"
	"github.com/fkmiec/quadctl/internal/podman"
	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
	"github.com/fkmiec/quadctl/internal/tui"
	"github.com/jedib0t/go-pretty/v6/table"
)

func HandleStart(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]command.Command, error) {

	commands := []command.Command{}

	// Always (re)install the quadlet definitions, whether or not they're already
	// installed, so that edits to the source files are picked up on every start.
	// CopyFile/CopyDir overwrite existing files in place, so this is a no-op cost
	// wise when nothing has changed. HandleCreate also reloads systemd
	// after copying so the generator picks up any changes.
	cmd, err := HandleCreate(quadctl, quadlets)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmd...)

	// Stop if already running (podman ps -a only returns a list if systemd services are running. Once stopped, it returns empty.)
	if info, err := podman.ContainerPS(quadctl.Runner, quadlets); err == nil && len(info) > 0 {
		cmd, err := HandleStop(quadctl, quadlets, false)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd...)
	}

	// Start the systemd services
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)

	if err = quadctl.Config.SystemdStartTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd start template: %w", err)
	}

	// Only start the pod and any loose containers
	for _, q := range quadlets {
		if (q.Type == ".container" && q.ParentPod == "") || q.Type == ".pod" || q.Type == ".kube" {
			args := quadlet.ParseFields(buf.String())
			args = append(args, q.ServiceName)
			cmd := command.NewCommand(unitLabel("Starting", q))
			cmd.Cmd = args
			commands = append(commands, cmd)
		}

		// For networks and volumes, we rely on the fact that systemd will start them automatically when the containers that depend on them are started.
	}
	return commands, nil
}

func HandleStop(quadctl *quadlet.State, quadlets []*quadlet.Quadlet, stopNetAndVol bool) ([]command.Command, error) {

	commands := []command.Command{}

	// Stop the systemd services
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.Config.SystemdStopTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd stop template: %w", err)
	}

	for _, q := range quadlets {
		var args []string
		// Stop a container directly only if it is not part of a pod.
		if (q.Type == ".container" && q.ParentPod == "") || q.Type == ".pod" || q.Type == ".kube" {
			// Stop the pod and any related containers.
			args = quadlet.ParseFields(buf.String())
			args = append(args, q.ServiceName)
		} else {
			// Stop network and volume services (Only used when called by handleUninstall. Ensures cleanup of volumes and networks).
			if stopNetAndVol && (q.Type == ".network" || q.Type == ".volume") {
				args = quadlet.ParseFields(buf.String())
				args = append(args, q.ServiceName)
			}
		}
		if len(args) == 0 {
			continue
		}
		cmd := command.NewCommand(unitLabel("Stopping", q))
		cmd.Cmd = args
		commands = append(commands, cmd)
	}
	return commands, nil
}

func HandleStatus(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]command.Command, error) {

	if quadctl.IsLongStatus {
		commands := []command.Command{}

		var buf bytes.Buffer
		data := systemdTemplateData(quadctl)
		if err := quadctl.Config.SystemdStatusTmpl.Execute(&buf, data); err != nil {
			return nil, fmt.Errorf("executing systemd status template: %w", err)
		}
		args := quadlet.ParseFields(buf.String())
		for _, q := range quadlets {
			args = append(args, q.ServiceName)
		}
		if quadctl.IsPrintOnly {
			c := command.NewCommand("Getting systemd status")
			c.Cmd = args
			commands = append(commands, c)
		} else {
			runner.RunStreaming(quadctl.Runner, args)
		}
		return commands, nil
	} else {
		if err := displayListOfSystemdInstalledQuadlets(quadctl, quadlets); err != nil {
			return nil, err
		}
		return []command.Command{}, nil
	}
}

func HandleLogs(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) ([]command.Command, error) {

	commands := []command.Command{}

	// Only .container and .kube quadlets run a process whose logs are worth tailing.
	var serviceQuadlets []*quadlet.Quadlet
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
		selected, err := tui.SelectFromList(names)
		if err != nil {
			return nil, fmt.Errorf("selecting service: %w", err)
		}
		serviceName = selected
	}

	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.Config.SystemdLogsTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd logs template: %w", err)
	}

	cmd := quadlet.ParseFields(buf.String())
	if serviceName != "" {
		cmd = append(cmd, "-u", serviceName)
	}
	if quadctl.IsPrintOnly {
		c := command.NewCommand("Opening systemd logs")
		c.Cmd = cmd
		commands = append(commands, c)
	} else {
		runner.RunStreaming(quadctl.Runner, cmd)
	}
	return commands, nil
}

func HandleReload(quadctl *quadlet.State) ([]command.Command, error) {
	var buf bytes.Buffer
	data := systemdTemplateData(quadctl)
	if err := quadctl.Config.SystemdReloadTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("executing systemd reload template: %w", err)
	}
	argv := quadlet.ParseFields(buf.String())
	cmd := command.NewCommand("Reloading systemd")
	cmd.Cmd = argv
	return []command.Command{cmd}, nil
}

// unitLabel is command.Label for the systemd unit a quadlet generates. It names the unit
// rather than the podman resource, because that is what the systemctl command being run acts
// on, but it reads the same way as every other command's label.
func unitLabel(verb string, q *quadlet.Quadlet) string {
	return command.Label(verb, q.Type, q.ServiceName)
}

// systemdTemplateData builds the data passed to the configurable systemd command templates.
// The "user" key is always present, empty when rootful: text/template renders a missing map
// key as the literal "<no value>".
func systemdTemplateData(quadctl *quadlet.State) map[string]string {
	user := ""
	if !quadctl.IsRootful {
		user = "--user"
	}
	return map[string]string{"user": user}
}

func displayListOfSystemdInstalledQuadlets(quadctl *quadlet.State, quadlets []*quadlet.Quadlet) error {
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
	t.SetStyle(command.TableStyle(quadctl))
	t.Render()
	return nil
}
