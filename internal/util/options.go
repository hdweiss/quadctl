package util

import (
	"github.com/fkmiec/quadctl/internal/schema"
)

func GetQuadletSchemas() map[string]map[string]schema.SchemaOption {
	//Get the schemas for each supported type
	schemas := map[string]map[string]schema.SchemaOption{}
	schemas["volume"] = GetQuadletOptionsMap("volume")
	schemas["network"] = GetQuadletOptionsMap("network")
	schemas["container"] = GetQuadletOptionsMap("container")
	schemas["pod"] = GetQuadletOptionsMap("pod")
	schemas["kube"] = GetQuadletOptionsMap("kube")
	return schemas
}

func GetQuadletOptionsMap(quadletType string) map[string]schema.SchemaOption {
	var options []schema.SchemaOption
	switch quadletType {
	case "container":
		options = schema.GetContainerOptions()
	case "pod":
		options = schema.GetPodOptions()
	case "network":
		options = schema.GetNetworkOptions()
	case "volume":
		options = schema.GetVolumeOptions()
	default:
		return nil
	}
	if options == nil {
		return nil
	}
	optionsMap := assembleQuadletOptionsMap(options)
	return optionsMap
}

func GetPodmanOptionsMap(quadletType string) map[string]schema.SchemaOption {
	var options []schema.SchemaOption
	switch quadletType {
	case "container":
		options = schema.GetContainerOptions()
	case "pod":
		options = schema.GetPodOptions()
	case "network":
		options = schema.GetNetworkOptions()
	case "volume":
		options = schema.GetVolumeOptions()
	default:
		return nil
	}
	if options == nil {
		return nil
	}
	optionsMap := assemblePodmanOptionsMap(options)
	return optionsMap
}

func assembleQuadletOptionsMap(options []schema.SchemaOption) map[string]schema.SchemaOption {
	optionsMap := make(map[string]schema.SchemaOption)
	for _, option := range options {
		optionsMap[option.QuadletKey] = option
	}
	return optionsMap
}

func assemblePodmanOptionsMap(options []schema.SchemaOption) map[string]schema.SchemaOption {
	optionsMap := make(map[string]schema.SchemaOption)
	for _, option := range options {
		optionsMap[option.PodmanKey] = option
	}
	return optionsMap
}
