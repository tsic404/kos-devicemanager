// Package kosdtc157 verifies that a preinstalled system application travels the
// same channel as a third-party extension: one package format, one install
// path, no field that names a preinstall source, and the same install and
// uninstall result for both (KOSD-AC-72, KOSD-TC-157).
package kosdtc157

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/tsic404/kos-sdk/packaging"
	"github.com/tsic404/kos-sdk/validate"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

const thirdPartyID = "example-device-widgets"

func TestPreinstalledProductTravelsTheThirdPartyChannel(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "g0", Model: "A100", Kind: "gpu"})
	clusterBefore := cluster.Snapshot(t)
	now := func() time.Time { return collected.Add(30 * time.Second) }
	session := harness.Session(write.RoleOwner)

	// The first-party artifact is built by the SDK's one packaging path.
	ours, ourArchive, err := devicemanager.Build(nil)
	if err != nil {
		t.Fatalf("build the product package: %v", err)
	}
	if ours.Manifest.Identifier != devicemanager.Identifier || ours.Manifest.Version != devicemanager.Version {
		t.Fatalf("package names %s@%s, want %s@%s",
			ours.Manifest.Identifier, ours.Manifest.Version, devicemanager.Identifier, devicemanager.Version)
	}
	verified, err := packaging.Verify(ourArchive, ours.Digest)
	if err != nil {
		t.Fatalf("verify the product package: %v", err)
	}
	if verified.Digest != ours.Digest {
		t.Fatalf("verified digest %s, want %s", verified.Digest, ours.Digest)
	}

	// The kernel reads the artifact itself: the packaged manifest.json, not a
	// declaration the case hands it, names the product. A package the kernel
	// refuses never reaches the device page (KOSD-C-17, KOSD-C-18).
	artifact, err := extension.Read(ourArchive)
	if err != nil {
		t.Fatalf("the kernel refused the product artifact: %v", err)
	}
	if artifact.Declaration.Identifier != devicemanager.Identifier {
		t.Fatalf("the kernel read %q from the artifact, want %q",
			artifact.Declaration.Identifier, devicemanager.Identifier)
	}
	if artifact.Digest != string(ours.Digest) {
		t.Fatalf("the kernel read digest %s, want %s", artifact.Digest, ours.Digest)
	}

	// No field names a preinstall channel: a declaration that carries one is
	// refused, so a distribution cannot mark its preinstalled copy.
	document, err := devicemanager.Document()
	if err != nil {
		t.Fatalf("read the product declaration: %v", err)
	}
	document["preinstalled"] = true
	if _, _, err := packaging.Pack(document, nil); err == nil {
		t.Fatal("a declaration naming a preinstall channel was accepted")
	} else if got, want := unknownFields(err), []string{"$.preinstalled"}; !harness.EqualStrings(got, want) {
		t.Fatalf("a declaration naming a preinstall channel failed at %v, want %v", got, want)
	}

	// A third-party extension packs through that same path.
	thirdParty, thirdPartyArchive, err := packaging.Pack(thirdPartyDocument(), nil)
	if err != nil {
		t.Fatalf("build the third-party package: %v", err)
	}
	if _, err := packaging.Verify(thirdPartyArchive, thirdParty.Digest); err != nil {
		t.Fatalf("verify the third-party package: %v", err)
	}
	if got, want := entries(t, ourArchive), entries(t, thirdPartyArchive); !harness.EqualStrings(got, want) {
		t.Fatalf("package entries %v, want the third-party structure %v", got, want)
	}

	// Both install through one path. The component object name differs from the
	// identifier, so the kernel identifies the product by its artifact.
	firstParty := harness.Component{Name: "device-view-bootstrap", Declaration: devicemanager.Declaration(), Created: collected}
	third := harness.Component{Name: "example-labs", Declaration: thirdPartyDeclaration(), Created: collected}
	first := harness.Install(t, firstParty, third)

	installed := componentsOf(first)
	bootstrap, known := installed["device-view-bootstrap"]
	if !known {
		t.Fatal("the preinstalled component is missing from the install result")
	}
	if bootstrap.Identifier != devicemanager.Identifier {
		t.Fatalf("the component identified itself as %q, want %q from its artifact", bootstrap.Identifier, devicemanager.Identifier)
	}
	if bootstrap.Phase != extension.PhaseReady || !bootstrap.HasVerifiedPackage {
		t.Fatalf("the preinstalled product is %s (%s), want a ready verified component", bootstrap.Phase, bootstrap.Reason)
	}
	contributed := installed["example-labs"]
	if contributed.Phase != extension.PhaseReady || !contributed.HasVerifiedPackage {
		t.Fatalf("the third-party extension is %s (%s), want a ready verified component", contributed.Phase, contributed.Reason)
	}
	for _, pageID := range []string{devicemanager.PageDevices, thirdPartyPage} {
		if !listedPage(first, pageID) {
			t.Fatalf("page %q is not listed after both installs", pageID)
		}
	}
	firstPages := pagesOf(t, first, "device-view-bootstrap")
	extensionPages := pagesOf(t, first, "example-labs")

	// Each product withdraws on its own uninstall and leaves the other alone.
	withoutOurs := harness.Install(t, third)
	if listedPage(withoutOurs, devicemanager.PageDevices) {
		t.Fatal("the uninstalled product's page is still listed")
	}
	if !listedPage(withoutOurs, thirdPartyPage) {
		t.Fatal("uninstalling the system application removed the third-party page")
	}
	if got := pagesOf(t, withoutOurs, "example-labs"); !equalPages(got, extensionPages) {
		t.Fatalf("uninstalling the system application changed the third-party result: %+v, want %+v", got, extensionPages)
	}

	withoutThird := harness.Install(t, firstParty)
	if listedPage(withoutThird, thirdPartyPage) {
		t.Fatal("the uninstalled third-party page is still listed")
	}
	if !listedPage(withoutThird, devicemanager.PageDevices) {
		t.Fatal("uninstalling the third-party extension removed the system application's page")
	}
	if got := pagesOf(t, withoutThird, "device-view-bootstrap"); !equalPages(got, firstPages) {
		t.Fatalf("uninstalling the third-party extension changed the system application's result: %+v, want %+v", got, firstPages)
	}

	// The direct read is refused for either removed page, and only for it.
	second := withoutOurs
	service := harness.Shell(second, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	if _, failure := service.Read(context.Background(), session, devicemanager.PageDevices); failure == nil {
		t.Fatal("the uninstalled product's page is still readable")
	} else if failure.Code != controlplane.CodeNotFound {
		t.Fatalf("the removed page was refused with %q, want %q", failure.Code, controlplane.CodeNotFound)
	}

	// Reinstalling from the same artifact restores exactly the first result.
	restored := harness.Install(t, firstParty, third)
	if !listedPage(restored, devicemanager.PageDevices) {
		t.Fatal("the reinstalled product's page is not listed")
	}
	if got := pagesOf(t, restored, "device-view-bootstrap"); !equalPages(got, firstPages) {
		t.Fatalf("reinstall result %+v, want the first install %+v", got, firstPages)
	}
	if got := pagesOf(t, restored, "example-labs"); !equalPages(got, extensionPages) {
		t.Fatalf("reinstall disturbed the third-party result: %+v, want %+v", got, extensionPages)
	}
	if clusterAfter := cluster.Snapshot(t); !harness.SameSnapshot(clusterBefore, clusterAfter) {
		t.Fatal("installing or uninstalling changed the cluster objects")
	}
}

// thirdPartyPage is the page the third-party fixture declares.
const thirdPartyPage = "widgets"

// thirdPartyDocument is the fixture extension's declaration document. It is
// the field set the framework fixes, and goes through the same SDK checks as
// the product's own declaration (KOSD-C-18).
func thirdPartyDocument() map[string]any {
	return map[string]any{
		"identifier":       thirdPartyID,
		"version":          "1.0.0",
		"frameworkVersion": extension.FrameworkVersion,
		"contributions": map[string]any{
			"pages": []any{map[string]any{
				"id":             thirdPartyPage,
				"title":          "部件",
				"location":       "panel",
				"readOnly":       true,
				"readOnlyReason": "示例页只读：没有修改入口",
				"items": []any{map[string]any{
					"id": "note", "label": "说明", "source": "local", "store": "example-widgets-note",
				}},
			}},
		},
		"dependencies":  map[string]any{},
		"permissions":   []any{},
		"configuration": map[string]any{},
		"retention":     []any{},
	}
}

// thirdPartyDeclaration is the fixture's runtime declaration, the same
// contribution the document above states.
func thirdPartyDeclaration() extension.Declaration {
	return extension.Declaration{
		Identifier:       thirdPartyID,
		Version:          "1.0.0",
		FrameworkVersion: extension.FrameworkVersion,
		Contributions: extension.Contributions{Pages: []extension.Page{{
			ID:             thirdPartyPage,
			Title:          "部件",
			Location:       "panel",
			ReadOnly:       true,
			ReadOnlyReason: "示例页只读：没有修改入口",
			Items: []extension.PageItem{{
				ID: "note", Label: "说明", Source: extension.PageSourceLocal, Store: "example-widgets-note",
			}},
		}}},
	}
}

// pageShape is one installed page as a case compares it.
type pageShape struct {
	ID             string
	ReadOnly       bool
	ReadOnlyReason string
	Items          []string
}

// pagesOf lists the pages one component activated, ordered by identity.
func pagesOf(t *testing.T, assessment extension.Assessment, component string) []pageShape {
	t.Helper()
	result, known := componentsOf(assessment)[component]
	if !known {
		t.Fatalf("component %s is missing from the install result", component)
	}
	shapes := make([]pageShape, 0, len(result.Contributions.Pages))
	for _, page := range result.Contributions.Pages {
		items := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, item.ID+"/"+item.Source)
		}
		sort.Strings(items)
		shapes = append(shapes, pageShape{
			ID: page.ID, ReadOnly: page.ReadOnly, ReadOnlyReason: page.ReadOnlyReason, Items: items,
		})
	}
	sort.Slice(shapes, func(left, right int) bool { return shapes[left].ID < shapes[right].ID })
	return shapes
}

// componentsOf indexes the install result by component object name.
func componentsOf(assessment extension.Assessment) map[string]extension.ComponentResult {
	results := make(map[string]extension.ComponentResult, len(assessment.Components))
	for _, result := range assessment.Components {
		results[result.Name] = result
	}
	return results
}

// listedPage reports whether the panel lists one page.
func listedPage(assessment extension.Assessment, id string) bool {
	for _, page := range assessment.Registry.Pages() {
		if page.ID == id {
			return true
		}
	}
	return false
}

// entries lists the archive member names of one package.
func entries(t *testing.T, archive []byte) []string {
	t.Helper()
	decompressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("read package archive: %v", err)
	}
	defer decompressed.Close()
	names := make([]string, 0)
	reader := tar.NewReader(decompressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read package entry: %v", err)
		}
		names = append(names, header.Name)
	}
	sort.Strings(names)
	return names
}

// unknownFields lists the sites the SDK refused as unknown fields, ordered as
// it reported them.
func unknownFields(err error) []string {
	var invalid *validate.InvalidError
	if !errors.As(err, &invalid) {
		return nil
	}
	paths := make([]string, 0)
	for _, issue := range invalid.Issues {
		if issue.Code == validate.CodeUnknownField {
			paths = append(paths, issue.Path)
		}
	}
	return paths
}

func equalPages(left, right []pageShape) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].ReadOnly != right[index].ReadOnly {
			return false
		}
		if left[index].ReadOnlyReason != right[index].ReadOnlyReason {
			return false
		}
		if !harness.EqualStrings(left[index].Items, right[index].Items) {
			return false
		}
	}
	return true
}
