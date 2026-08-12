package core

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fkmiec/quadctl/internal/util"
)

func HandleSystemdCreate(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {

	commands := []Command{}

	var targetDir string

	if quadctl.IsRootful {
		targetDir = quadctl.Config.QuadletRootPath
	} else {
		targetDir = quadctl.Config.QuadletUserPath
	}

	// The rootless hint is worth keeping alongside the error itself: the usual cause is a
	// generator directory owned by root that the user was told to install into.
	rootlessHint := ""
	if targetDir == quadctl.Config.QuadletUserPath {
		rootlessHint = "\nIf installing rootless quadlets to /etc/containers/systemd... or /usr/share/containers/systemd... you may need to grant your user write permissions to the target directory."
	}

	// Ensure permissions to write to the target directory
	fileInfo, err := os.Stat(targetDir)
	if err != nil {
		return nil, fmt.Errorf("accessing quadlet path %s: %w%s", targetDir, err, rootlessHint)
	}
	if !fileInfo.IsDir() {
		return nil, fmt.Errorf("quadlet path %s is not a directory. Ensure the path points to a directory and try again", targetDir)
	}
	perm := fileInfo.Mode().Perm()
	if perm&0200 != 0200 && perm&0020 != 0020 && perm&0002 != 0002 {
		return nil, fmt.Errorf("quadlet path %s is not writable. Ensure the directory is writable and try again%s", targetDir, rootlessHint)
	}

	c := NewCommand(fmt.Sprintf("Systemd installing quadlets to %s", targetDir))
	if quadctl.IsVerbose {
		c.PreFn = func(c *Command) {}
		c.PostFn = func(c *Command) {}
	}

	// Systemd create is mostly file operations.
	// For file operations, we use golang functions rather than podman, systemd or bash commands ...
	// Encapsulate code to run in a slice of functions that will be executed in a custom command when the command is run.
	// Each step reports its own failure; the command stops at the first one rather than
	// carrying on with a half-installed directory.
	funcs := []func() error{}
	c.Output = append(c.Output, fmt.Sprintf("Creating target directory %s", targetDir))
	f := func() error {
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("creating target directory %s: %w", targetDir, err)
		}
		return nil
	}
	funcs = append(funcs, f)

	// Where the files to install are read from, and what the installed subdirectory is called.
	// They differ when a .quadlets bundle was extracted: the files come from the run's scratch
	// directory, but the install is still named after the user's directory. Naming it after
	// the scratch directory would give every run a new, randomly named install directory that
	// nothing - including HandleSystemdRemove and the stale-file prune - could find again.
	searchDir := quadctl.SearchDir
	installName := filepath.Base(quadctl.SearchDir)
	if quadctl.DotQuadletsPath != "" {
		searchDir = quadctl.DotQuadletsPath
		//Check for and disallow use of symbolic links with .quadlets files
		if quadctl.Config.UseSymbolicLinks {
			return nil, fmt.Errorf("cannot use symbolic links with .quadlets files.\n  The individual quadlets in a .quadlets file must be extracted to a temp directory before install to systemd.\n  Cannot link to temp directory")
		}
	}

	// Use links if configured to do so
	if quadctl.Config.UseSymbolicLinks {
		c.Output = append(c.Output, "Using symbolic links for installation.")
		if quadctl.Config.UseSubdirectories {
			// Link the entire source directory as a subdirectory in the target location to keep related quadlets together
			dest := filepath.Join(targetDir, installName)
			c.Output = append(c.Output, fmt.Sprintf("Linking directory %s -> %s", dest, searchDir))
			f := func() error {
				if err := os.Symlink(searchDir, dest); err != nil {
					return fmt.Errorf("linking %s -> %s: %w", dest, searchDir, err)
				}
				return nil
			}
			funcs = append(funcs, f)
		} else {
			// Link the individual quadlet files directly into the target location
			for _, q := range quadlets {
				dest := filepath.Join(targetDir, filepath.Base(q.Filepath))
				c.Output = append(c.Output, fmt.Sprintf("Linking %s -> %s", dest, q.Filepath))
				f := func() error {
					if err := os.Symlink(q.Filepath, dest); err != nil {
						return fmt.Errorf("linking %s -> %s: %w", dest, q.Filepath, err)
					}
					return nil
				}
				funcs = append(funcs, f)
				// Also link drop-in directory if exists
				dropInDir := q.Filepath + ".d"
				if info, err := os.Stat(dropInDir); err == nil && info.IsDir() {
					destDropIn := dest + ".d"
					c.Output = append(c.Output, fmt.Sprintf("Linking directory %s -> %s", destDropIn, dropInDir))
					f := func() error {
						if err := os.Symlink(dropInDir, destDropIn); err != nil {
							return fmt.Errorf("linking drop-in directory %s -> %s: %w", destDropIn, dropInDir, err)
						}
						return nil
					}
					funcs = append(funcs, f)
				}
			}
		}
		// Otherwise copy files to the target directory using podman quadlet install
	} else {
		var destDropIn string
		// If the user configured to use a subdirectory to organize quadlets, we create the directory and move files after podman quadlet install step.
		if quadctl.Config.UseSubdirectories {
			//Create the subdirectory at target location
			dest := filepath.Join(targetDir, installName)
			c.Output = append(c.Output, fmt.Sprintf("Copying directory %s to %s", installName, dest))
			f := func() error {
				if err := util.CopyDir(searchDir, dest); err != nil {
					return fmt.Errorf("copying %s to %s: %w", searchDir, dest, err)
				}
				return nil
			}
			funcs = append(funcs, f)
		} else {
			for _, q := range quadlets {
				c.Output = append(c.Output, fmt.Sprintf("Copying file %s to %s", filepath.Base(q.Filepath), filepath.Join(targetDir, filepath.Base(q.Filepath))))
				f := func() error {
					if err := util.CopyFile(q.Filepath, filepath.Join(targetDir, filepath.Base(q.Filepath))); err != nil {
						return fmt.Errorf("copying %s: %w", q.Filepath, err)
					}
					return nil
				}
				funcs = append(funcs, f)
			}
		}
		// Copy drop-in directories if exist
		for _, q := range quadlets {
			dropInDir := q.Filepath + ".d"
			if info, err := os.Stat(dropInDir); err == nil && info.IsDir() {

				// Set dropInDir
				if quadctl.Config.UseSubdirectories {
					destDropIn = filepath.Join(targetDir, installName, filepath.Base(q.Filepath)+".d")
				} else {
					destDropIn = filepath.Join(targetDir, filepath.Base(q.Filepath)+".d")
				}
				c.Output = append(c.Output, fmt.Sprintf("Copying directory %s to %s", filepath.Base(dropInDir), destDropIn))
				f := func() error {
					if err := util.CopyDir(dropInDir, destDropIn); err != nil {
						return fmt.Errorf("copying drop-in directory %s to %s: %w", dropInDir, destDropIn, err)
					}
					return nil
				}
				funcs = append(funcs, f)
			}
		}
	}

	// Custom run function that will, when executed by runCommands(), execute the anonymous functions created above.
	c.RunFn = func(c *Command) {
		for _, f := range funcs {
			if err := f(); err != nil {
				c.Error = err
				return
			}
		}
		if quadctl.IsVerbose {
			fmt.Println(c.Label + "... Done")
			for _, line := range c.Output {
				fmt.Println(" => " + line)
			}
		}
	}

	commands = append(commands, c)

	// Verify podman can actually turn what we just installed into systemd units before
	// reloading/starting anything. Generation failures (bad option, unknown reference, etc.)
	// otherwise fail silently during daemon-reload, surfacing later as a confusing
	// "unit not found" from systemctl start with no indication of the real cause.
	commands = append(commands, validateQuadletGenerationCommand(quadctl, quadlets, targetDir))

	// Stop and remove any previously installed quadlet files that no longer exist
	// in the source directory, so deletions are reflected the same way edits are.
	if !quadctl.Config.UseSymbolicLinks {
		stale, err := pruneStaleSystemdFiles(quadctl, targetDir, installName, searchDir)
		if err != nil {
			return nil, err
		}
		commands = append(commands, stale...)
	}

	// Reload systemd to recognize the new quadlet services
	reload, err := HandleSystemdReload(quadctl)
	if err != nil {
		return nil, err
	}
	commands = append(commands, reload...)

	return commands, nil
}

// pruneStaleSystemdFiles finds files under the installed subdirectory for searchDir
// that are no longer present in searchDir itself (ie. were deleted from the quadlet
// source since the last install) and removes them, stopping their systemd service
// first if the stale file is a quadlet definition. Only applicable when quadlets are
// installed into a dedicated subdirectory per source directory (UseSubdirectories);
// without that, the installed directory is shared across unrelated quadlet groups and
// there's no reliable way to tell which leftover files belong to this one.
// installName is the name of the subdirectory under targetDir that belongs to this source
// directory; searchDir is where the files that should be there are read from. The two differ
// when a .quadlets bundle was extracted into a scratch directory.
func pruneStaleSystemdFiles(quadctl *util.State, targetDir, installName, searchDir string) ([]Command, error) {
	commands := []Command{}

	if !quadctl.Config.UseSubdirectories {
		return commands, nil
	}

	// Refuse to prune unless the install destination is a real subdirectory of the generator
	// root that belongs to this source directory. A degenerate name (".", "..", "/", or
	// empty) collapses dest onto targetDir itself, at which point every unrelated quadlet
	// installed there looks stale and gets deleted.
	if installName == "." || installName == ".." || installName == "" || installName == string(filepath.Separator) {
		fmt.Fprintf(os.Stderr, "Warning: skipping cleanup of stale files - cannot derive an install subdirectory from source path %q\n", quadctl.SearchDir)
		return commands, nil
	}

	dest := filepath.Join(targetDir, installName)
	if filepath.Clean(dest) == filepath.Clean(targetDir) {
		fmt.Fprintf(os.Stderr, "Warning: skipping cleanup of stale files - install directory %s is the quadlet generator root\n", dest)
		return commands, nil
	}

	destEntries, err := os.ReadDir(dest)
	if err != nil {
		return commands, nil
	}
	srcEntries, err := os.ReadDir(searchDir)
	if err != nil {
		return commands, nil
	}

	present := map[string]bool{}
	for _, e := range srcEntries {
		present[e.Name()] = true
	}

	for _, e := range destEntries {
		name := e.Name()
		if present[name] {
			continue
		}

		stalePath := filepath.Join(dest, name)

		// If the stale file is itself a quadlet definition, stop its service before
		// deleting it so podman cleans up the resources it created.
		if !e.IsDir() && util.IsQuadletExtension(filepath.Ext(name)) {
			if stale, err := util.ParseQuadletFile(stalePath); err == nil {
				stop, err := HandleSystemdStop(quadctl, []*util.Quadlet{stale}, true)
				if err != nil {
					return nil, err
				}
				commands = append(commands, stop...)
			}
		}

		isDir := e.IsDir()
		cmd := NewCommand(fmt.Sprintf("Removing %s (no longer present in %s)", name, searchDir))
		cmd.RunFn = func(c *Command) {
			if isDir {
				c.Error = os.RemoveAll(stalePath)
			} else {
				c.Error = os.Remove(stalePath)
			}
		}
		commands = append(commands, cmd)
	}

	return commands, nil
}

// quadletGeneratorPaths are the well-known install locations of podman's quadlet generator
// binary (podman-system-generator / podman-user-generator are both symlinks to the same
// binary), checked in order since the exact path varies by distro and podman version.
var quadletGeneratorPaths = []string{
	"/usr/lib/systemd/system-generators/podman-system-generator",
	"/usr/libexec/podman/quadlet",
	"/usr/lib/podman/quadlet",
}

func findQuadletGenerator() string {
	for _, p := range quadletGeneratorPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("quadlet"); err == nil {
		return p
	}
	return ""
}

// stripGeneratorLogPrefix strips the "quadlet-generator[<pid>]: " prefix the generator adds to
// some (but not all) of the lines it logs, so error output reads the same regardless of which
// code path inside podman produced it.
func stripGeneratorLogPrefix(line string) string {
	if strings.HasPrefix(line, "quadlet-generator[") {
		if idx := strings.Index(line, "]: "); idx != -1 {
			return line[idx+3:]
		}
	}
	return line
}

var (
	generatorLineNumRe  = regexp.MustCompile(`line (\d+)`)
	generatorQuotedPath = regexp.MustCompile(`"([^"]+\.[a-z]+)"`)
)

// enrichWithSourceLine appends the actual offending line from the quadlet file to a generator
// error that references a specific line number (e.g. a raw parse error like 'file contains line
// 8: "LDSA" which is not a key-value pair...'), so the file doesn't need to be opened separately
// to see what's actually there.
func enrichWithSourceLine(line string) string {
	lineMatch := generatorLineNumRe.FindStringSubmatch(line)
	pathMatch := generatorQuotedPath.FindStringSubmatch(line)
	if lineMatch == nil || pathMatch == nil {
		return line
	}
	lineNum, err := strconv.Atoi(lineMatch[1])
	if err != nil {
		return line
	}
	content, err := readFileLine(pathMatch[1], lineNum)
	if err != nil {
		return line
	}
	return fmt.Sprintf("%s\n      %s:%d: %s", line, filepath.Base(pathMatch[1]), lineNum, content)
}

func readFileLine(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for i := 1; scanner.Scan(); i++ {
		if i == n {
			return strings.TrimSpace(scanner.Text()), nil
		}
	}
	return "", fmt.Errorf("line %d not found in %s", n, path)
}

// validateQuadletGenerationCommand runs podman's own quadlet generator in dry-run mode against
// the just-installed quadlet directory and reports, using podman's own error message, any file
// it could not convert into a systemd unit. Without this, a bad quadlet option or reference
// fails silently during daemon-reload, and the first sign of trouble is a confusing "unit not
// found" (or similar) from the systemctl start that follows.
func validateQuadletGenerationCommand(quadctl *util.State, quadlets []*util.Quadlet, targetDir string) Command {
	c := NewCommand("Validating quadlet definitions")
	// This is a quick, silent-on-success check; skip the spinner so a failure (which exits
	// the process directly, matching how other fatal errors in this function are handled)
	// doesn't leave it dangling mid-animation.
	c.PreFn = func(c *Command) {}
	c.PostFn = func(c *Command) {}
	c.RunFn = func(c *Command) {
		generator := findQuadletGenerator()
		if generator == "" {
			return // Can't validate on this system; fall through to the normal reload/start flow.
		}

		outDir, err := os.MkdirTemp("", "quadctl-quadlet-validate-")
		if err != nil {
			return
		}
		defer os.RemoveAll(outDir)

		args := []string{generator, "--dryrun"}
		if !quadctl.IsRootful {
			args = append(args, "-user")
		}
		args = append(args, outDir, outDir, outDir)

		output, err := quadctl.Runner.Run(args, util.RunOptions{
			Mode: util.CaptureCombined,
			Env:  []string{"QUADLET_UNIT_DIRS=" + targetDir},
		})
		if err == nil {
			return
		}

		installed := map[string]bool{}
		for _, q := range quadlets {
			installed[filepath.Base(q.Filepath)] = true
		}

		var problems []string
		for _, rawLine := range strings.Split(string(output), "\n") {
			line := stripGeneratorLogPrefix(strings.TrimSpace(rawLine))
			if line == "" || strings.HasPrefix(line, "Loading source unit file") {
				continue // Informational, not an error (podman logs one of these per file, success or not).
			}
			for name := range installed {
				if strings.Contains(line, name) {
					problems = append(problems, enrichWithSourceLine(line))
					break
				}
			}
		}

		if len(problems) == 0 {
			return // Failure doesn't involve any quadlet we just installed (e.g. an unrelated pre-existing file).
		}

		c.Error = fmt.Errorf("podman could not generate systemd units for the following quadlet(s):\n\n  %s\n\nFix the issue(s) above in the quadlet file and try again",
			strings.Join(problems, "\n  "))
	}
	return c
}

func HandleSystemdRemove(quadctl *util.State, quadlets []*util.Quadlet) ([]Command, error) {
	var targetDir string
	if quadctl.IsRootful {
		targetDir = quadctl.Config.QuadletRootPath
	} else {
		targetDir = quadctl.Config.QuadletUserPath
	}

	commands := []Command{}

	// Ensure any running services are stopped before uninstalling
	cmds, err := HandleSystemdStop(quadctl, quadlets, true)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmds...)

	// Systemd removal is mostly file operations.
	// For file operations, we use golang functions rather than podman, systemd or bash commands ...
	// Encapsulate code to run in a slice of functions that will be executed in a custom command when the command is run.
	funcs := []func(){}
	c := NewCommand(fmt.Sprintf("Removing quadlets from %s", targetDir))
	if quadctl.IsVerbose {
		c.PreFn = func(c *Command) {}
		c.PostFn = func(c *Command) {}
	}

	//If targetDir exists, remove files.
	if info, err := os.Stat(targetDir); err == nil && info.IsDir() {
		if quadctl.Config.UseSymbolicLinks {
			if quadctl.Config.UseSubdirectories {
				//remove link to directory
				link := filepath.Join(targetDir, filepath.Base(quadctl.SearchDir))
				c.Output = append(c.Output, fmt.Sprintf("Removing symbolic link: %s", link))
				f := func() {
					_ = os.Remove(link)
				}
				funcs = append(funcs, f)
			} else {
				//remove individual file links
				for _, q := range quadlets {
					dest := filepath.Join(targetDir, filepath.Base(q.Filepath))
					c.Output = append(c.Output, fmt.Sprintf("Removing symbolic link: %s", dest))
					f := func() {
						if err := os.Remove(dest); err != nil {
							fmt.Fprintf(os.Stderr, "Failed to remove %s: %v\n", dest, err)
						}
					}
					funcs = append(funcs, f)
					// Also remove link to drop-in directory if exists
					dropInDir := dest + ".d"
					if info, err := os.Stat(dropInDir); err == nil && info.IsDir() {
						c.Output = append(c.Output, fmt.Sprintf("Removing symbolic link: %s", dropInDir))
						f := func() {
							if err := os.Remove(dropInDir); err != nil {
								fmt.Fprintf(os.Stderr, "Failed to remove symlink to drop-in dir %s: %v\n", dropInDir, err)
							}
						}
						funcs = append(funcs, f)
					}
				}
			}
		} else {
			if quadctl.Config.UseSubdirectories {
				//remove directory and all files within
				dest := filepath.Join(targetDir, filepath.Base(quadctl.SearchDir))
				c.Output = append(c.Output, fmt.Sprintf("Removing directory and files at: %s", dest))
				f := func() {
					_ = os.RemoveAll(dest)
				}
				funcs = append(funcs, f)
			} else {
				for _, q := range quadlets {
					file := filepath.Join(targetDir, filepath.Base(q.Filepath))
					if info, err := os.Stat(file); err == nil && info.IsDir() {
						c.Output = append(c.Output, fmt.Sprintf("Removing file: %s", file))
						f := func() {
							if err := os.Remove(file); err != nil {
								fmt.Fprintf(os.Stderr, "Failed to remove file %s: %v\n", file, err)
							}
						}
						funcs = append(funcs, f)
					}
				}
			}
		}

		//Expressly remove volume and network resources that might be left behind
		for _, q := range quadlets {
			if q.Type == ".volume" && quadctl.Config.IsRemoveVolumes {
				c.Output = append(c.Output, fmt.Sprintf("Removing volume %s", q.ID))
				var fn func()
				//Default name has systemd- prefix. If non-default name was specified, use it, otherwise use default prefix.
				if volName := util.LastValue(q.Sections["Volume"], "VolumeName"); volName != "" {
					fn = func() {
						_ = runCommandSilently(quadctl.Runner, []string{"podman", "volume", "rm", "-f", volName})
					}
				} else {
					fn = func() {
						_ = runCommandSilently(quadctl.Runner, []string{"podman", "volume", "rm", "-f", "systemd-" + q.ID})
					}
				}
				funcs = append(funcs, fn)
			}
			if q.Type == ".network" && quadctl.Config.IsRemoveNetworks {
				c.Output = append(c.Output, fmt.Sprintf("Removing network %s", q.ID))
				var fn func()
				//Default name has systemd- prefix. If non-default name was specified, use it, otherwise use default prefix.
				if networkName := util.LastValue(q.Sections["Network"], "NetworkName"); networkName != "" {
					fn = func() {
						_ = runCommandSilently(quadctl.Runner, []string{"podman", "network", "rm", "-f", networkName})
					}
				} else {
					fn = func() {
						_ = runCommandSilently(quadctl.Runner, []string{"podman", "network", "rm", "-f", "systemd-" + q.ID})
					}
				}
				funcs = append(funcs, fn)
			}
		}
	}

	// Custom run function that will, when executed by runCommands(), execute the anonymous functions created above.
	c.RunFn = func(c *Command) {
		for _, f := range funcs {
			f()
		}
		if quadctl.IsVerbose {
			fmt.Println(c.Label + "... Done")
			for _, line := range c.Output {
				fmt.Println(" => " + line)
			}
		}
	}

	commands = append(commands, c)

	// Reload systemd to ensure it picks up the changes after removal.
	cmds, err = HandleSystemdReload(quadctl)
	if err != nil {
		return nil, err
	}
	commands = append(commands, cmds...)

	return commands, nil
}
