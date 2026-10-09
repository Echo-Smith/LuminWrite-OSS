package packages

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractZip unpacks a zip body into dest with path-traversal protection:
//   - every entry name is rejected when absolute, containing "..", or
//     escaping dest after cleaning (zip slip);
//   - at most MaxPackageFiles entries and MaxPackageFileBytes per file;
//   - only regular files are written (directories are created as needed).
func ExtractZip(body []byte, dest string) error {
	if len(body) == 0 {
		return fmt.Errorf("empty package body")
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("package is not a valid zip: %w", err)
	}
	if len(reader.File) > MaxPackageFiles {
		return fmt.Errorf("package has more than %d entries", MaxPackageFiles)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create install dir: %w", err)
	}
	destClean := filepath.Clean(dest)

	for _, file := range reader.File {
		name := filepath.ToSlash(file.Name)
		if name == "" || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
			return fmt.Errorf("package entry %q has an unsafe name", file.Name)
		}
		target := filepath.Join(destClean, filepath.FromSlash(name))
		if target != destClean && !strings.HasPrefix(target, destClean+string(os.PathSeparator)) {
			return fmt.Errorf("package entry %q escapes the install directory", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create package dir: %w", err)
			}
			continue
		}
		if !file.Mode().IsRegular() {
			continue // skip symlinks and special files
		}
		if file.UncompressedSize64 > MaxPackageFileBytes {
			return fmt.Errorf("package entry %q exceeds %d bytes", file.Name, MaxPackageFileBytes)
		}
		if err := writeZipFile(file, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent dir: %w", err)
	}
	source, err := file.Open()
	if err != nil {
		return fmt.Errorf("open package entry: %w", err)
	}
	defer source.Close()
	out, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create package file: %w", err)
	}
	defer out.Close()
	// Cap the copy at the declared size + 1 so a lying header cannot stream
	// an oversized payload into the install directory.
	if _, err := io.Copy(out, io.LimitReader(source, MaxPackageFileBytes+1)); err != nil {
		return fmt.Errorf("write package file: %w", err)
	}
	return nil
}

// RemoveTree removes a package directory, refusing to touch the install root
// itself (defense against an empty or root-level install_path).
func RemoveTree(root, dir string) error {
	if dir == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	if clean == filepath.Clean(root) || clean == string(os.PathSeparator) {
		return fmt.Errorf("refusing to remove install root %s", clean)
	}
	if !strings.HasPrefix(clean, filepath.Clean(root)+string(os.PathSeparator)) {
		return fmt.Errorf("install path %s is outside the install root", clean)
	}
	return os.RemoveAll(clean)
}
