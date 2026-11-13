// Package kosdtc155 verifies that a system application is an independent
// extension artifact: every product on the system application list installs on
// its own, its contributions match its declaration, and uninstalling one leaves
// the other products and the cluster objects untouched (KOSD-AC-72,
// KOSD-TC-155).
package kosdtc155

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/app/kostaskmanager"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/extension/kosupdater"
	"github.com/tsic404/kosd/pkg/volume/kosstorage"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

func TestSystemApplicationsInstallAndUninstallOnTheirOwn(t *testing.T) {
	declarations := map[string]extension.Declaration{
		devicemanager.Identifier:  devicemanager.Declaration(),
		kosupdater.Identifier:     kosupdater.Declaration(),
		kosstorage.Identifier:     kosstorage.Declaration(),
		kostaskmanager.Identifier: kostaskmanager.Declaration(),
	}

	cluster := harness.Start()
	cluster.Node(t, "node-a")
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "g0", Model: "A100", Kind: "gpu"})
	clusterBefore := cluster.Snapshot(t)

	components := make([]harness.Component, 0, len(declarations))
	for name, declaration := range declarations {
		components = append(components, harness.Component{Name: name, Declaration: declaration})
	}
	assessment := harness.Install(t, components...)

	// Every product installs on its own and activates exactly what it declared.
	for _, result := range assessment.Components {
		declaration, known := declarations[result.Name]
		if !known {
			t.Fatalf("component %s was not part of the case", result.Name)
		}
		if result.Phase != extension.PhaseReady {
			t.Fatalf("component %s is %s (%s: %s), want %s",
				result.Name, result.Phase, result.Reason, result.Message, extension.PhaseReady)
		}
		if !result.HasVerifiedPackage {
			t.Fatalf("component %s installed without a verified package", result.Name)
		}
		if got, want := pageIDs(result.Contributions.Pages), pageIDs(declaration.Contributions.Pages); !harness.EqualStrings(got, want) {
			t.Fatalf("component %s activated pages %v, want the declared %v", result.Name, got, want)
		}
		if got, want := len(result.Contributions.Controllers), len(declaration.Contributions.Controllers); got != want {
			t.Fatalf("component %s activated %d controllers, want the declared %d", result.Name, got, want)
		}
		if got, want := len(result.Contributions.Settings), len(declaration.Contributions.Settings); got != want {
			t.Fatalf("component %s activated %d settings, want the declared %d", result.Name, got, want)
		}
	}

	listedBefore := pageIDs(assessment.Registry.Pages())
	if want := everyDeclaredPage(declarations); !harness.EqualStrings(listedBefore, want) {
		t.Fatalf("the panel lists %v, want the declared pages %v", listedBefore, want)
	}

	// Uninstalling the device view leaves the other products in place.
	remaining := make([]harness.Component, 0, len(components)-1)
	for _, component := range components {
		if component.Name != devicemanager.Identifier {
			remaining = append(remaining, component)
		}
	}
	after := harness.Install(t, remaining...)

	listedAfter := pageIDs(after.Registry.Pages())
	if want := harness.Without(listedBefore, devicemanager.PageDevices); !harness.EqualStrings(listedAfter, want) {
		t.Fatalf("pages after uninstalling the device view = %v, want %v", listedAfter, want)
	}
	for _, result := range after.Components {
		if result.Phase != extension.PhaseReady {
			t.Fatalf("component %s left at %s after the uninstall", result.Name, result.Phase)
		}
	}
	if clusterAfter := cluster.Snapshot(t); !harness.SameSnapshot(clusterBefore, clusterAfter) {
		t.Fatal("installing or uninstalling products changed the cluster objects")
	}

	// The removed page is not readable while its component is gone.
	now := func() time.Time { return collected.Add(30 * time.Second) }
	service := harness.Shell(after, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	if _, failure := service.Read(context.Background(), harness.Session(write.RoleOwner), devicemanager.PageDevices); failure == nil {
		t.Fatal("the page of an uninstalled product is still readable")
	} else if failure.Code != controlplane.CodeNotFound {
		t.Fatalf("the removed page was refused with %q, want %q", failure.Code, controlplane.CodeNotFound)
	}
}

// pageIDs lists the page identities of one contribution set, ordered.
func pageIDs(pages []extension.Page) []string {
	ids := make([]string, 0, len(pages))
	for _, page := range pages {
		ids = append(ids, page.ID)
	}
	sort.Strings(ids)
	return ids
}

// everyDeclaredPage lists every page the case's declarations name, ordered.
func everyDeclaredPage(declarations map[string]extension.Declaration) []string {
	ids := make([]string, 0)
	for _, declaration := range declarations {
		ids = append(ids, pageIDs(declaration.Contributions.Pages)...)
	}
	sort.Strings(ids)
	return ids
}
