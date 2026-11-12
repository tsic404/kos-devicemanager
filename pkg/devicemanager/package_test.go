package devicemanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kos-sdk/packaging"
	"github.com/tsic404/kosd/pkg/extension"
)

// TestPackageVerifies checks that the artifact carries the declaration and the
// digest that covers it (KOSD-C-17, SDK-C-4).
func TestPackageVerifies(t *testing.T) {
	pkg, archive, err := Build(nil)
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	verified, err := packaging.Verify(archive, pkg.Digest)
	if err != nil {
		t.Fatalf("verify package: %v", err)
	}
	if verified.Manifest.Identifier != Identifier || verified.Manifest.Version != Version {
		t.Fatalf("package declares %s@%s, want %s@%s",
			verified.Manifest.Identifier, verified.Manifest.Version, Identifier, Version)
	}
	if len(verified.Manifest.Contributions.Pages) != 1 {
		t.Fatalf("package pages = %+v, want the device page", verified.Manifest.Contributions.Pages)
	}
	wrong := packaging.Digest("sha256:" + strings.Repeat("0", 64))
	if _, err := packaging.Verify(archive, wrong); !errors.Is(err, packaging.ErrDigestMismatch) {
		t.Fatalf("a package verified against a digest it does not carry: %v", err)
	}
}

// TestPackagedDeclarationParsesInTheKernel checks the install entry: the
// manifest.json the package carries decodes with the kernel's own reader, so
// the artifact is installable instead of being refused field by field
// (KOSD-C-17, KOSD-C-18).
func TestPackagedDeclarationParsesInTheKernel(t *testing.T) {
	_, archive, err := Build(nil)
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	parsed, err := extension.Parse(carriedManifest(t, archive))
	if err != nil {
		t.Fatalf("the kernel refused the packaged manifest: %v", err)
	}
	if parsed.Identifier != Identifier || parsed.Version != Version {
		t.Fatalf("the kernel read %s@%s, want %s@%s", parsed.Identifier, parsed.Version, Identifier, Version)
	}
	if len(parsed.Contributions.Pages) != 1 {
		t.Fatalf("pages = %+v, want the device page", parsed.Contributions.Pages)
	}
	page := parsed.Contributions.Pages[0]
	if page.ID != PageDevices || !page.ReadOnly || page.ReadOnlyReason != ReadOnlyReason {
		t.Fatalf("page = %+v, want the read-only device page with its reason", page)
	}
	if got, want := len(parsed.Permissions), len(Declaration().Permissions); got != want {
		t.Fatalf("the artifact declares %d permissions, the loadable declaration %d", got, want)
	}
	if got, want := readScopeOfKernel(parsed.Permissions), readScopeOfKernel(Declaration().Permissions); got != want {
		t.Fatalf("the artifact declares read scope %s, the declaration %s", got, want)
	}

	// Decoding is the install entry; activation is the class loader. The
	// packaged declaration must pass both, or the component never reaches Ready
	// (KOSD-C-17, KOSD-C-28).
	loaded, err := parsed.Load()
	if err != nil {
		t.Fatalf("the kernel refused to load the packaged declaration: %v", err)
	}
	if len(loaded.Problems) != 0 {
		t.Fatalf("the packaged declaration has problems = %+v, want none", loaded.Problems)
	}
	if len(loaded.Contributions.Pages) != 1 || loaded.Contributions.Pages[0].ID != PageDevices {
		t.Fatalf("loaded pages = %+v, want the device page", loaded.Contributions.Pages)
	}
}

// TestManifestStatesTheLoadedPage checks that the artifact and the loaded
// contribution name the same page: one definition serves both (SDK-C-3).
func TestManifestStatesTheLoadedPage(t *testing.T) {
	pkg, _, err := Build(nil)
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	packaged, err := json.Marshal(pkg.Manifest.Contributions.Pages)
	if err != nil {
		t.Fatalf("encode packaged pages: %v", err)
	}
	loaded, err := json.Marshal([]extension.Page{DevicesPage()})
	if err != nil {
		t.Fatalf("encode loaded page: %v", err)
	}
	var packagedValue, loadedValue any
	if err := json.Unmarshal(packaged, &packagedValue); err != nil {
		t.Fatalf("decode packaged pages: %v", err)
	}
	if err := json.Unmarshal(loaded, &loadedValue); err != nil {
		t.Fatalf("decode loaded page: %v", err)
	}
	left, _ := json.Marshal(packagedValue)
	right, _ := json.Marshal(loadedValue)
	if string(left) != string(right) {
		t.Fatalf("packaged page %s, loaded page %s", left, right)
	}
}

// TestManifestReadScopeMatchesTheDeclaration checks that the package and the
// running contribution state one read scope, including the core group, so no
// read is stated only in one of the two (KOSD-C-18, SDK-C-3).
func TestManifestReadScopeMatchesTheDeclaration(t *testing.T) {
	declaration, err := Manifest()
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(declaration.Permissions) != 1 {
		t.Fatalf("manifest permissions = %+v, want the device page entry", declaration.Permissions)
	}
	permission := declaration.Permissions[0]
	if permission.Contribution != PageDevices {
		t.Fatalf("manifest permission covers %q, want %q", permission.Contribution, PageDevices)
	}
	if got, want := readScopeOfManifest(declaration.Permissions), readScopeOfKernel(Declaration().Permissions); got != want {
		t.Fatalf("manifest read scope %s, declaration read scope %s", got, want)
	}
}

// carriedManifest returns the manifest.json bytes one package archive carries.
func carriedManifest(t *testing.T, archive []byte) []byte {
	t.Helper()
	decompressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("read package archive: %v", err)
	}
	defer decompressed.Close()
	reader := tar.NewReader(decompressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			t.Fatal("the package carries no manifest.json")
		}
		if err != nil {
			t.Fatalf("read package entry: %v", err)
		}
		if header.Name != packaging.ManifestEntry {
			continue
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read manifest.json: %v", err)
		}
		return content
	}
}

// readScopeOfManifest renders the manifest's read scope as a comparable string.
func readScopeOfManifest(permissions []manifest.ExtensionPermission) string {
	groups := make([]string, 0)
	kinds := make([]string, 0)
	for _, permission := range permissions {
		groups = append(groups, permission.ReadScope.Groups...)
		for _, kind := range permission.ReadScope.Kinds {
			kinds = append(kinds, kind.Group+"/"+kind.Kind)
		}
	}
	return canonicalScope(groups, kinds)
}

// readScopeOfKernel renders the kernel declaration's read scope as a comparable
// string.
func readScopeOfKernel(permissions []extension.Permission) string {
	groups := make([]string, 0)
	kinds := make([]string, 0)
	for _, permission := range permissions {
		groups = append(groups, permission.ReadScope.Groups...)
		for _, kind := range permission.ReadScope.Kinds {
			kinds = append(kinds, kind.Group+"/"+kind.Kind)
		}
	}
	return canonicalScope(groups, kinds)
}

func canonicalScope(groups, kinds []string) string {
	sortedGroups := append([]string(nil), groups...)
	sortedKinds := append([]string(nil), kinds...)
	sort.Strings(sortedGroups)
	sort.Strings(sortedKinds)
	return "groups=" + strings.Join(sortedGroups, ",") + " kinds=" + strings.Join(sortedKinds, ",")
}
