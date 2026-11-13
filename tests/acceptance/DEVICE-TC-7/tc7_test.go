// Package devicet7 verifies the missing-layer alerts of the device page: every
// layer that does not hold raises its own alert naming that device and that
// layer, a node whose collection failed leaves the rest of the page readable,
// and the alert surface offers no repair, re-collection or hardware entry
// (DEVICE-AC-5, DEVICE-TC-7).
package devicet7

import (
	"context"
	"sort"
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

// TestEachMissingLayerRaisesItsOwnAlert turns one layer off at a time: exactly
// that device and that layer raise an alert, the other three layers and the
// other device raise none, and no alert carries a cause or an action.
func TestEachMissingLayerRaisesItsOwnAlert(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	for _, missing := range layerNames {
		t.Run(missing, func(t *testing.T) {
			layers := harness.Layers{Present: true, Recognized: true, DriverReady: true, Published: true}
			layerOff(missing, &layers)

			cluster := harness.Start()
			cluster.Node(t, "node-a")
			cluster.Collected(t, "node-a", "node-agent", collected,
				harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk", Layers: &layers},
				harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk"})

			page, item := alertItem(t, cluster, collected.Add(30*time.Second))
			if item.Source != extension.PageSourceComputed {
				t.Fatalf("alert item source = %q, want %q", item.Source, extension.PageSourceComputed)
			}
			if item.ObservedAt == nil || !item.ObservedAt.Equal(collected) {
				t.Fatalf("alert item observed at %v, want the collection time %v the facts carry", item.ObservedAt, collected)
			}

			alerts := alertsOf(t, item)
			if len(alerts) != 1 {
				t.Fatalf("alerts = %+v, want only the %q alert of nvme0", alerts, missing)
			}
			alert := alerts[0]
			if got := harness.String(t, alert, "node"); got != "node-a" {
				t.Fatalf("alert node = %q, want node-a", got)
			}
			if got := harness.String(t, alert, "device"); got != "nvme0" {
				t.Fatalf("alert device = %q, want the device whose layer does not hold", got)
			}
			if got := harness.String(t, alert, "layer"); got != missing {
				t.Fatalf("alert layer = %q, want %q", got, missing)
			}
			if keys := harness.Keys(alert); !harness.EqualStrings(keys, []string{"device", "layer", "node"}) {
				t.Fatalf("alert fields = %v, want the device and the layer only, without an inferred cause", keys)
			}

			// The alert surface carries no repair, re-collection or hardware
			// entry, and the page offers no action at all (DEVICE-C-5).
			if len(page.Actions) != 0 {
				t.Fatalf("the page offers actions: %+v", page.Actions)
			}
			if item.Entry != nil || item.Action != "" {
				t.Fatalf("the alert item carries a modification entry: %+v", item)
			}
		})
	}
}

// TestFailedCollectionLeavesTheRestReadable reads a node with a device missing
// two layers beside a node whose collection failed: both alerts name the one
// device and its own layers, and the device list keeps every node readable.
func TestFailedCollectionLeavesTheRestReadable(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	missing := harness.Layers{Present: true, Recognized: false, DriverReady: true, Published: false}

	cluster := harness.Start()
	cluster.Node(t, "node-a")
	cluster.Node(t, "node-b")
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk", Layers: &missing},
		harness.Device{Name: "sdb", Serial: "S-2", Model: "PM9A3", Kind: "disk"})

	page, item := alertItem(t, cluster, collected.Add(30*time.Second))
	alerts := alertsOf(t, item)
	if len(alerts) != 2 {
		t.Fatalf("alerts = %+v, want one alert per missing layer of nvme0", alerts)
	}
	if got := []string{
		harness.String(t, alerts[0], "node") + "/" + harness.String(t, alerts[0], "device") + "/" + harness.String(t, alerts[0], "layer"),
		harness.String(t, alerts[1], "node") + "/" + harness.String(t, alerts[1], "device") + "/" + harness.String(t, alerts[1], "layer"),
	}; !harness.EqualStrings(got, []string{"node-a/nvme0/recognized", "node-a/nvme0/published"}) {
		t.Fatalf("alerts = %v, want the two layers that do not hold, each as its own alert", got)
	}

	// The node whose collection failed raises no alert, keeps its empty state,
	// and does not take the other node off the page (DEVICE-C-5).
	list, found := harness.ItemOf(page, devicemanager.ItemDeviceList)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemDeviceList, page.Items)
	}
	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, list.Value), "nodes"))
	if len(nodes) != 2 {
		t.Fatalf("device list nodes = %v, want both nodes", sortedNames(nodes))
	}
	silent := nodes["node-b"]
	if devices := harness.Entries(t, silent, "devices"); len(devices) != 0 {
		t.Fatalf("the node without collection lists devices: %+v", devices)
	}
	if reason := harness.String(t, silent, "emptyReason"); reason == "" {
		t.Fatal("the node without collection shows no reason")
	}

	// The alerting device keeps all four layers readable, so an alert adds a
	// notice instead of replacing the facts it rests on.
	devices := harness.ByName(t, harness.Entries(t, nodes["node-a"], "devices"))
	if len(devices) != 2 {
		t.Fatalf("node-a devices = %v, want both collected devices", sortedNames(devices))
	}
	nvme := devices["nvme0"]
	for _, layer := range layerNames {
		want := layer != "recognized" && layer != "published"
		if got := harness.Bool(t, nvme, layer); got != want {
			t.Fatalf("layer %q = %v beside the alert, want %v", layer, got, want)
		}
	}
}

// alertItem reads the device page and returns it with the missing-layer alert
// item.
func alertItem(t *testing.T, cluster *harness.Cluster, readAt time.Time) (pages.Page, pages.Item) {
	t.Helper()
	now := func() time.Time { return readAt }
	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	item, found := harness.ItemOf(page, devicemanager.ItemLayerAlerts)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemLayerAlerts, page.Items)
	}
	return page, item
}

// alertsOf returns the alerts an alert item carries.
func alertsOf(t *testing.T, item pages.Item) []map[string]any {
	t.Helper()
	return harness.Entries(t, harness.Wire(t, item.Value), "alerts")
}

// sortedNames lists the identities of an index, ordered by identity.
func sortedNames(index map[string]map[string]any) []string {
	names := make([]string, 0, len(index))
	for name := range index {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// layerOff clears one of the four layers of a record.
func layerOff(name string, layers *harness.Layers) {
	switch name {
	case "present":
		layers.Present = false
	case "recognized":
		layers.Recognized = false
	case "driverReady":
		layers.DriverReady = false
	case "published":
		layers.Published = false
	}
}
