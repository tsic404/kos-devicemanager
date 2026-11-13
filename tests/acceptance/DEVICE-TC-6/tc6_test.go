// Package devicet6 verifies the occupancy of the device list: an occupied
// device names the system holding it, the object inside that system appears
// when the collection reported it and its absence is not a failure, a free
// device is marked free instead of left blank, and the page offers no release
// or reassignment entry (DEVICE-AC-4, DEVICE-TC-6).
package devicet6

import (
	"context"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// TestDeviceListShowsOccupancyAndFreeState reads one node carrying a device a
// storage system holds with its object, a device held by a system that named no
// object, and a device no system holds.
func TestDeviceListShowsOccupancyAndFreeState(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk",
			Occupancy: &harness.Occupancy{Source: "kos-storage", Reference: "pool-a"}},
		harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk",
			Occupancy: &harness.Occupancy{Source: "kos-network"}},
		harness.Device{Name: "sdc", Serial: "S-3", Model: "PM9A3", Kind: "disk"},
	)

	page, item := devicePageItem(t, cluster, collected.Add(30*time.Second))
	devices := devicesOf(t, item.Value)

	held := devices["nvme0"]
	occupancy := harness.Object(t, held, "occupancy")
	if got := harness.String(t, occupancy, "state"); got != "occupied" {
		t.Fatalf("held device state = %q, want occupied", got)
	}
	if got := harness.String(t, occupancy, "source"); got != "kos-storage" {
		t.Fatalf("held device source = %q, want the holding system kos-storage", got)
	}
	if got := harness.String(t, occupancy, "reference"); got != "pool-a" {
		t.Fatalf("held device reference = %q, want the reported object pool-a", got)
	}

	unreferenced := devices["sdb"]
	occupancy = harness.Object(t, unreferenced, "occupancy")
	if got := harness.String(t, occupancy, "state"); got != "occupied" {
		t.Fatalf("device held without an object state = %q, want occupied", got)
	}
	if got := harness.String(t, occupancy, "source"); got != "kos-network" {
		t.Fatalf("device held without an object source = %q, want kos-network", got)
	}
	if reference, found := occupancy["reference"]; found && reference != "" {
		t.Fatalf("a reference the collection did not report is invented: %v", reference)
	}

	free := devices["sdc"]
	occupancy = harness.Object(t, free, "occupancy")
	if got := harness.String(t, occupancy, "state"); got != "free" {
		t.Fatalf("free device state = %q, want free", got)
	}
	if source, found := occupancy["source"]; found && source != "" {
		t.Fatalf("a free device names a holding system: %v", source)
	}
	if reference, found := occupancy["reference"]; found && reference != "" {
		t.Fatalf("a free device names a holding object: %v", reference)
	}

	if len(page.Actions) != 0 {
		t.Fatalf("the page offers actions, including a release or reassignment entry: %+v", page.Actions)
	}
	for _, rendered := range page.Items {
		if rendered.Entry != nil || rendered.Action != "" {
			t.Fatalf("item %q carries a modification entry: %+v", rendered.ID, rendered)
		}
	}
}

// devicePageItem reads the device page and returns it with the device list
// item.
func devicePageItem(t *testing.T, cluster *harness.Cluster, readAt time.Time) (pages.Page, pages.Item) {
	t.Helper()
	now := func() time.Time { return readAt }
	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	item, found := harness.ItemOf(page, devicemanager.ItemDeviceList)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemDeviceList, page.Items)
	}
	return page, item
}

// devicesOf returns the device entries of the one node the list carries,
// indexed by device identity.
func devicesOf(t *testing.T, value any) map[string]map[string]any {
	t.Helper()
	nodes := harness.Entries(t, harness.Wire(t, value), "nodes")
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want the one node", nodes)
	}
	return harness.ByName(t, harness.Entries(t, nodes[0], "devices"))
}
