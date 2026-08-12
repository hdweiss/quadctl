package core

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/fkmiec/quadctl/internal/util"
)

// generateCreateCommand creates the base 'podman ... create' string.
func generateCreateCommand(quadctl *util.Quadctl, q *util.Quadlet) ([]string, []string) {
	var warnings []string
	var cmd []string

	// Warn about ignored sections
	for sec := range q.Sections {
		// standard systemd sections not used in CLI calls
		if sec == "Install" || sec == "Unit" {
			warnings = append(warnings, fmt.Sprintf("Ignoring [%s] section (Systemd specific)", sec))
		}
	}

	switch q.Type {
	case ".volume":
		//Get the schema for the volume type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["volume"]
		if !ok {
			warnings = append(warnings, "No volume schema found.")
			return cmd, warnings
		}
		cmd = append(cmd, "podman", "volume", "create")
		if volSec, ok := q.Sections["Volume"]; ok {
			cmd = append(cmd, getRawPodmanArgs(volSec)...)
			for _, k := range slices.Sorted(maps.Keys(volSec)) {
				vals := volSec[k]
				for _, v := range vals {
					switch k {
					case "Type":
						continue // Type is not a Podman CLI option
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "VolumeName":
						//cmd = append(cmd, "--name", v) // Not sure this is valid. May need to hold the value and append at the end after processing all options to avoid ordering issues with Podman CLI
						// The volume name is specified by the ID and added at the end of the command
						continue
					case "PodmanArgs": // Handled above
						continue
					default:
						podmanOpt, err := util.QuadletOptionToPodman("volume", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						// Use Fields to parse space-separated flags
						cmd = append(cmd, util.ParseFields(podmanOpt)...)
					}
				}
			}
		}
		cmd = append(cmd, q.ID)

	case ".network":
		//Get the schema for the network type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["network"]
		if !ok {
			warnings = append(warnings, "No network schema found.")
			return cmd, warnings
		}
		cmd = append(cmd, "podman", "network", "create")
		if netSec, ok := q.Sections["Network"]; ok {
			cmd = append(cmd, getRawPodmanArgs(netSec)...)
			for _, k := range slices.Sorted(maps.Keys(netSec)) {
				vals := netSec[k]
				for _, v := range vals {
					switch k {
					case "NetworkName":
						continue // NetworkName is for systemd and does not affect Podman CLI
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "NetworkDeleteOnStop":
						continue // NetworkDeleteOnStop is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
					default:
						podmanOpt, err := util.QuadletOptionToPodman("network", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						// Use Fields to parse space-separated flags
						cmd = append(cmd, util.ParseFields(podmanOpt)...)
					}
				}
			}
		}
		cmd = append(cmd, q.ID)

	case ".pod":
		//Get the schema
		options, ok := quadctl.QuadletSchemas["pod"]
		if !ok {
			warnings = append(warnings, "No pod schema found.")
			return cmd, warnings
		}

		podName := q.ID
		if name, ok := q.GeneratedNames["pod_name"]; ok {
			podName = name
		}
		cmd = append(cmd, "podman", "pod", "create", "--name", podName)
		if podSec, ok := q.Sections["Pod"]; ok {
			cmd = append(cmd, getRawPodmanArgs(podSec)...)
			for _, k := range slices.Sorted(maps.Keys(podSec)) {
				vals := podSec[k]
				for _, v := range vals {
					switch k {
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
					case "PodName": // Handled above
					default:
						podmanOpt, err := util.QuadletOptionToPodman("pod", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						// Use Fields to parse space-separated flags
						cmd = append(cmd, util.ParseFields(podmanOpt)...)
					}
				}
			}
		}

	case ".container":
		//Get the schema
		options, ok := quadctl.QuadletSchemas["container"]
		if !ok {
			warnings = append(warnings, "No container schema found.")
			return cmd, warnings
		}

		resName := q.GeneratedNames["container"]
		cmd = append(cmd, "podman", "container", "create", "--name", resName)

		// Map [Service] Restart= to --restart
		if q.RestartPolicy != "" {
			cmd = append(cmd, "--restart", q.RestartPolicy)
		}

		// Map [Container] AutoUpdate= to label
		//if q.GeneratedNames["auto_update"] != "" {
		//	cmd = append(cmd, "--label", "io.containers.autoupdate="+q.GeneratedNames["auto_update"])
		//}

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
				execCmd = strings.Split(quadctl.RunCmd, " ")
			}

			cmd = append(cmd, configuredPodmanArgs...)
			for _, k := range slices.Sorted(maps.Keys(contSec)) {
				vals := contSec[k]
				opt, ok := options[k]
				if !ok {
					warnings = append(warnings, fmt.Sprintf("Quadlet container option not defined: %s", k))
					continue
				}
				// Check if multiple values and not supported
				if !opt.AllowMultiple && len(vals) > 1 {
					// Values are tokenized on whitespace at parse time, so any single-valued
					// option whose value contains a space lands here and is dropped. Shown by
					// default until the value model is reworked - being ignored silently is
					// how this reads as "quadctl ran my container with the wrong command".
					warnings = append(warnings, fmt.Sprintf("%sOption %s=%s was ignored: it does not accept multiple space-separated values", WarnPrefix, k, strings.Join(vals, " ")))
					continue
				}

				if k == "Exec" {
					// Exec is a special case since it's not a Podman CLI option. Append command and args to the end of the create command.
					// Ignore quadlet file Exec option if --exec flag was passed on the CLI
					if len(execCmd) < 1 {
						execCmd = append(execCmd, strings.Split(vals[0], " ")...)
					}
					continue
				}

				for _, v := range vals {
					switch k {
					case "Image":
						image = v
					case "ReloadCmd":
						continue // ReloadCmd is for systemd and does not affect Podman CLI
					case "ReloadSignal":
						continue // ReloadSignal is for systemd and does not affect Podman CLI
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "StartWithPod":
						continue // StartWithPod is for systemd and does not affect Podman CLI
					case "Volume":
						volSource := strings.Split(v, ":")[0]
						cleanVol := strings.TrimSuffix(volSource, ".volume")
						mapped := strings.Replace(v, volSource, cleanVol, 1)
						cmd = append(cmd, "-v", mapped)
					case "Network":
						cmd = append(cmd, "--network", strings.TrimSuffix(v, ".network"))
					case "PodmanArgs": // Handled above
					default:
						if k == "Pod" {
							v = strings.TrimSuffix(v, ".pod")
							if podName, ok := q.GeneratedNames["pod_name"]; ok {
								v = podName
							}
						}

						podmanOpt, err := util.QuadletOptionToPodman("container", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						// Use Fields to parse space-separated flags
						cmd = append(cmd, util.ParseFields(podmanOpt)...)
					}
				}
			}
		}
		if image == "" {
			warnings = append(warnings, "No Image= specified in [Container]")
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
func generateStartupCommand(quadctl *util.Quadctl, q *util.Quadlet) ([]string, []string) {
	cmd := []string{}
	warnings := []string{}
	resName := q.ID

	//Kube is a special case. kube play is create and start in one step, so we generate the play command here in "start" phase.
	if q.Type == ".kube" {
		//Get the schema for the kube type and use the PodmanTemplateParsed to format the podman option.
		options, ok := quadctl.QuadletSchemas["kube"]
		if !ok {
			warnings = append(warnings, "No kube schema found.")
			return cmd, warnings
		}

		cmd = append(cmd, "podman", "play", "kube")
		if kubeSec, ok := q.Sections["Kube"]; ok {
			cmd = append(cmd, getRawPodmanArgs(kubeSec)...)
			for _, k := range slices.Sorted(maps.Keys(kubeSec)) {
				vals := kubeSec[k]
				for _, v := range vals {
					switch k {
					case "Yaml":
						continue // Yaml is parsed ahead of time and is appended at the end as the file argument for podman play kube
					case "ServiceName":
						continue // ServiceName is for systemd and does not affect Podman CLI
					case "PodmanArgs": // Handled above
						continue
					default:
						podmanOpt, err := util.QuadletOptionToPodman("kube", options, k, v)
						if err != nil {
							warnings = append(warnings, err.Error())
							continue
						}
						// Use Fields to parse space-separated flags
						cmd = append(cmd, util.ParseFields(podmanOpt)...)
					}
				}
			}
		}
		cmd = append(cmd, q.KubernetesYaml)
		return cmd, warnings
	}

	// Other startable types are pod and container
	if q.Type == ".container" {
		resName = q.GeneratedNames["container"]
	} else if q.Type == ".pod" {
		resName = q.GeneratedNames["pod_name"]
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
		warnings = append(warnings, fmt.Sprintf(" [INFO] Container %s belongs to pod %s, it will start with the pod.\n", resName, q.ParentPod))
	}

	return cmd, warnings
}

// generateStartupCommand creates necessary 'start' commands based on existence.
func generateRunCommand(quadctl *util.Quadctl, q *util.Quadlet) ([]string, []string) {

	if q.Type == ".kube" {
		// For kube type, just reuse the generateStartupCommand for kube types.
		return generateStartupCommand(quadctl, q)
	}

	createCmd, warnings := generateCreateCommand(quadctl, q)
	// generateCreateCommand returns an empty slice when it can't build a command (missing
	// schema, unhandled type), so there's nothing to strip the 'podman container create'
	// prefix from.
	if len(createCmd) < 3 {
		warnings = append(warnings, fmt.Sprintf("%sCould not generate a run command for %s", WarnPrefix, q.ID))
		return nil, warnings
	}
	runCmd := []string{"podman", "run"}
	runCmd = append(runCmd, createCmd[3:]...) // Replace 'podman container create' with 'podman run'

	return runCmd, warnings
}

func generateStopCommand(quadctl *util.Quadctl, q *util.Quadlet) []string {
	cmd := []string{}
	resName := q.ID
	if q.Type == ".container" {
		resName = q.GeneratedNames["container"]
	} else if q.Type == ".pod" {
		resName = q.GeneratedNames["pod_name"]
	}

	switch q.Type {
	case ".kube":
		if quadctl.IsRemoveVolumes || quadctl.IsRemoveNetworks || kubeDownForce(q) {
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
	vals := q.Sections["Kube"]["KubeDownForce"]
	return len(vals) > 0 && strings.EqualFold(vals[0], "true")
}

// Helper: Get raw PodmanArgs securely
func getRawPodmanArgs(section map[string][]string) []string {
	var args []string
	for _, argStr := range section["PodmanArgs"] {
		// Use Fields to parse space-separated flags
		args = append(args, util.ParseFields(argStr)...)
	}
	return args
}
