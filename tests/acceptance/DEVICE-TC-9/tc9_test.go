// Package devicet9 verifies the projection staleness marker and the invalid
// marker of the device page: a projection observed beyond KOSD-C-13's bound is
// marked stale, a fact object the validator marked invalid is shown as invalid
// instead of as a normal device, and neither marker stands in for the other
// (DEVICE-AC-6, DEVICE-TC-9).
package devicet9

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

// layerNames are the four layers as the wire names them (NODE-C-70).
var layerNames = []string{"present", "recognized", "driverReady", "published"}

// TestProjectionBeyondTheBoundIsMarkedStale reads the same facts twice: beyond
// the two-minute bound every item carries the data stale marker, inside it none
// does, and the fact staleness marker stays empty both times because no device
// is beyond NODE-C-37's bound.
func TestProjectionBeyondTheBoundIsMarkedStale(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk"})

	late := readPage(t, cluster, collected.Add(3*time.Minute))
	if len(late.Items) == 0 {
		t.Fatal("the device page carries no item")
	}
	for _, item := range late.Items {
		if !item.Stale {
			t.Fatalf("item %q is not marked stale beyond the freshness bound: %+v", item.ID, item)
		}
	}
	if stale := harness.Entries(t, harness.Wire(t, itemOf(t, late, devicemanager.ItemFactStaleness).Value), "devices"); len(stale) != 0 {
		t.Fatalf("the projection marker is reported as fact staleness: %+v", stale)
	}

	onTime := readPage(t, cluster, collected.Add(30*time.Second))
	for _, item := range onTime.Items {
		if item.Stale {
			t.Fatalf("item %q is marked stale inside the freshness bound: %+v", item.ID, item)
		}
	}
}

// TestInvalidFactObjectIsShownAsInvalid marks one node's fact object invalid:
// the page shows that node as invalid instead of as a normal device, while the
// other node's four layers stay readable, and neither staleness marker appears.
func TestInvalidFactObjectIsShownAsInvalid(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster := harness.Start()
	cluster.Node(t, "node-bad")
	cluster.Node(t, "node-good")
	cluster.Collected(t, "node-bad", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk"})
	cluster.MarkInvalid(t, "node-bad")
	cluster.Collected(t, "node-good", "node-agent", collected,
		harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk"})

	page := readPage(t, cluster, collected.Add(30*time.Second))
	list := itemOf(t, page, devicemanager.ItemDeviceList)
	if list.Source != extension.PageSourceObject {
		t.Fatalf("device list source = %q, want the object source that carries the marker", list.Source)
	}
	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, list.Value), "nodes"))
	bad := nodes["node-bad"]
	if !marked(t, bad) {
		t.Fatalf("the invalid fact object is not shown as invalid: %+v", bad)
	}
	if devices := harness.Entries(t, bad, "devices"); len(devices) != 0 {
		t.Fatalf("the invalid fact object is presented as a normal device: %+v", devices)
	}
	if reason := harness.String(t, bad, "emptyReason"); reason == "" {
		t.Fatal("the invalid fact object carries no reason")
	}

	good := nodes["node-good"]
	if marked(t, good) {
		t.Fatalf("a valid fact object is marked invalid: %+v", good)
	}
	devices := harness.ByName(t, harness.Entries(t, good, "devices"))
	if len(devices) != 1 {
		t.Fatalf("the valid node devices = %+v, want its one device", devices)
	}
	for _, layer := range layerNames {
		if !harness.Bool(t, devices["sdb"], layer) {
			t.Fatalf("the valid device layer %q is not readable beside the invalid one", layer)
		}
	}

	// The summary of the invalid node presents no hardware, and neither
	// staleness marker appears: invalid is its own marker.
	summary := itemOf(t, page, devicemanager.ItemNodeSummary)
	summaries := harness.ByName(t, harness.Entries(t, harness.Wire(t, summary.Value), "nodes"))
	if harness.Bool(t, summaries["node-bad"], "observable") {
		t.Fatalf("the invalid node carries a normal summary: %+v", summaries["node-bad"])
	}
	if hardware, found := summaries["node-bad"]["hardware"]; found && hardware != nil {
		t.Fatalf("the invalid node carries a hardware summary: %+v", hardware)
	}
	if stale := harness.Entries(t, harness.Wire(t, itemOf(t, page, devicemanager.ItemFactStaleness).Value), "devices"); len(stale) != 0 {
		t.Fatalf("the invalid fact is reported as fact staleness: %+v", stale)
	}
	if alerts := harness.Entries(t, harness.Wire(t, itemOf(t, page, devicemanager.ItemLayerAlerts).Value), "alerts"); len(alerts) != 0 {
		t.Fatalf("the invalid fact raises layer alerts: %+v", alerts)
	}
	for _, item := range page.Items {
		if item.Stale {
			t.Fatalf("item %q is marked projection-stale while the facts are fresh: %+v", item.ID, item)
		}
	}
}

// TestBothStalenessMarkersAndInvalidStandApart reads a projection built from
// facts beyond the bound, fresh facts and an invalid fact object at once: the
// data stale marker, the fact staleness marker and the invalid marker are all
// visible, and none of them replaces another.
func TestBothStalenessMarkersAndInvalidStandApart(t *testing.T) {
	readAt := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	beyond := readAt.Add(-25 * time.Hour)
	fresh := readAt.Add(-30 * time.Second)

	cluster := harness.Start()
	cluster.Node(t, "node-old")
	cluster.Node(t, "node-fresh")
	cluster.Node(t, "node-bad")
	cluster.Collected(t, "node-old", "node-agent", beyond,
		harness.Device{Name: "nvme1", Serial: "S-1", Model: "PM9A3", Kind: "disk"})
	cluster.Collected(t, "node-fresh", "node-agent", fresh,
		harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk"})
	cluster.Collected(t, "node-bad", "node-agent", fresh,
		harness.Device{Name: "nvme0", Serial: "S-3", Model: "PM9A3", Kind: "disk"})
	cluster.MarkInvalid(t, "node-bad")

	page := readPage(t, cluster, readAt)
	list := itemOf(t, page, devicemanager.ItemDeviceList)
	if !list.Stale {
		t.Fatal("the projection beyond the freshness bound carries no data stale marker")
	}

	stale := harness.Entries(t, harness.Wire(t, itemOf(t, page, devicemanager.ItemFactStaleness).Value), "devices")
	if len(stale) != 1 {
		t.Fatalf("fact staleness = %+v, want only the device beyond the bound", stale)
	}
	if got := harness.String(t, stale[0], "node"); got != "node-old" {
		t.Fatalf("fact staleness names node %q, want node-old", got)
	}

	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, list.Value), "nodes"))
	if !marked(t, nodes["node-bad"]) {
		t.Fatalf("the invalid node is not marked invalid beside both staleness markers: %+v", nodes["node-bad"])
	}
	if marked(t, nodes["node-old"]) {
		t.Fatalf("a stale node is marked invalid instead of stale: %+v", nodes["node-old"])
	}
	if marked(t, nodes["node-fresh"]) {
		t.Fatalf("a fresh node is marked invalid: %+v", nodes["node-fresh"])
	}
}

// marked reports the invalid marker of one node entry; a node without the
// marker carries no field, which is an unmarked node rather than an error.
func marked(t *testing.T, entry map[string]any) bool {
	t.Helper()
	raw, found := entry["invalid"]
	if !found {
		return false
	}
	flag, isBool := raw.(bool)
	if !isBool {
		t.Fatalf("invalid marker = %T, want a boolean", raw)
	}
	return flag
}

// readPage reads the device page at one moment.
func readPage(t *testing.T, cluster *harness.Cluster, readAt time.Time) pages.Page {
	t.Helper()
	now := func() time.Time { return readAt }
	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	return page
}

// itemOf returns one rendered item of a page.
func itemOf(t *testing.T, page pages.Page, id string) pages.Item {
	t.Helper()
	item, found := harness.ItemOf(page, id)
	if !found {
		t.Fatalf("page carries no %q item: %+v", id, page.Items)
	}
	return item
}
