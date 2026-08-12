package util

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// Config is what the user configured: read once from quadctl.ini before any work starts and
// not written to afterwards. Everything that varies per invocation - flags, the subcommand,
// the directory being acted on - lives on State instead. Keeping the two apart is what stops
// one directory's run from carrying settings into the next (PLAN.md 3.2).
type Config struct {
	// Where quadlets are read from and installed to.
	QuadletSrcPath  string // The user's own quadlet directories
	QuadletRootPath string // systemd's generator directory, rootful
	QuadletUserPath string // systemd's generator directory, rootless

	// How installing into the generator directories behaves.
	UseSubdirectories bool // Install each source directory as its own subdirectory
	UseSymbolicLinks  bool // Link the source files instead of copying them
	IsReloadSystemd   bool // daemon-reload after installing or removing
	IsRemoveVolumes   bool // Remove volumes on uninstall
	IsRemoveNetworks  bool // Remove networks on uninstall

	// SystemdEnabled makes systemd mode the default, as though -s had been given.
	SystemdEnabled bool

	// The systemd commands to drive, as templates - "{{.user}}" expands to --user when
	// rootless and to nothing when rootful.
	SystemdStartTmpl  *template.Template
	SystemdStopTmpl   *template.Template
	SystemdStatusTmpl *template.Template
	SystemdReloadTmpl *template.Template
	SystemdLogsTmpl   *template.Template

	// Values is the file as read, before any of the above was derived from it. Kept for
	// diagnostics and for reporting keys quadctl doesn't recognize.
	Values map[string]string
}

// DefaultConfig returns the configuration quadctl uses before quadctl.ini is consulted. The
// defaults are the conservative ones: copy rather than link (a link breaks when the source
// moves), organize into subdirectories, reload systemd so edits take effect, and clean up
// volumes and networks on uninstall rather than leaving them behind.
func DefaultConfig() *Config {
	return &Config{
		QuadletRootPath:   "/etc/containers/systemd",
		QuadletUserPath:   "/etc/containers/systemd/users",
		UseSubdirectories: true,
		UseSymbolicLinks:  false,
		IsReloadSystemd:   true,
		IsRemoveVolumes:   true,
		IsRemoveNetworks:  true,
		SystemdStartTmpl:  template.Must(template.New("systemdStart").Parse("systemctl {{.user}} start")),
		SystemdStopTmpl:   template.Must(template.New("systemdStop").Parse("systemctl {{.user}} stop")),
		SystemdStatusTmpl: template.Must(template.New("systemdStatus").Parse("systemctl {{.user}} status")),
		SystemdReloadTmpl: template.Must(template.New("systemdReload").Parse("systemctl {{.user}} daemon-reload")),
		SystemdLogsTmpl:   template.Must(template.New("systemdLogs").Parse("journalctl {{.user}} -xe")),
		Values:            map[string]string{},
	}
}

// LoadConfig reads quadctl.ini over the defaults. isRootful decides how hard it insists on
// QUADCTL_CONFIG_DIR: root does not read the invoking user's $HOME, so falling back to it
// would silently use a different config than the same command run without sudo.
func LoadConfig(isRootful bool) (*Config, error) {
	values, err := readConfigFile(isRootful)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig()
	cfg.Values = values

	if val, ok := values["use_subdirectories"]; ok && (val == "false" || val == "0") {
		cfg.UseSubdirectories = false
	}
	if val, ok := values["use_symbolic_links"]; ok && (val == "true" || val == "1") {
		cfg.UseSymbolicLinks = true
	}
	if val, ok := values["auto_reload_systemd"]; ok && (val == "false" || val == "0") {
		cfg.IsReloadSystemd = false
	}
	if val, ok := values["remove_volumes"]; ok && (val == "false" || val == "0") {
		cfg.IsRemoveVolumes = false
	}
	if val, ok := values["remove_networks"]; ok && (val == "false" || val == "0") {
		cfg.IsRemoveNetworks = false
	}
	if val, ok := values["quadlet.src.path"]; ok && val != "" {
		cfg.QuadletSrcPath = val
	}
	if val, ok := values["quadlet.root.path"]; ok && val != "" {
		cfg.QuadletRootPath = val
	}
	if val, ok := values["quadlet.user.path"]; ok && val != "" {
		cfg.QuadletUserPath = val
	}
	if val, ok := values["systemd.enabled"]; ok && (val == "true" || val == "1") {
		cfg.SystemdEnabled = true
	}

	for _, t := range []struct {
		key  string
		dest **template.Template
	}{
		{"systemd.start", &cfg.SystemdStartTmpl},
		{"systemd.stop", &cfg.SystemdStopTmpl},
		{"systemd.status", &cfg.SystemdStatusTmpl},
		{"systemd.reload", &cfg.SystemdReloadTmpl},
		{"systemd.logs", &cfg.SystemdLogsTmpl},
	} {
		val, ok := values[t.key]
		if !ok || val == "" {
			continue
		}
		parsed, err := template.New(t.key).Parse(val)
		if err != nil {
			return nil, fmt.Errorf("config key %s is not a valid template: %w", t.key, err)
		}
		*t.dest = parsed
	}

	return cfg, nil
}

// readConfigFile locates quadctl.ini, writing the shipped default if there is none, and
// returns its key/value pairs.
func readConfigFile(isRootful bool) (map[string]string, error) {

	config := make(map[string]string)
	var path string

	// Use config path specified by user in environment variable if provided. Make it required if running as root.
	path = os.Getenv("QUADCTL_CONFIG_DIR")
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		if isRootful {
			err = fmt.Errorf("Invalid config path %s\nDefault location based on user $HOME is not read by root.\nSet $QUADCTL_CONFIG_DIR in /etc/environment to define a single location for both root and non-root.\nFor example:\n\necho \"QUADCTL_CONFIG_DIR=$HOME/.config/quadctl\" | sudo tee -a /etc/environment > /dev/null", path)
			return nil, err
			// Use standard $HOME/.config for normal user in absence of QUADCTL_CONFIG_DIR environment variable
		} else {
			path = os.Getenv("XDG_CONFIG_HOME")
			if path == "" {
				path = os.Getenv("HOME") + "/.config"
			}
			path = filepath.Join(path, "quadctl")
		}
	}

	// Create quadlet config directory if not exists
	if err := createDirIfNotExists(path); err != nil {
		return nil, fmt.Errorf("config directory (%s) not found and could not be created: %w", path, err)
	}

	path = filepath.Join(path, "quadctl.ini")

	_, err := os.Stat(path)
	if err != nil {
		if err := installDefaultConfig(path); err != nil {
			return nil, err
		}
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			config[key] = val
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	// Check if user quadlet locations all exist and create if not.
	if err := createDirIfNotExists(config["quadlet.src.path"]); err != nil {
		return nil, fmt.Errorf("configured quadlet.src.path not found and could not be created: %w", err)
	}
	if err := createDirIfNotExists(config["quadlet.user.path"]); err != nil {
		return nil, fmt.Errorf("configured quadlet.user.path not found and could not be created: %w", err)
	}

	return config, nil
}
