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
// "PublishPort"). It returns nil for ".kube", whose contents come from a Kubernetes YAML
// rather than from options quadctl translates, and for any type the schema does not describe.
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
	default:
		return nil
	}

	byKey := make(map[string]SchemaOption, len(options))
	for _, option := range options {
		byKey[option.QuadletKey] = option
	}
	return byKey
}
