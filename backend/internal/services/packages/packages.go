// Package packages implements the download-install model for the style
// market: styles, skills and services install as versioned packages into a
// fixed directory (data/packages/<kind>/<slug>/<version>/), with a registry
// row per install (docs/34).
//
// Two sources, one pipeline:
//   - builtin: the package body is embedded in the binary (catalog.json +
//     per-package directories) and copied out;
//   - url: the package body is a zip fetched through an SSRF-guarded
//     downloader and extracted with path-traversal protection.
// Both produce a staged directory that the same registration dispatch
// consumes, so the kind-specific landing (style profile / skill directory /
// MCP server registration) is source-independent.
package packages

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrAlreadyInstalled marks a re-install of an installed (user, kind, slug);
// the API maps it to 409 so clients can offer uninstall-first.
var ErrAlreadyInstalled = errors.New("package already installed")

// Kinds.
const (
	KindStyle   = "style"
	KindSkill   = "skill"
	KindService = "service"
)

// Sources.
const (
	SourceBuiltin = "builtin"
	SourceURL     = "url"
)

// Limits.
const (
	// MaxDownloadBytes caps a URL package body (zip).
	MaxDownloadBytes = 32 << 20 // 32 MB
	// MaxPackageFiles caps the entries of one package.
	MaxPackageFiles = 200
	// MaxPackageFileBytes caps a single extracted file.
	MaxPackageFileBytes = 8 << 20 // 8 MB
)

var (
	slugPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
	kindPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)
)

// ValidSlug reports whether s is safe as a path segment and identifier.
func ValidSlug(s string) bool { return slugPattern.MatchString(s) }

// ValidVersion reports whether v is safe as a path segment.
func ValidVersion(v string) bool { return versionPattern.MatchString(v) }

// ValidKind reports whether k is a known package kind.
func ValidKind(k string) bool {
	switch k {
	case KindStyle, KindSkill, KindService:
		return true
	}
	return false
}

// Manifest is package.json inside a package body. It declares what the
// package is and where its entry lives; validation is deliberately strict
// because every field becomes a path segment or an identifier.
type Manifest struct {
	Kind        string `json:"kind"`
	Slug        string `json:"slug"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Entry is the kind-specific payload file, relative to the package root:
	//   style   — style config JSON applied to user_style_profiles;
	//   skill   — the SKILL.md-style entry (informational; the whole
	//            directory is the runtime form);
	//   service — an MCP server manifest (command/args/env/url).
	Entry string `json:"entry"`
}

// Validate checks identifiers and path-safety of every manifest field.
func (m Manifest) Validate() error {
	if !ValidKind(m.Kind) {
		return fmt.Errorf("manifest: kind must be one of style|skill|service")
	}
	if !ValidSlug(m.Slug) {
		return fmt.Errorf("manifest: slug must match %s", slugPattern.String())
	}
	if !ValidVersion(m.Version) {
		return fmt.Errorf("manifest: version must match %s", versionPattern.String())
	}
	if m.Entry != "" && !safeRelativePath(m.Entry) {
		return fmt.Errorf("manifest: entry must be a safe relative path")
	}
	return nil
}

// safeRelativePath rejects absolute paths, traversal, and empty segments.
func safeRelativePath(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return false
	}
	for _, segment := range strings.Split(filepath.ToSlash(p), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		if strings.ContainsRune(segment, 0) {
			return false
		}
	}
	return true
}

// InstallRoot resolves the on-disk location of one package version.
// Layout: <root>/<kind>/<slug>/<version>/.
func InstallRoot(root, kind, slug, version string) string {
	return filepath.Join(root, kind, slug, version)
}

// PackageDir resolves the directory of an already-installed package from its
// registry row (install_path when recorded, else the conventional layout).
func PackageDir(root string, kind, slug, version, installPath string) string {
	if installPath != "" && filepath.IsAbs(installPath) {
		return installPath
	}
	return InstallRoot(root, kind, slug, version)
}
