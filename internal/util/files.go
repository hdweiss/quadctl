package util

import (
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

//go:embed config/quadctl.ini
var files embed.FS

func installDefaultConfig(path string) error {
	fileData, _ := files.ReadFile("config/quadctl.ini")
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

func createDirIfNotExists(path string) error {
	_, err := os.Stat(path)
	if err != nil {
		if err = os.MkdirAll(path, 0770); err != nil {
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

func CopyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := os.Chmod(dst, 0644); err != nil {
		d.Close()
		return err
	}
	defer d.Close()
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

func CopyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	files, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue // Don't handle recursive dirs for drop-ins
		}
		if err := CopyFile(filepath.Join(src, f.Name()), filepath.Join(dst, f.Name())); err != nil {
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
