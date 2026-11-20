// Package devicet10 verifies the read-only boundary of the device page in each
// of its states. Every state is built and read on its own — the complete page
// without alerts, the page whose every node lists no device, and the alerting
// page — so no state's audit rests on another state sharing the page. A mixed
// page carrying all three states at once is checked as an extra case
// (DEVICE-AC-7, DEVICE-TC-10).
package devicet10

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// layerNames are the four layers as the wire names them (NODE-C-70).
var layerNames = []string{"present", "recognized", "driverReady", "published"}

// collected is the collection time every state's facts carry.
var collected = time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)

// readPage reads the device page of one cluster as a guest.
func readPage(t *testing.T, cluster *harness.Cluster) pages.Page {
	t.Helper()
	readAt := collected.Add(30 * time.Second)
	now := func() time.Time { return readAt }
	assessment := harness.Install(t, harness.Component{
		Name:        "kos-devicemanager",
		Declaration: devicemanager.Declaration(),
	})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	return page
}

// deviceNodes returns the device list item of one page, indexed by node name.
func deviceNodes(t *testing.T, page pages.Page) (map[string]map[string]any, int) {
	t.Helper()
	list, found := harness.ItemOf(page, devicemanager.ItemDeviceList)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemDeviceList, page.Items)
	}
	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, list.Value), "nodes"))
	total := 0
	for _, node := range nodes {
		total += len(harness.Entries(t, node, "devices"))
	}
	return nodes, total
}

// alertsOf returns the alert entries of one page.
func alertsOf(t *testing.T, page pages.Page) []map[string]any {
	t.Helper()
	item, found := harness.ItemOf(page, devicemanager.ItemLayerAlerts)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemLayerAlerts, page.Items)
	}
	return harness.Entries(t, harness.Wire(t, item.Value), "alerts")
}

// TestCompletePageWithoutAlertsHasNoWriteEntry reads a page whose nodes all
// carry fully healthy devices: the case first shows that state holds, then
// audits the whole read-only page for write entries.
func TestCompletePageWithoutAlertsHasNoWriteEntry(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-gpu")
	cluster.Node(t, "node-disk")
	cluster.Collected(t, "node-gpu", "node-agent", collected,
		harness.Device{
			Name: "gpu0", Serial: "G-1", Model: "A100", Kind: "gpu",
			Occupancy: &harness.Occupancy{Source: "vm", Reference: "vm-a"},
		})
	cluster.Collected(t, "node-disk", "node-agent", collected,
		harness.Device{Name: "sda", Serial: "S-1", Model: "PM9A3", Kind: "disk"})

	page := readPage(t, cluster)

	// The scenario holds: both nodes list a device and every layer is on, so
	// the page is the complete state and raises no alert.
	nodes, total := deviceNodes(t, page)
	if len(nodes) != 2 || total != 2 {
		t.Fatalf("device list = %d nodes / %d devices, want the complete state", len(nodes), total)
	}
	for name, node := range nodes {
		devices := harness.Entries(t, node, "devices")
		if len(devices) != 1 {
			t.Fatalf("node %s lists %d devices, want the complete state", name, len(devices))
		}
		if reason, found := node["emptyReason"]; found && reason != nil && reason != "" {
			t.Fatalf("node %s shows an empty reason on the complete page: %+v", name, node)
		}
		for _, device := range devices {
			for _, layer := range layerNames {
				if !harness.Bool(t, device, layer) {
					t.Fatalf("node %s device %v layer %q is off; the complete page needs every layer",
						name, device["name"], layer)
				}
			}
		}
	}
	if alerts := alertsOf(t, page); len(alerts) != 0 {
		t.Fatalf("the complete page raises alerts: %+v", alerts)
	}

	assertNoWriteSurface(t, page)
}

// TestPageWithoutDevicesHasNoWriteEntry reads a page whose every node lists no
// device, one because its collection reported none and one because no
// collection exists: the case shows the whole page is the empty state, then
// audits it for write entries.
func TestPageWithoutDevicesHasNoWriteEntry(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-reported-none")
	cluster.Node(t, "node-never-collected")
	cluster.Collected(t, "node-reported-none", "node-agent", collected)

	page := readPage(t, cluster)

	// The scenario holds: the page carries no device at all, and each node
	// states why its list is empty.
	nodes, total := deviceNodes(t, page)
	if len(nodes) != 2 {
		t.Fatalf("device list nodes = %+v, want the two empty nodes", nodes)
	}
	if total != 0 {
		t.Fatalf("the page lists %d devices, want the empty state", total)
	}
	for name, node := range nodes {
		if devices := harness.Entries(t, node, "devices"); len(devices) != 0 {
			t.Fatalf("node %s lists devices on the empty page: %+v", name, devices)
		}
		if reason := harness.String(t, node, "emptyReason"); reason == "" {
			t.Fatalf("node %s shows an empty state without a reason", name)
		}
	}
	if reason := harness.String(t, nodes["node-reported-none"], "emptyReason"); reason != devicemanager.NoDevicesReason {
		t.Fatalf("the reported-empty node reason = %q, want %q", reason, devicemanager.NoDevicesReason)
	}
	if reason := harness.String(t, nodes["node-never-collected"], "emptyReason"); reason != devicemanager.NoDeviceFactsReason {
		t.Fatalf("the uncollected node reason = %q, want %q", reason, devicemanager.NoDeviceFactsReason)
	}
	if alerts := alertsOf(t, page); len(alerts) != 0 {
		t.Fatalf("the empty page raises alerts: %+v", alerts)
	}

	assertNoWriteSurface(t, page)
}

// TestAlertPageHasNoWriteEntry reads a page whose one device misses a layer:
// the case shows the alert state holds, then audits the whole page for write
// entries, including the alerting device and the alert itself.
func TestAlertPageHasNoWriteEntry(t *testing.T) {
	missing := harness.Layers{Present: true, Recognized: true, DriverReady: true}
	cluster := harness.Start()
	cluster.Node(t, "node-alert")
	cluster.Collected(t, "node-alert", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "N-1", Model: "PM9A3", Kind: "disk", Layers: &missing})

	page := readPage(t, cluster)

	// The scenario holds: the node still lists its device — a missing layer
	// never hides the device — and exactly that device and layer is alerted.
	nodes, total := deviceNodes(t, page)
	if len(nodes) != 1 || total != 1 {
		t.Fatalf("device list = %d nodes / %d devices, want the alerting device listed", len(nodes), total)
	}
	devices := harness.Entries(t, nodes["node-alert"], "devices")
	if len(devices) != 1 {
		t.Fatalf("alerting node devices = %+v, want its device", devices)
	}
	if harness.Bool(t, devices[0], "published") {
		t.Fatalf("the alerting device reports the missing layer as on: %+v", devices[0])
	}
	alerts := alertsOf(t, page)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want the one missing layer", alerts)
	}
	alert := harness.String(t, alerts[0], "node") + "/" + harness.String(t, alerts[0], "device") + "/" + harness.String(t, alerts[0], "layer")
	if alert != "node-alert/nvme0/published" {
		t.Fatalf("alert = %q, want the alerting node, device and missing layer", alert)
	}

	assertNoWriteSurface(t, page)
}

// TestMixedPageHasNoWriteEntry reads one page carrying the complete, empty and
// alerting states together, as an extra case: each state keeps its shape and
// the page carries no write entry either.
func TestMixedPageHasNoWriteEntry(t *testing.T) {
	missing := harness.Layers{Present: true, Recognized: true, DriverReady: true}
	cluster := harness.Start()
	cluster.Node(t, "node-complete")
	cluster.Collected(t, "node-complete", "node-agent", collected,
		harness.Device{
			Name: "gpu0", Serial: "G-1", Model: "A100", Kind: "gpu",
			Occupancy: &harness.Occupancy{Source: "vm", Reference: "vm-a"},
		})
	cluster.Node(t, "node-empty")
	cluster.Node(t, "node-alert")
	cluster.Collected(t, "node-alert", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "N-1", Model: "PM9A3", Kind: "disk", Layers: &missing})

	page := readPage(t, cluster)

	nodes, total := deviceNodes(t, page)
	if len(nodes) != 3 || total != 2 {
		t.Fatalf("device list = %d nodes / %d devices, want complete, empty and alerting nodes", len(nodes), total)
	}
	if devices := harness.Entries(t, nodes["node-complete"], "devices"); len(devices) != 1 {
		t.Fatalf("complete node devices = %+v, want the collected device", devices)
	}
	if devices := harness.Entries(t, nodes["node-empty"], "devices"); len(devices) != 0 {
		t.Fatalf("empty node lists devices: %+v", devices)
	}
	if reason := harness.String(t, nodes["node-empty"], "emptyReason"); reason == "" {
		t.Fatal("empty node shows no empty-state reason")
	}
	alerts := alertsOf(t, page)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %+v, want the missing layer of nvme0", alerts)
	}
	if got := harness.String(t, alerts[0], "node") + "/" + harness.String(t, alerts[0], "device") + "/" + harness.String(t, alerts[0], "layer"); got != "node-alert/nvme0/published" {
		t.Fatalf("alert = %q, want the alerting node, device and missing layer", got)
	}

	assertNoWriteSurface(t, page)
}

// assertNoWriteSurface checks one page as the browser receives it: the page
// states it is read-only with a reason, offers no page action, no item carries
// an action or entry, and no node of the rendered page holds a write entry at
// any depth (DEVICE-C-7).
func assertNoWriteSurface(t *testing.T, page pages.Page) {
	t.Helper()
	if !page.ReadOnly || strings.TrimSpace(page.ReadOnlyReason) == "" {
		t.Fatalf("page does not state the read-only reason: readOnly=%v reason=%q", page.ReadOnly, page.ReadOnlyReason)
	}
	if len(page.Actions) != 0 {
		t.Fatalf("the read-only page offers actions: %+v", page.Actions)
	}
	for _, item := range page.Items {
		if item.Action != "" || item.Entry != nil {
			t.Fatalf("item %q carries a modification entry: action=%q entry=%+v", item.ID, item.Action, item.Entry)
		}
	}

	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("encode the device page: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode the device page: %v", err)
	}
	walkForWriteSurface(t, decoded, "page")
	for _, forbidden := range []string{"电源", "BIOS", "RAID", "风扇", "释放", "改派"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("the device page carries the forbidden entry %q", forbidden)
		}
	}
}

// walkForWriteSurface checks one decoded page node and every child. Any action
// list, action identity or modification entry at any depth is a write surface
// the read-only page must not carry.
func walkForWriteSurface(t *testing.T, value any, path string) {
	t.Helper()
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			switch key {
			case "actions":
				list, ok := child.([]any)
				if !ok || len(list) != 0 {
					t.Fatalf("%s.%s carries page actions: %+v", path, key, child)
				}
			case "action":
				if child != nil && child != "" {
					t.Fatalf("%s.%s carries a modification action: %+v", path, key, child)
				}
			case "entry":
				if child != nil {
					t.Fatalf("%s.%s carries a modification entry: %+v", path, key, child)
				}
			}
			walkForWriteSurface(t, child, path+"."+key)
		}
	case []any:
		for index, child := range node {
			walkForWriteSurface(t, child, path+"["+strconv.Itoa(index)+"]")
		}
	}
}
