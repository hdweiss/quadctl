package quadlet

import (
	"github.com/fkmiec/quadctl/internal/schema"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtures = "testdata/webstack"

func TestParseQuadlet(t *testing.T) {
	q, err := ParseQuadletFile(filepath.Join(fixtures, "app.container"))
	if err != nil {
		t.Fatalf("ParseQuadletFile: %v", err)
	}

	if q.ID != "app" {
		t.Errorf("ID = %q, want %q", q.ID, "app")
	}
	if q.Type != ".container" {
		t.Errorf("Type = %q, want %q", q.Type, ".container")
	}
	// ContainerName= wins over the default systemd-<id>.
	if q.ResourceName != "web-app" {
		t.Errorf("ResourceName = %q, want %q", q.ResourceName, "web-app")
	}
	if q.ParentPod != "stack" {
		t.Errorf("ParentPod = %q, want %q", q.ParentPod, "stack")
	}
	if q.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %q, want %q", q.RestartPolicy, "always")
	}
	// No ServiceName= on a .container: the unit is named after the file.
	if q.ServiceName != "app" {
		t.Errorf("ServiceName = %q, want %q", q.ServiceName, "app")
	}
	if q.AutoUpdate != "registry" {
		t.Errorf("AutoUpdate = %q, want %q", q.AutoUpdate, "registry")
	}

	// The drop-in directory is merged into the same section.
	env := q.Sections["Container"]["Environment"]
	if !slices.Contains(env, "EXTRA=1") {
		t.Errorf("drop-in not merged: Environment = %q", env)
	}
}

func TestParseQuadletServiceNames(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"db.container", "database"},   // explicit ServiceName=
		{"app.container", "app"},       // container defaults to the bare id
		{"stack.pod", "stack-pod"},     // everything else gets -<type>
		{"data.volume", "data-volume"}, //
		{"front.network", "front-network"},
	}

	for _, tt := range tests {
		q, err := ParseQuadletFile(filepath.Join(fixtures, tt.file))
		if err != nil {
			t.Fatalf("%s: %v", tt.file, err)
		}
		if q.ServiceName != tt.want {
			t.Errorf("%s: ServiceName = %q, want %q", tt.file, q.ServiceName, tt.want)
		}
	}
}

// TestResolveResourceNames pins the rule PLAN.md Phase 4 settled on: the explicit
// ContainerName=/PodName=/VolumeName=/NetworkName= where the file gives one, quadlet's own
// systemd-<id> where it doesn't. It has to hold on both execution paths, because under -s
// the generator picks the names and quadctl only gets to agree with it.
func TestResolveResourceNames(t *testing.T) {
	tests := []struct {
		file string
		want string
	}{
		{"app.container", "web-app"},   // ContainerName=
		{"db.container", "systemd-db"}, // no name given: quadlet's default
		{"stack.pod", "stack-pod"},     // PodName=
		{"data.volume", "data"},        // VolumeName=
		{"front.network", "front"},     // NetworkName=
	}

	for _, tt := range tests {
		q, err := ParseQuadletFile(filepath.Join(fixtures, tt.file))
		if err != nil {
			t.Fatalf("%s: %v", tt.file, err)
		}
		if q.ResourceName != tt.want {
			t.Errorf("%s: ResourceName = %q, want %q", tt.file, q.ResourceName, tt.want)
		}
	}
}

// TestResolveRefs covers the other half of the naming rule: a Volume=/Network=/Pod= value
// naming another quadlet has to resolve to that quadlet's resource name, or the container
// mounts "data" while the volume was created as "systemd-data". Values that name no quadlet -
// a bind mount, a network mode - are passed through untouched.
func TestResolveRefs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "cache.volume"), "[Volume]\nDriver=local\n")
	write(t, filepath.Join(dir, "named.volume"), "[Volume]\nVolumeName=shared-cache\n")
	write(t, filepath.Join(dir, "back.network"), "[Network]\nDriver=bridge\n")
	write(t, filepath.Join(dir, "group.pod"), "[Pod]\n")
	write(t, filepath.Join(dir, "app.container"), strings.Join([]string{
		"[Container]",
		"Image=alpine",
		"Pod=group.pod",
		"Network=back.network",
		"Volume=cache.volume:/var/cache",
		"Volume=named.volume:/srv/shared",
		"Volume=/etc/localtime:/etc/localtime:ro",
		"",
	}, "\n"))

	quadlets, err := InitQuadlets(&State{SearchDir: dir})
	if err != nil {
		t.Fatalf("InitQuadlets: %v", err)
	}
	var app *Quadlet
	for _, q := range quadlets {
		if q.ID == "app" {
			app = q
		}
	}
	if app == nil {
		t.Fatal("app.container missing from the parsed set")
	}

	if app.PodResourceName != "systemd-group" {
		t.Errorf("PodResourceName = %q, want %q", app.PodResourceName, "systemd-group")
	}
	for ref, want := range map[string]string{
		"back.network":   "systemd-back",   // sibling with no explicit name
		"cache.volume":   "systemd-cache",  //
		"named.volume":   "shared-cache",   // sibling with VolumeName=
		"/etc/localtime": "/etc/localtime", // a host path, not a reference
	} {
		if got := app.ResolveRef(ref); got != want {
			t.Errorf("ResolveRef(%q) = %q, want %q", ref, got, want)
		}
	}
}

// TestParseIniFileQuotedValue follows a quoted value containing a space from the file to the
// argument a container would actually receive. Under the old value model the parser split it
// on whitespace and the generator then dropped the option entirely (TODO.md section 1);
// PLAN.md 3.1 made the line survive parsing intact and become one argument at use time.
func TestParseIniFileQuotedValue(t *testing.T) {
	q, err := ParseQuadletFile(filepath.Join(fixtures, "app.container"))
	if err != nil {
		t.Fatal(err)
	}

	// The parser records the line as written, quotes and all.
	env := q.Sections["Container"]["Environment"]
	if !slices.Contains(env, `GREETING="hello world"`) {
		t.Errorf("quoted value did not survive parsing as one value: %q", env)
	}

	// The schema turns it into one value, with the quotes resolved.
	values := OptionValues(schema.QuadletOptions("container"), "Environment", env)
	if !slices.Contains(values, "GREETING=hello world") {
		t.Errorf("quoted value did not resolve to a single argument: %q", values)
	}
}

func TestDiscoverAndParseQuadletsOrdersDependenciesFirst(t *testing.T) {
	quadctl := &State{SearchDir: fixtures}
	ordered, err := InitQuadlets(quadctl)
	if err != nil {
		t.Fatalf("InitQuadlets: %v", err)
	}

	pos := map[string]int{}
	for i, q := range ordered {
		pos[q.ID] = i
	}
	for _, id := range []string{"app", "db", "stack", "data", "front"} {
		if _, ok := pos[id]; !ok {
			t.Fatalf("quadlet %q missing from %v", id, pos)
		}
	}

	// app declares Pod=, Volume=, Network= and After=db, so all four must precede it.
	for _, dep := range []string{"stack", "data", "front", "db"} {
		if pos[dep] > pos["app"] {
			t.Errorf("%s (%d) should be ordered before app (%d)", dep, pos[dep], pos["app"])
		}
	}
}

// TestInitQuadletsIsDeterministic is the regression test for PLAN.md 1.3: map iteration
// order used to reshuffle the result on every call.
func TestInitQuadletsIsDeterministic(t *testing.T) {
	var first []string
	for i := range 20 {
		quadctl := &State{SearchDir: fixtures}
		ordered, err := InitQuadlets(quadctl)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(ordered))
		for _, q := range ordered {
			ids = append(ids, q.ID)
		}
		if i == 0 {
			first = ids
			continue
		}
		if !slices.Equal(ids, first) {
			t.Fatalf("run %d ordered quadlets as %v, run 0 gave %v", i, ids, first)
		}
	}
}

// TestInitQuadletsFileFilter covers the Phase 0.2 fix: -f selects the named quadlet and its
// dependencies, and names an error rather than silently running against everything.
func TestInitQuadletsFileFilter(t *testing.T) {
	t.Run("selects the file and its dependencies", func(t *testing.T) {
		quadctl := &State{SearchDir: fixtures, IsFile: true, PathArg: "db.container"}
		ordered, err := InitQuadlets(quadctl)
		if err != nil {
			t.Fatal(err)
		}
		if len(ordered) != 1 || ordered[0].ID != "db" {
			t.Errorf("got %d quadlets %v, want just db", len(ordered), idsOf(ordered))
		}
	})

	t.Run("unknown file is an error", func(t *testing.T) {
		quadctl := &State{SearchDir: fixtures, IsFile: true, PathArg: "nope.container"}
		if _, err := InitQuadlets(quadctl); err == nil {
			t.Error("expected an error for a file that isn't among the parsed quadlets")
		}
	})

	t.Run("missing path argument is an error", func(t *testing.T) {
		quadctl := &State{SearchDir: fixtures, IsFile: true}
		if _, err := InitQuadlets(quadctl); err == nil {
			t.Error("expected an error when -f is given without a path")
		}
	})
}

// TestExtractDependenciesMissingPod covers a Phase 0.3 panic: a Pod= naming a file that
// isn't there used to dereference a nil *Quadlet.
func TestExtractDependenciesMissingPod(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "orphan.container"), "[Container]\nImage=alpine\nPod=missing.pod\n")

	quadctl := &State{SearchDir: dir}
	_, err := InitQuadlets(quadctl)
	if err == nil {
		t.Fatal("expected an error for a Pod= that names no quadlet in the directory")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the missing quadlet, got: %v", err)
	}
}

func idsOf(quadlets []*Quadlet) []string {
	ids := make([]string, 0, len(quadlets))
	for _, q := range quadlets {
		ids = append(ids, q.ID)
	}
	return ids
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestParseDotQuadlets covers the single-file bundle format: each section between "---"
// separators is extracted into the file its "# FileName=" marker names.
func TestParseDotQuadlets(t *testing.T) {
	tempDir := t.TempDir()
	if err := parseDotQuadlets(filepath.Join("testdata", "dotquadlets", "bundle.quadlets"), tempDir); err != nil {
		t.Fatalf("parseDotQuadlets: %v", err)
	}

	for name, wantKey := range map[string]string{
		"cache.container":  "ContainerName",
		"cachedata.volume": "VolumeName",
	} {
		q, err := ParseQuadletFile(filepath.Join(tempDir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		section := strings.TrimPrefix(q.Type, ".")
		section = strings.ToUpper(section[:1]) + section[1:]
		if len(q.Sections[section][wantKey]) == 0 {
			t.Errorf("%s: expected [%s] %s= to survive extraction, got %v", name, section, wantKey, q.Sections)
		}
	}
}

// TestReadK8sYamlMultiDocument covers TODO.md section 2: a Kubernetes YAML is usually several
// documents, and only the first one used to be read. Every kind: Pod contributes its pod and
// its containers; anything else in the file is podman's business.
func TestReadK8sYamlMultiDocument(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "web.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
---
apiVersion: v1
kind: Pod
metadata:
  name: web-pod
spec:
  containers:
    - name: frontend
      image: nginx:latest
---
# A comment-only document, which is not a resource.
---
apiVersion: v1
kind: Pod
metadata:
  name: worker-pod
spec:
  containers:
    - name: worker
      image: alpine:3.20
`)

	resources, err := readK8sYaml(filepath.Join(dir, "web.yaml"))
	if err != nil {
		t.Fatalf("readK8sYaml: %v", err)
	}

	var pods, containers []string
	for _, res := range resources {
		name, _ := res["name"].(string)
		switch res["type"] {
		case "pod":
			pods = append(pods, name)
		case "container":
			containers = append(containers, name)
		}
	}
	if want := []string{"web-pod", "worker-pod"}; !slices.Equal(pods, want) {
		t.Errorf("pods = %q, want %q", pods, want)
	}
	if want := []string{"frontend", "worker"}; !slices.Equal(containers, want) {
		t.Errorf("containers = %q, want %q", containers, want)
	}
}

// TestReadK8sYamlErrors covers the two failure modes that used to be silent or confusing: an
// unreadable file (the read error was discarded) and a file with no pod in it.
func TestReadK8sYamlErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := readK8sYaml(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("expected an error for a YAML file that cannot be read")
	}

	write(t, filepath.Join(dir, "svc.yaml"), "apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n")
	_, err := readK8sYaml(filepath.Join(dir, "svc.yaml"))
	if err == nil {
		t.Fatal("expected an error for a YAML file with no kind: Pod document")
	}
	if !strings.Contains(err.Error(), "Service") {
		t.Errorf("error should name the kinds it did find, got: %v", err)
	}
}

// TestParseKubeWithoutYamlKey covers TODO.md section 2: a [Kube] section with no Yaml= used to
// reach readK8sYaml("") and fail with an error about an empty path.
func TestParseKubeWithoutYamlKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "web.kube")
	write(t, path, "[Kube]\nPublishPort=8080:80\n")

	_, err := ParseQuadletFile(path)
	if err == nil {
		t.Fatal("expected an error for a [Kube] section without Yaml=")
	}
	if !strings.Contains(err.Error(), "Yaml=") {
		t.Errorf("error should name the missing key, got: %v", err)
	}
}
