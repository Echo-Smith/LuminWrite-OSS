package packages

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── SSRF guard ───────────────────────────────────────────

func TestIsPublicIPRejectsNonRoutable(t *testing.T) {
	rejected := []string{
		"127.0.0.1", "127.8.9.1",          // loopback
		"10.1.2.3", "192.168.1.10",        // private
		"172.16.0.1", "172.31.255.255",    // private
		"169.254.169.254",                // link-local (cloud metadata)
		"0.0.0.0",                        // unspecified
		"100.64.0.1",                     // CGNAT
		"192.0.2.5",                      // TEST-NET-1
		"198.51.100.7",                   // TEST-NET-2
		"203.0.113.9",                    // TEST-NET-3
		"198.18.0.5",                     // benchmarking
		"240.0.0.1",                      // reserved
		"224.0.0.1",                      // multicast
		"::1", "fe80::1", "fc00::1",      // IPv6 loopback/link-local/ULA
	}
	for _, addr := range rejected {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("unparseable test address %q", addr)
		}
		if isPublicIP(ip) {
			t.Fatalf("isPublicIP(%s) = true, want false", addr)
		}
	}
	for _, addr := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		if !isPublicIP(net.ParseIP(addr)) {
			t.Fatalf("isPublicIP(%s) = false, want true", addr)
		}
	}
}

func TestFetchPackageRejectsSchemesAndHosts(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"loopback host", "http://127.0.0.1:9/pkg.zip"},
		{"localhost host", "http://localhost:9/pkg.zip"},
		{"metadata endpoint", "http://169.254.169.254/latest/meta-data"},
		{"private host", "http://192.168.1.5/pkg.zip"},
		{"no host", "http:///pkg.zip"},
	}
	for _, tc := range cases {
		if _, err := FetchPackage(tc.url); err == nil {
			t.Fatalf("%s: FetchPackage(%q) succeeded, want rejection", tc.name, tc.url)
		}
	}
}

func TestFetchPackageRejectsRedirectToPrivate(t *testing.T) {
	// A public-looking host that redirects into loopback must be rejected at
	// the hop (the redirect target is re-validated).
	var target string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/evil.zip", http.StatusFound)
	}))
	defer server.Close()
	target = server.URL
	if _, err := FetchPackage(target + "/pkg.zip"); err == nil {
		t.Fatal("redirect into loopback was not rejected")
	}
}

// ─── Zip extraction safety ─────────────────────────────────

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, content := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractZipBlocksTraversal(t *testing.T) {
	body := buildZip(t, map[string]string{
		"package.json":     `{"kind":"skill","slug":"evil","version":"1.0.0"}`,
		"../../escape.txt": "pwned",
	})
	dest := filepath.Join(t.TempDir(), "pkg")
	if err := ExtractZip(body, dest); err == nil {
		t.Fatal("zip slip entry was accepted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escape.txt")); err == nil {
		t.Fatal("zip slip wrote outside the install directory")
	}
}

func TestExtractZipBlocksOversizedEntry(t *testing.T) {
	big := strings.Repeat("x", MaxPackageFileBytes+1)
	body := buildZip(t, map[string]string{"big.txt": big})
	dest := filepath.Join(t.TempDir(), "pkg")
	if err := ExtractZip(body, dest); err == nil {
		t.Fatal("oversized entry was accepted")
	}
}

// ─── Manifest validation ───────────────────────────────────

func TestManifestValidate(t *testing.T) {
	valid := Manifest{Kind: KindStyle, Slug: "writer-starter", Version: "1.0.0", Entry: "style.json"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	bad := []Manifest{
		{Kind: "bogus", Slug: "s", Version: "1"},
		{Kind: KindStyle, Slug: "../escape", Version: "1"},
		{Kind: KindStyle, Slug: "s", Version: "../../etc"},
		{Kind: KindStyle, Slug: "s", Version: "1", Entry: "/absolute.json"},
		{Kind: KindStyle, Slug: "s", Version: "1", Entry: "../../escape.json"},
	}
	for _, m := range bad {
		if err := m.Validate(); err == nil {
			t.Fatalf("invalid manifest accepted: %+v", m)
		}
	}
}

// ─── Builtin catalog + install ─────────────────────────────

func TestBuiltinCatalogAndInstall(t *testing.T) {
	root := t.TempDir()
	// Nil stores: skill installs are directory-only, so no dependency is hit.
	installer := NewInstaller(root, nil, nil, nil)

	catalog, err := BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Packages) == 0 {
		t.Fatal("builtin catalog is empty")
	}

	// Skill package: registry is nil → expect the explicit registry error,
	// which proves staging and manifest validation ran before registration.
	_, err = installer.Install(context.Background(), "user-1", InstallRequest{Source: SourceBuiltin, Slug: "knowledge-base"})
	if err == nil || !strings.Contains(err.Error(), "registry") {
		t.Fatalf("install without registry: got %v", err)
	}

	// Unknown slug fails before staging.
	if _, err := installer.Install(context.Background(), "user-1", InstallRequest{Source: SourceBuiltin, Slug: "nope"}); err == nil {
		t.Fatal("unknown builtin slug installed")
	}
}

func TestRemoveTreeGuards(t *testing.T) {
	root := t.TempDir()
	if err := RemoveTree(root, root); err == nil {
		t.Fatal("removing the install root was allowed")
	}
	if err := RemoveTree(root, "/etc"); err == nil {
		t.Fatal("removing a path outside the root was allowed")
	}
	dir := filepath.Join(root, "skill", "x", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(root, dir); err != nil {
		t.Fatalf("legitimate removal failed: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("directory still present after removal")
	}
}

// ─── Builtin body sanity ───────────────────────────────────

func TestBuiltinPackageBodiesAreWellFormed(t *testing.T) {
	catalog, err := BuiltinCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog.Packages {
		source := filepath.Join("builtin", entry.Kind, entry.Slug, entry.Version)
		data, err := builtinFS.ReadFile(filepath.Join(source, "package.json"))
		if err != nil {
			t.Fatalf("%s: package.json missing: %v", entry.Slug, err)
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("%s: package.json invalid: %v", entry.Slug, err)
		}
		if err := manifest.Validate(); err != nil {
			t.Fatalf("%s: manifest invalid: %v", entry.Slug, err)
		}
		if manifest.Kind != entry.Kind || manifest.Slug != entry.Slug || manifest.Version != entry.Version {
			t.Fatalf("%s: manifest/catalog mismatch: %+v vs %+v", entry.Slug, manifest, entry)
		}
		if manifest.Entry != "" {
			if _, err := builtinFS.ReadFile(filepath.Join(source, filepath.FromSlash(manifest.Entry))); err != nil {
				t.Fatalf("%s: entry %q missing: %v", entry.Slug, manifest.Entry, err)
			}
		}
	}
}
