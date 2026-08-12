package util

import (
	"bufio"
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/fkmiec/quadctl/internal/schema"
	yaml "github.com/goccy/go-yaml"
)

var (
	extensions = map[string]bool{
		".container": true,
		".pod":       true,
		".network":   true,
		".volume":    true,
		".kube":      true,
	}
)

type Quadctl struct {
	QuadletSchemas map[string]map[string]schema.SchemaOption
	Config         map[string]string
	Runner         Runner // Executes every external command; swapped for a fake in tests

	IsRootful         bool
	IsSystemd         bool
	IsPrintOnly       bool
	IsVerbose         bool
	IsFile            bool
	ListDepth         int
	IsListAll         bool
	IsShowAll         bool
	IsLongStatus      bool
	Subcommand        string
	SearchDir         string
	PathArg           string // Positional path argument given to the subcommand, if any
	PodmanArgs        string
	RunCmd            string
	DotQuadletsPath   string
	QuadletSrcPath    string // Path to the user's source directory containing quadlet folders or files
	UseSubdirectories bool   // Default to installing quadlets in a subdirectory to keep them organized
	UseSymbolicLinks  bool   // Default to copying files for installation to avoid potential issues with source files being moved or deleted, but can be configured to use symbolic links for a more dynamic setup
	IsReloadSystemd   bool   // Default to reloading systemd after installation to apply changes immediately
	IsRemoveVolumes   bool   // Default to removing volumes on uninstall since they are often not needed after uninstall and can be left behind if not removed, but can be configured to keep volumes for data persistence.
	IsRemoveNetworks  bool   // Default to removing networks on uninstall since they are often not needed after uninstall and can be left behind if not removed, but can be configured to keep volumes for data persistence.
	SystemdStartTmpl  *template.Template
	SystemdStopTmpl   *template.Template
	SystemdStatusTmpl *template.Template
	SystemdReloadTmpl *template.Template
	SystemdLogsTmpl   *template.Template
	QuadletRootPath   string
	QuadletUserPath   string
}

// Quadlet represents a parsed Quadlet file and its relationships.
type Quadlet struct {
	ID             string // Base name without extension (e.g., "my-app")
	Filepath       string
	Type           string // .container, .pod, .network, .volume
	Sections       map[string]map[string][]string
	Deps           []string                 // IDs of other quadlets that must run first
	ParentPod      string                   // If this is a container, the ID of its parent pod
	RestartPolicy  string                   // [Service] Restart=
	GeneratedNames map[string]string        // Key: name type, Value: specific name (useful for ps filters)
	ServiceName    string                   // The name of the systemd unit (from quadlet file or default to <id>-<type>)
	KubernetesYaml string                   // If specified, the path to the Kubernetes YAML file for this quadlet
	KubeResources  []map[string]interface{} //type, name, image for any pod or container defined in k8s yaml
}

type Option struct {
	Key   string
	Value string
}

func InitQuadlets(quadctl *Quadctl) ([]*Quadlet, error) {
	// Discover, parse and resolve dependencies
	quadlets, err := discoverAndParseQuadlets(quadctl, quadctl.SearchDir)
	if err != nil {
		return nil, fmt.Errorf("processing quadlets in %s: %w", quadctl.SearchDir, err)
	}

	// If user specified the -f flag, the path provided should be a quadlet file, rather than directory. Only process the specified file and its dependencies.
	var selectedQuadlets []*Quadlet
	if quadctl.IsFile {
		if quadctl.PathArg == "" {
			return nil, fmt.Errorf("-f/--file requires the path of a quadlet file")
		}
		// If a file was specified, find the corresponding quadlet
		name := filepath.Base(quadctl.PathArg)
		tmp := strings.TrimSuffix(name, filepath.Ext(name))
		selected := quadlets[tmp]
		if selected == nil {
			return nil, fmt.Errorf("quadlet %s not found in %s", name, quadctl.SearchDir)
		}
		selectedQuadlets = append(selectedQuadlets, selected)
		if len(selected.Deps) > 0 {
			// Add dependencies to the selected quadlets
			for _, dep := range selected.Deps {
				if depQuadlet := quadlets[dep]; depQuadlet != nil {
					selectedQuadlets = append(selectedQuadlets, depQuadlet)
				}
			}
		}
		// Replace the original quadlets with the selected ones
		selectedQuadletsMap := make(map[string]*Quadlet)
		for _, q := range selectedQuadlets {
			selectedQuadletsMap[q.ID] = q
		}
		quadlets = selectedQuadletsMap
	}

	// Topologically sort quadlets based on dependencies
	ordered, err := topologicalSort(quadlets)
	if err != nil {
		return nil, fmt.Errorf("determining ordering: %w", err)
	}

	return ordered, nil
}

// InitAllQuadlets discovers and parses quadlets across every subdirectory of the
// configured quadlet source path, returning the combined list. Used by commands
// like 'ps' that report on all quadlets managed by quadctl when no specific
// quadlet name or path was given on the command line.
func InitAllQuadlets(quadctl *Quadctl) ([]*Quadlet, error) {
	dirs, err := ListSubdirectories(quadctl.QuadletSrcPath)
	if err != nil {
		return nil, fmt.Errorf("listing quadlets in %s: %w", quadctl.QuadletSrcPath, err)
	}

	origSearchDir := quadctl.SearchDir
	// A single-file filter is meaningless once the scope is widened to every quadlet
	// directory, and would abort on the first directory that doesn't contain the file.
	origIsFile := quadctl.IsFile
	quadctl.IsFile = false
	defer func() {
		quadctl.SearchDir = origSearchDir
		quadctl.IsFile = origIsFile
	}()

	var all []*Quadlet
	for _, d := range dirs {
		quadctl.SearchDir = filepath.Join(quadctl.QuadletSrcPath, d)
		quadlets, err := InitQuadlets(quadctl)
		if err != nil {
			return nil, err
		}
		all = append(all, quadlets...)
	}

	return all, nil
}

// --- PARSING AND GENERATION LOGIC ---

func discoverAndParseQuadlets(quadctl *Quadctl, searchDir string) (map[string]*Quadlet, error) {

	quadlets := make(map[string]*Quadlet)

	if info, err := os.Stat(searchDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("search path is not a directory: %s", searchDir)
	}

	dir, err := os.Open(searchDir)
	if err != nil {
		return nil, err
	}
	files, err := dir.Readdir(0)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		//fmt.Println(f.Name(), f.IsDir())
		path := filepath.Join(searchDir, f.Name())
		ext := filepath.Ext(path)
		if ".quadlets" == ext {
			//parseDotQuadlets extracts individual quadlets into separate files in a temp directory
			tempDir, err := parseDotQuadlets(path)
			if err != nil {
				return nil, err
			}
			//Save the DotQuadletsPath (location .quadlets file was extracted to) for systemd install
			quadctl.DotQuadletsPath = tempDir
		}
	}

	// If there were .quadlets files, then we copy other dot files to the temp directory where .quadlets were extracted
	if quadctl.DotQuadletsPath != "" {
		for _, f := range files {
			//Copy subdirectories (ie. drop-in directories and files)
			if f.IsDir() {
				//fmt.Printf("Calling CopyDir for: %s\n", f.Name())
				path := filepath.Join(searchDir, f.Name())
				newPath := filepath.Join(quadctl.DotQuadletsPath, f.Name())
				if err := CopyDir(path, newPath); err != nil {
					return nil, fmt.Errorf("copying drop-in directory %s to %s: %w", path, newPath, err)
				}
				continue
			}
			//Skip the .quadlets files that were already extracted into the temp directory
			path := filepath.Join(searchDir, f.Name())
			ext := filepath.Ext(path)
			if ".quadlets" == ext {
				continue
			}
			//Copy any other files over (could be .container, .volume, etc. or .env file or a README ... whatever)
			//fmt.Printf("Calling CopyFile for: %s\n", f.Name())
			newPath := filepath.Join(quadctl.DotQuadletsPath, f.Name())
			if err := CopyFile(path, newPath); err != nil {
				return nil, fmt.Errorf("copying %s to temporary .quadlets processing path %s: %w", path, newPath, err)
			}
		}
		searchDir = quadctl.DotQuadletsPath
		dir, err = os.Open(searchDir)
		if err != nil {
			return nil, err
		}
		files, err = dir.Readdir(0)
		if err != nil {
			return nil, err
		}
	}

	// Below will process all .container, .pod, .volume, .network, .kube files
	// If there were .quadlets files, all were extracted to a temp directory and all other files and subdirectories were copied to the temp directory

	for _, f := range files {
		//fmt.Printf("Calling parseQuadlet for: %s\n", f.Name())
		path := filepath.Join(searchDir, f.Name())
		ext := filepath.Ext(path)
		if extensions[ext] {
			q, err := parseQuadlet(path)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", path, err)
			}
			quadlets[q.ID] = q
		}
	}

	// 2nd pass: Extract dependencies (after all have IDs)
	for _, q := range quadlets {
		extractDependencies(q, quadlets)
	}

	return quadlets, nil
}

// Split quadlets by "---" on a separate new line and find filenames specified as "# FileName=<name>"
func parseDotQuadlets(path string) (string, error) {
	// Extract the .quadlets file into a temp directory with the same name as the original quadctl.SearchDir in the system temp directory.
	id := filepath.Base(filepath.Dir(path))
	tempDir := filepath.Join(os.TempDir(), id)

	//fmt.Printf("Temp Dir for .quadlet: %s\n", tempDir)

	// Remove tempDir, if exists already, so that no old files remain from prior runs.
	err := os.RemoveAll(tempDir)
	if err != nil && !os.IsNotExist(err) {
		fmt.Printf("Failed to remove existing temp directory: %v\n", err)
		return "", err
	}

	// Create temp directory
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp directory: %v\n", err)
		return "", err
	}

	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	baseQuadletFilename := ""
	quadletText := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		//fmt.Println("READING: " + line)
		if strings.HasPrefix(line, "#") && strings.Contains(strings.ToLower(strings.TrimSpace(line)), "filename") {
			//fmt.Println("Found Filename...")
			prop := strings.Split(line, "=")
			if len(prop) > 1 {
				baseQuadletFilename = strings.TrimSpace(prop[1])
				//fmt.Println("Filename: " + baseQuadletFilename)
				continue
			}
		}
		// Save file when hit the separator
		if "---" == strings.TrimSpace(line) {
			baseQuadletFilename = checkExtension(baseQuadletFilename, quadletText)

			err := WriteFile(filepath.Join(tempDir, baseQuadletFilename), quadletText)
			if err != nil {
				return "", err
			}
			baseQuadletFilename = ""
			quadletText = ""
			continue
		}
		quadletText += line + "\n"
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading .quadlets file %s: %w", path, err)
	}

	// Save file if reach end of .quadlet file with a filename and quadlet text
	if len(baseQuadletFilename) > 0 && len(quadletText) > 0 {
		//fmt.Println("SAVING FINAL FILE...")
		baseQuadletFilename = checkExtension(baseQuadletFilename, quadletText)
		err := WriteFile(filepath.Join(tempDir, baseQuadletFilename), quadletText)
		if err != nil {
			return "", err
		}
	}

	return tempDir, nil
}

// Add quadlet file extension if omitted in user's .quadlets file. Podman docs had examples with extension and later without, so handle both.
func checkExtension(filename string, quadletText string) string {
	//fmt.Printf("checkExtension filename param: %s\n", filename)
	ext := filepath.Ext(filename)
	if ext == "" {
		if strings.Contains(quadletText, "[Container]") {
			ext = ".container"
		} else if strings.Contains(quadletText, "[Volume]") {
			ext = ".volume"
		} else if strings.Contains(quadletText, "[Network]") {
			ext = ".network"
		} else if strings.Contains(quadletText, "[Pod]") {
			ext = ".pod"
		}
		filename += ext
	}
	//fmt.Printf("checkExtension filename returned: %s\n", filename)
	return filename
}

// IsQuadletExtension reports whether ext (as returned by filepath.Ext) is one of
// the recognized quadlet file extensions (.container, .pod, .network, .volume, .kube).
func IsQuadletExtension(ext string) bool {
	return extensions[ext]
}

// ParseQuadletFile parses a single quadlet file in isolation, without regard to
// its siblings or dependencies. Used to resolve the ServiceName (which may be
// overridden via a ServiceName= option) of a quadlet file that is still present
// in an installed systemd directory but has already been removed from the
// source directory, so it can be stopped before its stale installed copy is deleted.
func ParseQuadletFile(path string) (*Quadlet, error) {
	return parseQuadlet(path)
}

func parseQuadlet(path string) (*Quadlet, error) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	id := strings.TrimSuffix(base, ext)

	q := &Quadlet{
		ID:             id,
		Filepath:       path,
		Type:           ext,
		Sections:       make(map[string]map[string][]string),
		GeneratedNames: make(map[string]string),
	}

	if err := parseIniFile(path, q); err != nil {
		return nil, err
	}

	// Set service name ... For container, use ServiceName if provided, otherwise {id}. For others, ServiceName or {id}-{type}
	var confServiceName string
	switch q.Type {
	case ".container":
		q.GeneratedNames["container"] = id
		confServiceName = LastValue(q.Sections["Container"], "ServiceName")
	case ".pod":
		confServiceName = LastValue(q.Sections["Pod"], "ServiceName")
	case ".volume":
		confServiceName = LastValue(q.Sections["Volume"], "ServiceName")
	case ".network":
		confServiceName = LastValue(q.Sections["Network"], "ServiceName")
	case ".kube":
		confServiceName = LastValue(q.Sections["Kube"], "ServiceName")
	}
	if confServiceName == "" {
		if q.Type == ".container" || q.Type == ".kube" {
			q.ServiceName = id
		} else {
			q.ServiceName = id + "-" + strings.TrimPrefix(q.Type, ".")
		}
	} else {
		q.ServiceName = confServiceName
	}

	// Merge systemd-style drop-ins from filename.d/*.conf
	dropInDir := path + ".d"
	if info, err := os.Stat(dropInDir); err == nil && info.IsDir() {
		files, err := filepath.Glob(filepath.Join(dropInDir, "*.conf"))
		if err != nil {
			return nil, fmt.Errorf("reading drop-in directory %s: %w", dropInDir, err)
		}
		slices.Sort(files) // Drop-ins are applied in name order, so a later one can override an earlier.
		for _, f := range files {
			// A drop-in that can't be read changes what the quadlet means; failing quietly
			// here is how an unreadable override turns into a mystery further down.
			if err := parseIniFile(f, q); err != nil {
				return nil, fmt.Errorf("reading drop-in %s: %w", f, err)
			}
		}
	}

	// Specific checks based on parsing
	if contSec, ok := q.Sections["Container"]; ok {
		if val := LastValue(contSec, "ContainerName"); val != "" {
			q.GeneratedNames["container"] = val
		}
		if val := LastValue(contSec, "Pod"); val != "" {
			q.ParentPod = strings.TrimSuffix(val, ".pod")
		}
		if val := LastValue(contSec, "AutoUpdate"); val != "" {
			q.GeneratedNames["auto_update"] = val
		}
	}

	if podSec, ok := q.Sections["Pod"]; ok {
		if val := LastValue(podSec, "PodName"); val != "" {
			q.GeneratedNames["pod_name"] = val
		}
	}

	if svcSec, ok := q.Sections["Service"]; ok {
		if val := LastValue(svcSec, "Restart"); val != "" {
			q.RestartPolicy = strings.ToLower(val)
		}
	}

	if kubeSec, ok := q.Sections["Kube"]; ok {
		if yamlPath := LastValue(kubeSec, "Yaml"); yamlPath != "" {
			if !filepath.IsAbs(yamlPath) {
				yamlPath = filepath.Join(filepath.Dir(q.Filepath), yamlPath)
			}
			if info, err := os.Stat(yamlPath); err != nil || info.IsDir() {
				return nil, fmt.Errorf("Yaml= does not name a readable file: %s", yamlPath)
			}
			q.KubernetesYaml = yamlPath
		}
		resources, err := readK8sYaml(q.KubernetesYaml)
		if err != nil {
			return nil, err
		}
		q.KubeResources = resources
	}

	return q, nil
}

// parseIniFile reads one quadlet file (or drop-in) into q.Sections, recording each
// assignment exactly as written. Nothing is tokenized here: whether a written value is one
// value or several is a property of the option, not of the file, and only the schema knows
// which - see OptionValues, which applies that at use time. Splitting here is what used to
// silently drop Exec=/bin/sh -c "echo hi".
func parseIniFile(path string, q *Quadlet) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	currentSection := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// A trailing backslash continues the assignment on the next line, the way systemd
		// unit files spell a long value across several.
		for strings.HasSuffix(line, `\`) && scanner.Scan() {
			line = strings.TrimSpace(strings.TrimSuffix(line, `\`)) + " " + strings.TrimSpace(scanner.Text())
			line = strings.TrimSpace(line)
		}

		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			if _, exists := q.Sections[currentSection]; !exists {
				q.Sections[currentSection] = make(map[string][]string)
			}
			continue
		}

		if currentSection == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		if val == "" {
			// Assigning nothing resets the key, as in systemd - it is how a drop-in clears a
			// list the base file set rather than adding to it.
			delete(q.Sections[currentSection], key)
			continue
		}
		q.Sections[currentSection][key] = append(q.Sections[currentSection][key], val)
	}
	return scanner.Err()
}

// OptionValues resolves the raw lines recorded for a key into the values a command should be
// built from.
//
// An option the schema marks AllowMultiple may be repeated on its own line and may also carry
// several whitespace-separated values on one line, the way quadlet reads them; quote a value
// that contains a space. An option that takes a single value keeps its line intact, spaces
// and all, and the last assignment wins - again as in systemd. An option the schema doesn't
// know is treated as single-valued, since guessing the other way would split a value that was
// never meant to be split.
func OptionValues(options map[string]schema.SchemaOption, key string, raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	if opt, known := options[key]; !known || !opt.AllowMultiple {
		return raw[len(raw)-1:]
	}
	var values []string
	for _, line := range raw {
		values = append(values, ParseFields(line)...)
	}
	return values
}

// LastValue returns the effective value of a single-valued key in a section: the last
// assignment wins. Empty when the key was never set.
func LastValue(section map[string][]string, key string) string {
	vals := section[key]
	if len(vals) == 0 {
		return ""
	}
	return vals[len(vals)-1]
}

// extractDependencies determines implicit and explicit requirements
func extractDependencies(q *Quadlet, all map[string]*Quadlet) {
	depSet := make(map[string]bool)

	// Explicit Systemd dependencies [Unit] After=/Requires=. Both take a space-separated list
	// of units per line and may be given more than once.
	if unit, ok := q.Sections["Unit"]; ok {
		for _, key := range []string{"Requires", "After"} {
			for _, line := range unit[key] {
				for _, val := range ParseFields(line) {
					// Strip systemd.service ext, and optional quadlet ext, map back to ID
					id := strings.TrimSuffix(val, ".service")
					id = strings.TrimSuffix(id, filepath.Ext(id))
					if _, exists := all[id]; exists {
						depSet[id] = true
					}
				}
			}
		}
	}

	// Implicit dependencies [Container/Pod] Network=/Volume=/Pod=
	if q.Type == ".container" {
		cont := q.Sections["Container"]
		if pod := LastValue(cont, "Pod"); pod != "" {
			podID := strings.TrimSuffix(pod, ".pod")
			depSet[podID] = true
			// Get the user-specified pod name for potential use in ps filters, otherwise will use pod ID as the pod name.
			// The referenced pod file may not exist in this directory at all; the missing dependency
			// is reported by topologicalSort, so here just fall back to the ID.
			podName := podID
			if parent, ok := all[podID]; ok && parent.GeneratedNames["pod_name"] != "" {
				podName = parent.GeneratedNames["pod_name"]
			}
			q.GeneratedNames["pod_name"] = podName
		}

		for _, net := range cont["Network"] {
			id := strings.TrimSuffix(net, ".network")
			if _, exists := all[id]; exists {
				depSet[id] = true
			}
		}

		for _, vol := range cont["Volume"] {
			// Vol format source.volume:/path
			sourceVol := strings.TrimSuffix(strings.Split(vol, ":")[0], ".volume")
			if _, exists := all[sourceVol]; exists {
				depSet[sourceVol] = true
			}
		}
	} else if q.Type == ".pod" {
		podSec := q.Sections["Pod"]
		for _, net := range podSec["Network"] {
			id := strings.TrimSuffix(net, ".network")
			if _, exists := all[id]; exists {
				depSet[id] = true
			}
		}
	}

	// Sorted: Deps drives the topological sort, and an unstable order there reorders the
	// commands quadctl emits from one run to the next.
	q.Deps = slices.Sorted(maps.Keys(depSet))
}

func topologicalSort(quadlets map[string]*Quadlet) ([]*Quadlet, error) {
	var ordered []*Quadlet
	visited := make(map[string]bool)
	temp := make(map[string]bool)

	var visit func(nodeID string) error
	visit = func(nodeID string) error {
		if temp[nodeID] {
			return fmt.Errorf("circular dependency detected involving %s", nodeID)
		}
		if visited[nodeID] {
			return nil
		}

		temp[nodeID] = true
		for _, dep := range quadlets[nodeID].Deps {
			if _, exists := quadlets[dep]; !exists {
				return fmt.Errorf("%s depends on unknown quadlet %s", nodeID, dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		temp[nodeID] = false
		visited[nodeID] = true
		ordered = append(ordered, quadlets[nodeID])
		return nil
	}

	// Sorted seed order, so quadlets with no dependency relationship between them keep a
	// stable relative order instead of shuffling on every run.
	for _, id := range slices.Sorted(maps.Keys(quadlets)) {
		if !visited[id] {
			if err := visit(id); err != nil {
				return nil, err
			}
		}
	}
	return ordered, nil
}

// ParseFields splits a written value into the argv entries it stands for, the way a shell or
// systemd would: whitespace separates tokens, text inside matching single or double quotes is
// one token spaces and all, and the quotes themselves are punctuation rather than content.
//
// Dropping the quotes here is what lets the result be handed straight to exec: an argument is
// already exactly the bytes the program should see, so nothing downstream has to guess which
// quotes were the user's and which were syntax.
func ParseFields(input string) []string {
	var fields []string
	var current strings.Builder
	var quote rune
	quoted := false // a token can be quoted and empty: --entrypoint ""

	flush := func() {
		if current.Len() > 0 || quoted {
			fields = append(fields, current.String())
			current.Reset()
			quoted = false
		}
	}

	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			quoted = true
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()

	return fields
}

// valuePlaceholder stands in for an option's value while its podman template is rendered, so
// the rendered fragment can be cut into arguments without the value's own spaces being read
// as separators. Nothing a quadlet file can contain looks like it.
//
// This assumes a template only ever emits the value, never branches on it - true of every
// option in the schema, and worth rechecking if one ever grows an {{if}}.
const valuePlaceholder = "\x00quadctl-value\x00"

// QuadletOptionToPodman renders one quadlet key/value into the podman arguments it stands
// for. A template is a command-line fragment: it may expand to a flag and its value
// ("{{.Key}} {{.Value}}"), to a single token ("--driver={{.Value}}"), to a flag that embeds
// the value ("--security-opt apparmor={{.Value}}"), or to a flag alone ("--read-only"). So it
// is rendered with the placeholder, cut into arguments, and only then given the real value —
// cutting the rendered text itself is what turned Environment=GREETING="hello world" into two
// arguments and a container that never saw the second word.
func QuadletOptionToPodman(qType string, options map[string]schema.SchemaOption, k string, v string) ([]string, error) {
	opt, ok := options[k]
	if !ok {
		return nil, fmt.Errorf("Quadlet %s option not defined: %s", qType, k)
	}

	var buf bytes.Buffer
	if err := opt.PodmanTemplateParsed.Execute(&buf, Option{Key: opt.PodmanKey, Value: valuePlaceholder}); err != nil {
		return nil, fmt.Errorf("Error formatting %s option %s: %w", qType, k, err)
	}

	args := ParseFields(buf.String())
	for i, arg := range args {
		args[i] = strings.ReplaceAll(arg, valuePlaceholder, v)
	}
	return args, nil
}

func readYamlFile(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

// Very basic extraction by scanning for "image:" key in Kubernetes YAML
func readK8sYaml(yamlPath string) ([]map[string]interface{}, error) {

	var resources []map[string]interface{}

	yml, _ := readYamlFile(yamlPath)

	var kind string
	var pod map[string]interface{}
	path, err := yaml.PathString("$.kind")
	if err != nil {
		return nil, fmt.Errorf("creating YAML path: %w", err)
	}
	if err := path.Read(strings.NewReader(yml), &kind); err != nil {
		return nil, fmt.Errorf("reading kind from %s: %w", yamlPath, err)
	}
	if kind != "Pod" {
		return nil, fmt.Errorf("%s: unsupported Kubernetes resource kind: %s", yamlPath, kind)
	}
	path, err = yaml.PathString("$.metadata")
	if err != nil {
		return nil, fmt.Errorf("creating YAML path: %w", err)
	}
	if err := path.Read(strings.NewReader(yml), &pod); err != nil {
		return nil, fmt.Errorf("reading metadata from %s: %w", yamlPath, err)
	}
	pod["type"] = "pod"
	//pod["name"] comes as part of $.metadata
	resources = append(resources, pod)

	var containers []map[string]interface{}
	path, err = yaml.PathString("$.spec.containers[*]")
	if err != nil {
		return nil, fmt.Errorf("creating YAML path: %w", err)
	}
	if err := path.Read(strings.NewReader(yml), &containers); err != nil {
		return nil, fmt.Errorf("reading containers from %s: %w", yamlPath, err)
	}
	for i := range containers {
		containers[i]["type"] = "container"
		containers[i]["pod"] = pod["name"]
	}
	resources = append(resources, containers...)

	//fmt.Printf("K8s:\n%v\n", resources)

	return resources, nil
}
