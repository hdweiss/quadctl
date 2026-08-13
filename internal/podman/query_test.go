package podman

import (
	"strings"
	"testing"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/fkmiec/quadctl/internal/runner"
)

// TestQuadletOwnsContainer covers TODO.md section 2's suffix matching: `podman ps` output was
// attributed to a quadlet whenever the container's name merely ended with the quadlet's, so
// the quadlet `web` claimed an unrelated container called `myweb`.
func TestQuadletOwnsContainer(t *testing.T) {
	web := &quadlet.Quadlet{ID: "web", Type: ".container", ResourceName: "systemd-web"}
	inPod := &quadlet.Quadlet{
		ID: "app", Type: ".container",
		ResourceName: "web-app", ParentPod: "stack", PodResourceName: "stack-pod",
	}
	kube := &quadlet.Quadlet{
		ID: "site", Type: ".kube",
		KubeResources: []map[string]interface{}{
			{"type": "pod", "name": "site-pod"},
			{"type": "container", "name": "nginx", "pod": "site-pod"},
		},
	}
	// A .volume describes nothing that shows up in `podman ps`.
	vol := &quadlet.Quadlet{ID: "data", Type: ".volume", ResourceName: "systemd-data"}

	tests := []struct {
		name      string
		q         *quadlet.Quadlet
		container string
		pod       string
		want      bool
	}{
		{"exact container name", web, "systemd-web", "", true},
		{"unrelated container ending in the same name", web, "my-systemd-web", "", false},
		{"unrelated container starting with the same name", web, "systemd-webhook", "", false},
		{"container in no pod does not match on an empty pod column", web, "other", "", false},
		{"renamed container", inPod, "web-app", "stack-pod", true},
		{"sibling in the same pod", inPod, "stack-pod-infra", "stack-pod", true},
		{"same name in a different pod still matches by name", inPod, "web-app", "other-pod", true},
		{"different pod, different name", inPod, "other", "other-pod", false},
		{"kube container by its played name", kube, "site-pod-nginx", "site-pod", true},
		{"kube infra container by pod", kube, "site-pod-infra", "site-pod", true},
		{"kube container name alone is not enough", kube, "nginx", "", false},
		{"volume owns nothing", vol, "systemd-data", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quadletOwnsContainer(tt.q, tt.container, tt.pod); got != tt.want {
				t.Errorf("quadletOwnsContainer(%s, %q, %q) = %v, want %v",
					tt.q.ID, tt.container, tt.pod, got, tt.want)
			}
		})
	}
}

// TestGetContainerPSFiltersExactly drives the same rule through the podman ps parsing, so the
// column layout is covered too.
func TestGetContainerPSFiltersExactly(t *testing.T) {
	// The ps format is id|name|pod|status|ports|image|created; these rows are in no pod.
	runner := &runner.RecordingRunner{
		Fallback: runner.RunResult{Output: strings.Join([]string{
			"abc123|systemd-web||Up 2 minutes|80/tcp|nginx|2026-08-12 09:00:00 +0000 UTC",
			"def456|myweb||Up 2 minutes|80/tcp|nginx|2026-08-12 09:00:00 +0000 UTC",
		}, "\n")},
	}

	quadlets := []*quadlet.Quadlet{{ID: "web", Type: ".container", ResourceName: "systemd-web"}}
	info, err := ContainerPS(runner, quadlets)
	if err != nil {
		t.Fatalf("getContainerPS: %v", err)
	}
	if len(info) != 1 || strings.TrimSpace(info[0][1]) != "systemd-web" {
		t.Errorf("matched %d row(s) %v, want just systemd-web", len(info), info)
	}
}

// TestAnyRunning covers TODO.md section 2: 'start' decided whether the stack was already up
// from the first ps row alone, and the systemd path from the row count alone.
func TestAnyRunning(t *testing.T) {
	row := func(name, status string) []string {
		return []string{"id", name, "", status, "", "image", "created"}
	}

	tests := []struct {
		name string
		rows [][]string
		want bool
	}{
		{"nothing at all", nil, false},
		{"all exited", [][]string{row("a", "Exited (0) 2 hours ago"), row("b", "Created")}, false},
		{"running, listed first", [][]string{row("a", "Up 3 minutes"), row("b", "Created")}, true},
		{"running, listed last", [][]string{row("a", "Exited (0) 2 hours ago"), row("b", "Up 3 minutes")}, true},
		{"running and healthy", [][]string{row("a", "Up 2 hours (healthy)")}, true},
		{"short row", [][]string{{"id", "a"}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AnyRunning(tt.rows); got != tt.want {
				t.Errorf("AnyRunning(%v) = %v, want %v", tt.rows, got, tt.want)
			}
		})
	}
}
