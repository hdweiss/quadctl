package config

import (
	"bufio"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

	// Warnings is what quadctl could not make sense of in the file: a key it does not know,
	// or a value that should have been a boolean and wasn't. Both used to be dropped in
	// silence, which is how a misspelled key looks exactly like a setting that doesn't work.
	// main prints these before doing anything else.
	Warnings []string
}

// configBools maps each boolean configuration key to the field it sets.
func (c *Config) configBools() map[string]*bool {
	return map[string]*bool{
		"use_subdirectories":  &c.UseSubdirectories,
		"use_symbolic_links":  &c.UseSymbolicLinks,
		"auto_reload_systemd": &c.IsReloadSystemd,
		"remove_volumes":      &c.IsRemoveVolumes,
		"remove_networks":     &c.IsRemoveNetworks,
		"systemd.enabled":     &c.SystemdEnabled,
	}
}

// configStrings maps each plain string configuration key to the field it sets.
func (c *Config) configStrings() map[string]*string {
	return map[string]*string{
		"quadlet.src.path":  &c.QuadletSrcPath,
		"quadlet.root.path": &c.QuadletRootPath,
		"quadlet.user.path": &c.QuadletUserPath,
	}
}

// configTemplates maps each systemd command key to the template it parses into.
func (c *Config) configTemplates() map[string]**template.Template {
	return map[string]**template.Template{
		"systemd.start":  &c.SystemdStartTmpl,
		"systemd.stop":   &c.SystemdStopTmpl,
		"systemd.status": &c.SystemdStatusTmpl,
		"systemd.reload": &c.SystemdReloadTmpl,
		"systemd.logs":   &c.SystemdLogsTmpl,
	}
}

// parseConfigBool reads a boolean the way someone writing a config file would expect to be
// able to write one. Each key used to be compared against a hardcoded pair of spellings, and
// in one direction only: use_symbolic_links reacted to "true" and "1" but not "True" or
// "yes", use_subdirectories only to "false" and "0", and anything else was dropped without a
// word (TODO.md section 4).
func parseConfigBool(val string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "true", "t", "yes", "y", "on", "1":
		return true, true
	case "false", "f", "no", "n", "off", "0":
		return false, true
	}
	return false, false
}

// DefaultConfig returns the configuration quadctl uses before quadctl.ini is consulted. The
// defaults are the conservative ones: copy rather than link (a link breaks when the source
// moves), organize into subdirectories, reload systemd so edits take effect, and clean up
// volumes and networks on uninstall rather than leaving them behind.
func DefaultConfig() *Config {
	return &Config{
		QuadletRootPath:   "/etc/containers/systemd",
		QuadletUserPath:   DefaultUserQuadletPath(),
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

// DefaultUserQuadletPath is the rootless generator directory quadctl assumes when
// quadlet.user.path is unset: the XDG one, which is also what the shipped quadctl.ini writes.
// The code used to default to /etc/containers/systemd/users instead - a path the rootless
// user quadctl was about to create it as generally cannot write (TODO.md section 4).
func DefaultUserQuadletPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "containers", "systemd")
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

	bools, strs, tmpls := cfg.configBools(), cfg.configStrings(), cfg.configTemplates()

	// Sorted, so a file with several problems reports them in the same order every time.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		val := values[key]
		switch {
		case bools[key] != nil:
			parsed, ok := parseConfigBool(val)
			if !ok {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf("config key %s: %q is not a true/false value, ignoring it", key, val))
				continue
			}
			*bools[key] = parsed
		case strs[key] != nil:
			if val != "" {
				*strs[key] = val
			}
		case tmpls[key] != nil:
			if val == "" {
				continue
			}
			parsed, err := template.New(key).Parse(val)
			if err != nil {
				return nil, fmt.Errorf("config key %s is not a valid template: %w", key, err)
			}
			*tmpls[key] = parsed
		default:
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("config key %s is not one quadctl knows, ignoring it", key))
		}
	}

	// Create the directories this invocation could actually write to. Only one of the two
	// generator directories applies: creating the user path while running under sudo left
	// root-owned directories in the user's home, and the root path was never created at all
	// (TODO.md section 2).
	paths := map[string]string{"quadlet.src.path": cfg.QuadletSrcPath}
	if isRootful {
		paths["quadlet.root.path"] = cfg.QuadletRootPath
	} else {
		paths["quadlet.user.path"] = cfg.QuadletUserPath
	}
	for _, key := range slices.Sorted(maps.Keys(paths)) {
		if err := createDirIfNotExists(paths[key]); err != nil {
			return nil, fmt.Errorf("configured %s (%s) not found and could not be created: %w", key, paths[key], err)
		}
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

	// The quadlet directories are created by LoadConfig, once the defaults have filled in
	// whatever the file left out.
	return config, nil
}
