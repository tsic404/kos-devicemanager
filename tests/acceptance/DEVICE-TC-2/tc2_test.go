// Package devicet2 verifies that the device page follows its component: while
// the component runs the page is listed, a disable request withdraws the entry
// and the direct read is refused, and re-enabling brings the entry back without
// touching another product's pages (DEVICE-AC-1, DEVICE-TC-2, DASH-C-22).
package devicet2

import (
	"context"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/extension/kosupdater"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// withdrawn refuses to resolve any item: a withdrawn page must be refused before
// its source is read, so a case that reaches it has a defect.
type withdrawn struct{ t *testing.T }

func (w withdrawn) Read(context.Context, identity.Session, extension.PageItem) (pages.Reading, *controlplane.Error) {
	w.t.Fatal("the shell resolved an item of a withdrawn page")
	return pages.Reading{}, nil
}

func TestDevicePageFollowsItsComponent(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "g0", Model: "A100", Kind: "gpu"})
	now := func() time.Time { return collected.Add(30 * time.Second) }
	session := harness.Session(write.RoleOwner)

	device := harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()}
	updater := harness.Component{Name: "kos-updater", Declaration: kosupdater.Declaration()}

	enabled := harness.Install(t, device, updater)
	service := harness.Shell(enabled, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	before := harness.PageIDs(service, write.RoleOwner)
	if !harness.Contains(before, devicemanager.PageDevices) || !harness.Contains(before, kosupdater.PageComponents) {
		t.Fatalf("listed pages before disable = %v, want the device and component pages", before)
	}
	if _, failure := service.Read(context.Background(), session, devicemanager.PageDevices); failure != nil {
		t.Fatalf("the enabled device page is unreadable: %v", failure)
	}

	disabled := device
	disabled.IsDisabled = true
	assessment := harness.Install(t, disabled, updater)
	service = harness.Shell(assessment, withdrawn{t: t}, now)

	after := harness.PageIDs(service, write.RoleOwner)
	if harness.Contains(after, devicemanager.PageDevices) {
		t.Fatalf("pages after disable = %v, want no device page", after)
	}
	if want := harness.Without(before, devicemanager.PageDevices); !harness.EqualStrings(after, want) {
		t.Fatalf("pages after disable = %v, want the other product's pages unchanged at %v", after, want)
	}

	_, failure := service.Read(context.Background(), session, devicemanager.PageDevices)
	if failure == nil {
		t.Fatal("the direct read of a withdrawn page succeeded")
	}
	if failure.Code != controlplane.CodeNotFound {
		t.Fatalf("direct read refused with %q, want %q", failure.Code, controlplane.CodeNotFound)
	}
	if failure.Message == "" || failure.Stage != controlplane.StageRead {
		t.Fatalf("refusal carries no user-facing reason or stage: %+v", failure)
	}

	reenabled := harness.Install(t, device, updater)
	service = harness.Shell(reenabled, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	if restored := harness.PageIDs(service, write.RoleOwner); !harness.EqualStrings(restored, before) {
		t.Fatalf("pages after re-enabling = %v, want %v", restored, before)
	}
	if _, failure := service.Read(context.Background(), session, devicemanager.PageDevices); failure != nil {
		t.Fatalf("the re-enabled device page is unreadable: %v", failure)
	}
}
