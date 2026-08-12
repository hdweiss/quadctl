package quadlet

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveSearchDir turns the optional path argument into the absolute directory to search
// for quadlets. The argument may be a directory or a single quadlet file, given either
// relative to the working directory or as a name under quadlet.src.path; with no argument
// at all the working directory is used.
func ResolveSearchDir(quadctl *State, path string) (string, error) {

	// Determine search directory (optional path or CWD ... optional path may be relative to CWD or quadlets_path from config)
	// If no path is specified, use the current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting current working directory: %w", err)
	}
	// If a path is specified, determine if relative to CWD or quadlet.src.path
	if path != "" {
		// If os.Stat returns no error, the path is absolute or valid relative to the current working directory
		info, err := os.Stat(path)
		if err == nil {
			//if a file was specified, get parent directory of the file
			if !info.IsDir() {
				dir = filepath.Dir(path)
			} else {
				dir = path
			}
		} else {
			// Otherwise, look for specified directory path relative to the quadlets path
			dir = filepath.Join(quadctl.Config.QuadletSrcPath, path)
			// If the path is not found relative to the quadlets path or is not a directory, it's an error
			info, err = os.Stat(dir)
			if err == nil {
				//if a file was specified, get parent directory of the file
				if !info.IsDir() {
					dir = filepath.Dir(dir)
				}
			} else {
				return "", fmt.Errorf("%s not found", path)
			}
		}
		// Always absolutize. Downstream code derives the systemd install subdirectory from
		// filepath.Base(SearchDir); a relative "." here would resolve to the generator root
		// itself and take every unrelated quadlet installed there with it.
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("resolving path %s: %w", dir, err)
		}
		dir = abs
	}

	return dir, nil
}
