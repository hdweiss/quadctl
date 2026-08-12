package config

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed quadctl.ini
var files embed.FS

func installDefaultConfig(path string) error {
	fileData, _ := files.ReadFile("quadctl.ini")
	//fmt.Printf("In installDefaultConfig(%s):\n%s\n", path, string(fileData))
	data := map[string]string{}
	data["home"] = os.Getenv("HOME")
	data["user"] = "{{.user}}"

	t, err := template.New("config").Parse(string(fileData))
	if err == nil {
		var f *os.File
		if f, err = os.Create(path); err == nil {
			defer f.Close()
			if err = t.Execute(f, data); err == nil {
				return nil
			}
		}
	}
	//If unsuccessful, write default config to standard out so user can add it manually.
	fmt.Printf("Writing default config.ini contents to standard out. Replace {{.home}} with user home directory.\n  DO NOT replace {{.user}} template variable.\n\n%s\n", string(fileData))
	return fmt.Errorf("unable to create default config.ini at %s: %w", path, err)
}

// dirMode is the mode quadctl creates directories with. A directory needs its execute bit to
// be traversable at all, which is what made the 0660 in the old listing code produce a
// directory nothing could look inside.
const dirMode = 0755

func createDirIfNotExists(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if err = os.MkdirAll(path, dirMode); err != nil {
			return err
		}
	}
	return nil
}

func WriteFile(path string, text string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	//fmt.Printf("WriteFile %s with:\n%s\n", path, text)
	_, err = f.WriteString(text)

	return err
}

// CopyFile copies src to dst, giving dst the source file's permissions. The mode used to be
// hardcoded 0644, which both discarded a deliberately restrictive one - an .env file sitting
// next to the quadlets became world-readable in the generator directory - and silently
// dropped the execute bit from anything meant to be run.
func CopyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer d.Close()
	// O_CREATE only applies the mode to a file that did not exist, so set it explicitly:
	// overwriting an installed copy has to update its permissions too.
	if err := d.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	_, err = io.Copy(d, s)
	return err
}

func DeleteFile(path string) error {
	f, _ := os.Stat(path)
	if f != nil {
		return os.Remove(path)
	}
	return nil
}

// CopyDir copies src to dst recursively, preserving directory and file permissions. It used
// to skip subdirectories, so anything a quadlet directory kept alongside its files - a
// drop-in directory, a config/ folder that gets bind-mounted - was silently left behind when
// the directory was installed under systemd.
func CopyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	// MkdirAll applies the umask, and does nothing at all when the directory already exists.
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcPath, dstPath := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := CopyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := CopyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

func ListSubdirectories(path string) ([]string, error) {
	var directories []string
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		directories = append(directories, entry.Name())
	}
	return directories, nil
}

func ListFiles(path string) ([]string, error) {
	var files []string
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	return files, nil
}
