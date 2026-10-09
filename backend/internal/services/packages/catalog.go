package packages

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// builtinFS embeds the builtin catalog and package bodies. Bodies are
// directories (not zips) so a builtin install copies them out; the URL
// source produces the same staged shape via zip extraction, and both feed
// the same registration dispatch.
//
//go:embed builtin/catalog.json builtin/style builtin/skill
var builtinFS embed.FS

// builtinCatalog loads the embedded catalog document.
func builtinCatalog() (*Catalog, error) {
	data, err := builtinFS.ReadFile("builtin/catalog.json")
	if err != nil {
		return nil, fmt.Errorf("read builtin catalog: %w", err)
	}
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse builtin catalog: %w", err)
	}
	return &catalog, nil
}

// BuiltinCatalog returns the builtin catalog for the API surface.
func BuiltinCatalog() (*Catalog, error) { return builtinCatalog() }

// builtinBySlug resolves a catalog entry by slug.
func builtinBySlug(slug string) (CatalogEntry, bool) {
	catalog, err := builtinCatalog()
	if err != nil {
		return CatalogEntry{}, false
	}
	for _, entry := range catalog.Packages {
		if entry.Slug == slug {
			return entry, true
		}
	}
	return CatalogEntry{}, false
}

// copyBuiltin copies an embedded package body into dest.
func copyBuiltin(kind, slug, version, dest string) error {
	source := fmt.Sprintf("builtin/%s/%s/%s", kind, slug, version)
	if _, err := fs.Stat(builtinFS, source); err != nil {
		return fmt.Errorf("builtin package %s/%s@%s not found", kind, slug, version)
	}
	return fs.WalkDir(builtinFS, source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dest, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := builtinFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
