// Package devicet8 verifies the fact staleness marker of the device page: a
// device whose collection is older than NODE-C-37's bound is marked stale with
// the moment it was last observed, and a device still inside the bound is not
// (DEVICE-AC-6, DEVICE-TC-8).
package devicet8

import (
	"context"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// TestFactStalenessMarksOnlyDevicesBeyondTheBound reads three nodes at one
// moment: a device observed beyond the bound, a device observed inside it, and a
// device observed exactly at it. Only the first is marked stale, and the mark
// carries the moment its own record was observed.
func TestFactStalenessMarksOnlyDevicesBeyondTheBound(t *testing.T) {
	readAt := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	factBeyond := readAt.Add(-25 * time.Hour)
	entryBeyond := readAt.Add(-26 * time.Hour)
	inside := readAt.Add(-time.Hour)
	entryInside := readAt.Add(-30 * time.Minute)
	atBound := readAt.Add(-24 * time.Hour)

	cluster := harness.Start()
	cluster.Node(t, "node-old")
	cluster.Node(t, "node-inside")
	cluster.Node(t, "node-edge")
	cluster.Collected(t, "node-old", "node-agent", factBeyond,
		harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk",
			ObservedAt: &entryBeyond})
	cluster.Collected(t, "node-inside", "node-agent", inside,
		harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk",
			ObservedAt: &entryInside})
	cluster.Collected(t, "node-edge", "node-agent", atBound,
		harness.Device{Name: "sdc", Serial: "S-3", Model: "PM9A3", Kind: "disk"})

	page, item := stalenessItem(t, cluster, readAt)
	if item.Source != extension.PageSourceComputed {
		t.Fatalf("staleness item source = %q, want %q", item.Source, extension.PageSourceComputed)
	}
	if item.ObservedAt == nil || !item.ObservedAt.Equal(factBeyond) {
		t.Fatalf("staleness item observed at %v, want the oldest collection time %v its facts state",
			item.ObservedAt, factBeyond)
	}
	if item.Entry != nil || item.Action != "" {
		t.Fatalf("the staleness item carries a modification entry: %+v", item)
	}

	stale := harness.Entries(t, harness.Wire(t, item.Value), "devices")
	if len(stale) != 1 {
		t.Fatalf("stale devices = %+v, want only the device beyond the bound", stale)
	}
	marked := stale[0]
	if got := harness.String(t, marked, "node"); got != "node-old" {
		t.Fatalf("stale node = %q, want node-old", got)
	}
	if got := harness.String(t, marked, "device"); got != "nvme0" {
		t.Fatalf("stale device = %q, want nvme0", got)
	}
	if got, want := harness.String(t, marked, "lastObservedAt"), entryBeyond.Format(time.RFC3339); got != want {
		t.Fatalf("stale device last observed at %q, want the record's own %q", got, want)
	}

	// A stale fact is still shown: the marker adds the age instead of taking
	// the device off the list (DEVICE-C-6).
	list, found := harness.ItemOf(page, devicemanager.ItemDeviceList)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemDeviceList, page.Items)
	}
	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, list.Value), "nodes"))
	devices := harness.ByName(t, harness.Entries(t, nodes["node-old"], "devices"))
	if _, found := devices["nvme0"]; !found {
		t.Fatalf("a stale device left the device list: %v", devices)
	}
}

// stalenessItem reads the device page and returns it with the fact staleness
// item.
func stalenessItem(t *testing.T, cluster *harness.Cluster, readAt time.Time) (pages.Page, pages.Item) {
	t.Helper()
	now := func() time.Time { return readAt }
	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	item, found := harness.ItemOf(page, devicemanager.ItemFactStaleness)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemFactStaleness, page.Items)
	}
	return page, item
}
