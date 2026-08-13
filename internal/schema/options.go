package schema

// The schema is written as one slice of SchemaOption per quadlet type. Callers look options
// up by their quadlet-file key while building commands, so the slices are indexed here.

// AllQuadletOptions indexes every supported quadlet type's options by their quadlet-file key.
// The outer key is the type without its leading dot ("container", "pod", ...), matching what
// a quadlet file's section is called.
func AllQuadletOptions() map[string]map[string]SchemaOption {
	schemas := map[string]map[string]SchemaOption{}
	for _, t := range []string{"volume", "network", "container", "pod", "kube"} {
		schemas[t] = QuadletOptions(t)
	}
	return schemas
}

// QuadletOptions indexes one type's options by their quadlet-file key ("Environment",
// "PublishPort"). It returns nil for any type the schema does not describe.
//
// "kube" belongs here as much as the rest: a .kube quadlet's workload comes from a Kubernetes
// YAML, but its [Kube] section still carries options quadctl has to translate (ConfigMap,
// PublishPort, Network, UserNS, ...). Leaving it out is what made every one of those keys
// disappear with a warning only -v could show (TODO.md section 1).
func QuadletOptions(quadletType string) map[string]SchemaOption {
	var options []SchemaOption
	switch quadletType {
	case "container":
		options = GetContainerOptions()
	case "pod":
		options = GetPodOptions()
	case "network":
		options = GetNetworkOptions()
	case "volume":
		options = GetVolumeOptions()
	case "kube":
		options = GetKubeOptions()
	default:
		return nil
	}

	// Fill in the per-value validators. They cost nothing to carry and they are the half of
	// the model a 'validate' command would read (FEATURES.md); populating them here is what
	// makes them part of the schema rather than a function nothing calls.
	PopulateValidators(options)

	byKey := make(map[string]SchemaOption, len(options))
	for _, option := range options {
		byKey[option.QuadletKey] = option
	}
	return byKey
}
