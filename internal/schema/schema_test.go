package schema

import "testing"

// TestSchemaIsWellFormed runs the schema's own consistency check over every option of every
// type. ValidateSchema used to be reachable only through a parallel Schema wrapper that
// nothing called, so the check existed and never ran (TODO.md section 5).
func TestSchemaIsWellFormed(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options []SchemaOption
	}{
		{"container", GetContainerOptions()},
		{"pod", GetPodOptions()},
		{"network", GetNetworkOptions()},
		{"volume", GetVolumeOptions()},
		{"kube", GetKubeOptions()},
		{"image", GetImageOptions()},
		{"build", GetBuildOptions()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.options) == 0 {
				t.Fatal("no options at all")
			}
			if err := ValidateSchema(tt.options); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestQuadletOptionsIndexesEveryType covers TODO.md section 1: "kube" was missing from the
// index, so every [Kube] key quadctl knows about was dropped with a warning only -v showed.
func TestQuadletOptionsIndexesEveryType(t *testing.T) {
	all := AllQuadletOptions()
	for _, quadletType := range []string{"container", "pod", "network", "volume", "kube"} {
		options, ok := all[quadletType]
		if !ok || len(options) == 0 {
			t.Errorf("AllQuadletOptions has no options for %q", quadletType)
		}
	}

	// The keys that used to vanish, and their podman spellings.
	kube := all["kube"]
	for _, key := range []string{"ConfigMap", "PublishPort", "Network", "UserNS", "LogDriver", "ExitCodePropagation", "SetWorkingDirectory"} {
		opt, ok := kube[key]
		if !ok {
			t.Errorf("[Kube] %s= is not in the schema", key)
			continue
		}
		// The loop that pre-parses the templates used to assign to the loop copy, leaving
		// these nil for every kube option and panicking as soon as one was rendered.
		if opt.PodmanTemplateParsed == nil || opt.QuadletTemplateParsed == nil {
			t.Errorf("[Kube] %s= has unparsed templates", key)
		}
	}
}
