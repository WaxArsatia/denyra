package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	source := flag.String("source", "", "verified .sflx artifact")
	destination := flag.String("destination", "", "installed extension directory")
	flag.Parse()
	if err := extract(*source, *destination); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func extract(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("source and destination are required")
	}
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	for _, entry := range reader.File {
		clean := filepath.Clean(entry.Name)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe extension archive path %q", entry.Name)
		}
		target := filepath.Join(destination, clean)
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !entry.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("non-regular extension entry %q", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := entry.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o444)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
	}
	for _, required := range []string{"manifest.json", "index.js"} {
		info, err := os.Stat(filepath.Join(destination, required))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("extension archive lacks %s", required)
		}
	}
	return nil
}
