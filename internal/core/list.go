package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fkmiec/quadctl/internal/quadlet"
	"github.com/jedib0t/go-pretty/v6/list"
)

func HandleList(quadctl *quadlet.State) error {

	if !quadctl.IsListAll {
		absPath := quadctl.Config.QuadletSrcPath
		if quadctl.IsSystemd {
			if quadctl.IsRootful {
				absPath = quadctl.Config.QuadletRootPath
			} else {
				absPath = quadctl.Config.QuadletUserPath
			}
		}
		return listQuadlets(absPath, quadctl.ListDepth)
	}

	// -a walks all three configured paths. They used to be printed as three unlabelled trees
	// with three identical error messages, so there was nothing to say which was which
	// (TODO.md section 4).
	paths := []struct{ role, path string }{
		{"source (quadlet.src.path)", quadctl.Config.QuadletSrcPath},
		{"systemd, rootful (quadlet.root.path)", quadctl.Config.QuadletRootPath},
		{"systemd, rootless (quadlet.user.path)", quadctl.Config.QuadletUserPath},
	}
	var failed []error
	for _, p := range paths {
		fmt.Printf("\n%s\n", p.role)
		if err := listQuadlets(p.path, quadctl.ListDepth); err != nil {
			// One unreadable path shouldn't hide the other two, so report it and carry on.
			fmt.Fprintf(os.Stderr, "  %v\n", err)
			failed = append(failed, err)
		}
	}
	if len(failed) == len(paths) {
		return fmt.Errorf("none of the configured quadlet paths could be listed: %w", errors.Join(failed...))
	}
	return nil
}

func listQuadlets(absPath string, depth int) error {
	// A listing reports on what is there; it does not make anything. The old code created the
	// directory it was about to list - as 0660, with no execute bit, so nothing could be
	// traversed afterwards - which also meant 'quadctl ls -a' as a normal user tried to
	// create /etc/containers/systemd (TODO.md section 2).
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("cannot list %s: %w", absPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("configured quadlet path is not a directory: %s", absPath)
	}

	lw := list.NewWriter()
	lw.SetStyle(list.StyleConnectedRounded)

	// Append the root directory name
	lw.AppendItem(absPath)

	// Start recursive rendering (root is level 1, its children are level 2)
	lw.Indent()
	err = appendDirItems(lw, absPath, 2, depth)
	if err != nil {
		return err
	}
	lw.UnIndent()

	// Output the rendered list
	fmt.Println(lw.Render())
	return nil
}

// appendDirItems recursively traverses the directory and adds items to the list writer.
func appendDirItems(lw list.Writer, currentPath string, level int, depth int) error {
	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		// Skip dot-prefixed entries, as the directory selector already does: .git in a
		// quadlet directory is not a quadlet (TODO.md section 2).
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// Add the current file or directory to the list
		lw.AppendItem(entry.Name())

		// Nest deeper if it's a directory
		lw.Indent()
		if entry.IsDir() && depth > level {
			nextPath := filepath.Join(currentPath, entry.Name())
			if err := appendDirItems(lw, nextPath, level+1, depth); err != nil {
				return err
			}
		}
		lw.UnIndent()
	}

	return nil
}
