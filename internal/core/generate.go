package core

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fkmiec/quadctl/internal/util"
)

// generateCreateCommand creates the base 'podman ... create' string.
func generateCreateCommand(quadctl *util.State, q *util.Quadlet) ([]string, []string) {
	var warnings []string
	var cmd []string

	// Warn about ignored sections
	for sec := range q.Sections {
		// standard systemd sections not used in CLI calls
		if sec == "Install" || sec == "Unit" {
			warnings = append(warnings, InfoPrefix+fmt.Sprintf("ignoring [%s] section (systemd only)", sec))
		}
	}

	switch q.Type {
	case ".volume":
		//Get the schema for the volume type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["volume"]
		if !ok {
			warnings = append(warnings, "no volume schema found")
			return cmd, warnings
		}
		cmd = append(cmd, "podman", "volume", "create")
		if volSec, ok := q.Sections["Volume"]; ok {
			cmd = append(cmd, getRawPodmanArgs(volSec)...)
			for _, k := range slices.Sorted(maps.Keys(volSec)) {
				for _, v := range util.OptionValues(options, k, volSec[k]) {
					switch k {
					case "Type":
						continue // Type is not a Podman CLI option
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "VolumeName":
						// Folded into q.ResourceName and appended as the positional name
						// argument below, since 'podman volume create' takes the name last
						// rather than as a flag.
						continue
					case "PodmanArgs": // Handled above
						continue
					default:
						podmanArgs, err := util.QuadletOptionToPodman("volume", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						cmd = append(cmd, podmanArgs...)
					}
				}
			}
		}
		cmd = append(cmd, q.ResourceName)

	case ".network":
		//Get the schema for the network type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["network"]
		if !ok {
			warnings = append(warnings, "no network schema found")
			return cmd, warnings
		}
		cmd = append(cmd, "podman", "network", "create")
		if netSec, ok := q.Sections["Network"]; ok {
			cmd = append(cmd, getRawPodmanArgs(netSec)...)
			for _, k := range slices.Sorted(maps.Keys(netSec)) {
				for _, v := range util.OptionValues(options, k, netSec[k]) {
					switch k {
					case "NetworkName":
						// Folded into q.ResourceName and appended as the positional name
						// argument below.
						continue
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "NetworkDeleteOnStop":
						continue // NetworkDeleteOnStop is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
					default:
						podmanArgs, err := util.QuadletOptionToPodman("network", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						cmd = append(cmd, podmanArgs...)
					}
				}
			}
		}
		cmd = append(cmd, q.ResourceName)

	case ".pod":
		//Get the schema
		options, ok := quadctl.QuadletSchemas["pod"]
		if !ok {
			warnings = append(warnings, "no pod schema found")
			return cmd, warnings
		}

		cmd = append(cmd, "podman", "pod", "create", "--name", q.ResourceName)
		if podSec, ok := q.Sections["Pod"]; ok {
			cmd = append(cmd, getRawPodmanArgs(podSec)...)
			for _, k := range slices.Sorted(maps.Keys(podSec)) {
				for _, v := range util.OptionValues(options, k, podSec[k]) {
					switch k {
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
					case "PodName": // Handled above, as --name
					case "Volume":
						cmd = append(cmd, "--volume", resolveVolumeRef(q, v))
					case "Network":
						cmd = append(cmd, "--network", q.ResolveRef(v))
					default:
						podmanArgs, err := util.QuadletOptionToPodman("pod", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						cmd = append(cmd, podmanArgs...)
					}
				}
			}
		}

	case ".container":
		//Get the schema
		options, ok := quadctl.QuadletSchemas["container"]
		if !ok {
			warnings = append(warnings, "no container schema found")
			return cmd, warnings
		}

		cmd = append(cmd, "podman", "container", "create", "--name", q.ResourceName)

		// Map [Service] Restart= to --restart
		if q.RestartPolicy != "" {
			cmd = append(cmd, "--restart", q.RestartPolicy)
		}

		var image string
		var execCmd []string
		if contSec, ok := q.Sections["Container"]; ok {
			configuredPodmanArgs := getRawPodmanArgs(contSec)

			// Special handling for quadctl run command. It's basically same as create, but allows for specifying podman args and a command to execute.
			if quadctl.PodmanArgs != "" {
				// If PodmanArgs were also provided via CLI, we will append them after the ones from the quadlet file.
				// This allows CLI args to override quadlet args if there are conflicts, since in Podman CLI the last specified flag takes precedence.
				configuredPodmanArgs = append(configuredPodmanArgs, util.ParseFields(quadctl.PodmanArgs)...)
			}
			if quadctl.RunCmd != "" {
				execCmd = util.ParseFields(quadctl.RunCmd)
			}

			cmd = append(cmd, configuredPodmanArgs...)
			for _, k := range slices.Sorted(maps.Keys(contSec)) {
				if _, ok := options[k]; !ok {
					warnings = append(warnings, fmt.Sprintf("no such quadlet container option: %s", k))
					continue
				}
				vals := util.OptionValues(options, k, contSec[k])

				if k == "Exec" {
					// Exec is not a Podman CLI option: it is the command appended after the
					// image. The written value is a command line, so it is split into argv
					// here - quoting survives, which is the whole point of keeping the value
					// intact until now. A --exec flag on the CLI wins over the file.
					if len(execCmd) < 1 {
						execCmd = util.ParseFields(vals[0])
					}
					continue
				}

				for _, v := range vals {
					switch k {
					case "Image":
						image = v
					case "ContainerName": // Handled above, as --name
					case "ReloadCmd":
						continue // ReloadCmd is for systemd and does not affect Podman CLI
					case "ReloadSignal":
						continue // ReloadSignal is for systemd and does not affect Podman CLI
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "StartWithPod":
						continue // StartWithPod is for systemd and does not affect Podman CLI
					case "Volume":
						cmd = append(cmd, "-v", resolveVolumeRef(q, v))
					case "Network":
						cmd = append(cmd, "--network", q.ResolveRef(v))
					case "Pod":
						cmd = append(cmd, "--pod", q.PodResourceName)
					case "PodmanArgs": // Handled above
					default:
						podmanArgs, err := util.QuadletOptionToPodman("container", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						cmd = append(cmd, podmanArgs...)
					}
				}
			}
		}
		if image == "" {
			warnings = append(warnings, "no Image= specified in [Container]")
			image = "<MISSING_IMAGE>"
		}
		cmd = append(cmd, image)
		if len(execCmd) > 0 {
			// If a command to execute is specified for the quadlet, the equivalent podman create command will have it appended at the end.
			cmd = append(cmd, execCmd...)
		}
	}
	return cmd, warnings
}

// generateStartupCommand creates necessary 'start' commands based on existence.
func generateStartupCommand(quadctl *util.State, q *util.Quadlet) ([]string, []string) {
	cmd := []string{}
	warnings := []string{}
	resName := q.ID

	//Kube is a special case. kube play is create and start in one step, so we generate the play command here in "start" phase.
	if q.Type == ".kube" {
		//Get the schema for the kube type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["kube"]
		if !ok {
			warnings = append(warnings, "no kube schema found")
			return cmd, warnings
		}

		cmd = append(cmd, "podman", "play", "kube")
		if kubeSec, ok := q.Sections["Kube"]; ok {
			cmd = append(cmd, getRawPodmanArgs(kubeSec)...)
			for _, k := range slices.Sorted(maps.Keys(kubeSec)) {
				for _, v := range util.OptionValues(options, k, kubeSec[k]) {
					switch k {
					case "Yaml":
						continue // Yaml is parsed ahead of time and is appended at the end as the file argument for podman play kube
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
						continue
					default:
						podmanArgs, err := util.QuadletOptionToPodman("kube", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						cmd = append(cmd, podmanArgs...)
					}
				}
			}
		}
		cmd = append(cmd, q.KubernetesYaml)
		return cmd, warnings
	}

	// Other startable types are pod and container
	if q.ResourceName != "" {
		resName = q.ResourceName
	}

	// 3. Determine if we should start it
	shouldStart := true
	if q.Type == ".container" && q.ParentPod != "" {
		// Prompt: Create start commands ONLY for pods and loose containers
		shouldStart = false
	}

	if shouldStart {
		if q.Type == ".pod" {
			cmd = append(cmd, "podman", "pod", "start", resName)
		} else if q.Type == ".container" {
			cmd = append(cmd, "podman", "container", "start", resName)
		}
	} else if q.Type == ".container" {
		warnings = append(warnings, InfoPrefix+fmt.Sprintf("container %s belongs to pod %s and starts with it", resName, q.ParentPod))
	}

	return cmd, warnings
}

// generateStartupCommand creates necessary 'start' commands based on existence.
func generateRunCommand(quadctl *util.State, q *util.Quadlet) ([]string, []string) {

	if q.Type == ".kube" {
		// For kube type, just reuse the generateStartupCommand for kube types.
		return generateStartupCommand(quadctl, q)
	}

	createCmd, warnings := generateCreateCommand(quadctl, q)
	// generateCreateCommand returns an empty slice when it can't build a command (missing
	// schema, unhandled type), so there's nothing to strip the 'podman container create'
	// prefix from.
	if len(createCmd) < 3 {
		warnings = append(warnings, fmt.Sprintf("could not generate a run command for %s", q.ID))
		return nil, warnings
	}
	runCmd := []string{"podman", "run"}
	runCmd = append(runCmd, createCmd[3:]...) // Replace 'podman container create' with 'podman run'

	return runCmd, warnings
}

func generateStopCommand(quadctl *util.State, q *util.Quadlet) []string {
	cmd := []string{}
	resName := q.ID
	if q.ResourceName != "" {
		resName = q.ResourceName
	}

	switch q.Type {
	case ".kube":
		if quadctl.Config.IsRemoveVolumes || quadctl.Config.IsRemoveNetworks || kubeDownForce(q) {
			cmd = append(cmd, []string{"podman", "play", "kube", "--down", "--force", q.KubernetesYaml}...)
		} else {
			cmd = append(cmd, []string{"podman", "play", "kube", "--down", q.KubernetesYaml}...)
		}
	case ".pod":
		cmd = append(cmd, []string{"podman", "pod", "stop", resName}...)
	case ".container":
		if q.ParentPod == "" {
			// loose container
			cmd = append(cmd, []string{"podman", "stop", resName}...)
		}
	}
	return cmd
}

// kubeDownForce reports whether a .kube quadlet sets KubeDownForce=true. The key is
// optional, so the section and the value slice may both be absent.
func kubeDownForce(q *util.Quadlet) bool {
	return strings.EqualFold(util.LastValue(q.Sections["Kube"], "KubeDownForce"), "true")
}

// resolveVolumeRef rewrites the source half of a Volume= value - everything before the first
// ":" - when it names a .volume quadlet, leaving the container path and options alone. A
// source that is a host path or a pre-existing podman volume is passed through untouched.
func resolveVolumeRef(q *util.Quadlet, val string) string {
	source, rest, hasRest := strings.Cut(val, ":")
	resolved := q.ResolveRef(source)
	if !hasRest {
		return resolved
	}
	return resolved + ":" + rest
}

// getRawPodmanArgs turns the PodmanArgs= lines of a section into argv. Each line is a command
// line fragment, so each is split on whitespace with quoting honored, and repeated lines
// accumulate in the order written.
func getRawPodmanArgs(section map[string][]string) []string {
	var args []string
	for _, argStr := range section["PodmanArgs"] {
		args = append(args, util.ParseFields(argStr)...)
	}
	return args
}
