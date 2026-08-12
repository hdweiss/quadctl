package core

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fkmiec/quadctl/internal/util"

	"github.com/jedib0t/go-pretty/v6/list"
)

func HandleList(quadctl *util.Quadctl) error {

	if !quadctl.IsListAll {
		absPath := quadctl.QuadletSrcPath
		if quadctl.IsSystemd {
			if quadctl.IsRootful {
				absPath = quadctl.QuadletRootPath
			} else {
				absPath = quadctl.QuadletUserPath
			}
		}
		return listQuadlets(absPath, quadctl.ListDepth)
	} else {
		for _, path := range []string{quadctl.QuadletSrcPath, quadctl.QuadletRootPath, quadctl.QuadletUserPath} {
			if err := listQuadlets(path, quadctl.ListDepth); err != nil {
				return fmt.Errorf("listing quadlets in %s: %w", path, err)
			}
		}
		return nil
	}
}

func listQuadlets(absPath string, depth int) error {
	// Verify the path exists and is a directory
	info, err := os.Stat(absPath)
	if err != nil {
		//try to create the directory
		if err = os.MkdirAll(absPath, 0660); err != nil {
			return fmt.Errorf("%s does not exist and could not be created: %w", absPath, err)
		}
	} else if !info.IsDir() {
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
