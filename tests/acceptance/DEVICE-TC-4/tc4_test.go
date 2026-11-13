// Package devicet4 verifies the device list renders the four fact layers as
// independent values: an entry missing one layer keeps the other three and its
// occupancy readable, no merged health state stands in for them, and a serial
// number the collector reported twice appears once, as the newest record
// (DEVICE-AC-3, DEVICE-TC-4, NODE-C-70).
package devicet4

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

// mergedNames are the names a folded four-layer status would travel under; the
// page must not replace the four layers with one of them.
var mergedNames = []string{"health", "healthy", "status", "state"}

// TestDeviceListShowsEachLayerOnItsOwn reads the device page once per layer,
// each time with that layer false and the other three true: only the layer that
// does not hold may read as missing, and the occupancy stays readable.
func TestDeviceListShowsEachLayerOnItsOwn(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	deviceCollected := collected.Add(-5 * time.Minute)
	for _, missing := range layerNames {
		t.Run(missing, func(t *testing.T) {
			facts := harness.Layers{Present: true, Recognized: true, DriverReady: true, Published: true}
			layerOff(missing, &facts)

			cluster := harness.Start()
			cluster.Node(t, "node-a")
			cluster.Collected(t, "node-a", "node-agent", collected, harness.Device{
				Name: "nvme0", Serial: "S-1", Model: "PM9A3", Kind: "disk",
				Layers:     &facts,
				Occupancy:  &harness.Occupancy{Source: "kos-storage", Reference: "pool-a"},
				ObservedAt: &deviceCollected,
			})

			item := deviceListItem(t, cluster, collected.Add(30*time.Second))
			if item.Source != extension.PageSourceObject {
				t.Fatalf("device list source = %q, want %q", item.Source, extension.PageSourceObject)
			}
			if item.ObservedAt == nil || !item.ObservedAt.Equal(collected) {
				t.Fatalf("device list observed at %v, want the collection time %v the facts carry", item.ObservedAt, collected)
			}

			nodes := harness.Entries(t, harness.Wire(t, item.Value), "nodes")
			if len(nodes) != 1 {
				t.Fatalf("nodes = %+v, want the one node", nodes)
			}
			devices := harness.Entries(t, nodes[0], "devices")
			if len(devices) != 1 {
				t.Fatalf("devices = %+v, want the one device", devices)
			}
			device := devices[0]

			if got := harness.String(t, device, "name"); got != "nvme0" {
				t.Fatalf("device identity = %q, want nvme0", got)
			}
			if got := harness.String(t, device, "collectedAt"); got != deviceCollected.Format(time.RFC3339) {
				t.Fatalf("device collection time = %q, want the record's own %q",
					got, deviceCollected.Format(time.RFC3339))
			}
			for _, layer := range layerNames {
				want := layer != missing
				if got := harness.Bool(t, device, layer); got != want {
					t.Fatalf("layer %q = %v with only %q missing, want %v", layer, got, missing, want)
				}
			}
			occupancy := harness.Object(t, device, "occupancy")
			if got := harness.String(t, occupancy, "source"); got != "kos-storage" {
				t.Fatalf("occupancy source = %q, want kos-storage while a layer is missing", got)
			}
			if got := harness.String(t, occupancy, "reference"); got != "pool-a" {
				t.Fatalf("occupancy reference = %q, want pool-a while a layer is missing", got)
			}
			for _, merged := range mergedNames {
				if _, found := device[merged]; found {
					t.Fatalf("device entry carries a merged %q field: %+v", merged, device)
				}
			}
		})
	}
}

// TestDeviceListKeepsNewestRecordOfASerial states one serial number twice with
// different collection times: the list carries the newest record only, and it
// keeps the devices whose records name another serial number or none, which are
// not duplicates of it.
func TestDeviceListKeepsNewestRecordOfASerial(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	older := collected.Add(-2 * time.Hour)
	newer := collected.Add(-time.Minute)

	cluster := harness.Start()
	cluster.Node(t, "node-a")
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "sda-silent", Serial: "S-9", Model: "PM9A3", Kind: "disk",
			Layers: &harness.Layers{}, ObservedAt: &older},
		harness.Device{Name: "sda", Serial: "S-9", Model: "PM9A3", Kind: "disk",
			Layers:     &harness.Layers{Present: true, Recognized: true, DriverReady: true, Published: true},
			ObservedAt: &newer},
		harness.Device{Name: "sdb", Serial: "S-10", Model: "PM9A3", Kind: "disk", ObservedAt: &newer},
		harness.Device{Name: "sdc", Model: "PM9A3", Kind: "disk", ObservedAt: &newer},
		harness.Device{Name: "sdd", Model: "PM9A3", Kind: "disk", ObservedAt: &newer},
	)

	item := deviceListItem(t, cluster, collected.Add(30*time.Second))
	nodes := harness.Entries(t, harness.Wire(t, item.Value), "nodes")
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want the one node", nodes)
	}
	devices := harness.ByName(t, harness.Entries(t, nodes[0], "devices"))
	if len(devices) != 4 {
		t.Fatalf("devices = %v, want the newest record of the duplicated serial and the three other devices",
			names(devices))
	}
	newest, found := devices["sda"]
	if !found {
		t.Fatalf("devices = %v, want the newest record of serial S-9", names(devices))
	}
	if got := harness.String(t, newest, "collectedAt"); got != newer.Format(time.RFC3339) {
		t.Fatalf("kept record collected at %q, want the newest %q", got, newer.Format(time.RFC3339))
	}
	for _, layer := range layerNames {
		if !harness.Bool(t, newest, layer) {
			t.Fatalf("kept record layer %q = false, want the newest record and its four layers", layer)
		}
	}
	for _, name := range []string{"sdb", "sdc", "sdd"} {
		if _, found := devices[name]; !found {
			t.Fatalf("devices = %v, want %s kept as its own device", names(devices), name)
		}
	}
}

// deviceListItem reads the device page and returns its device list item.
func deviceListItem(t *testing.T, cluster *harness.Cluster, readAt time.Time) pages.Item {
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
	return item
}

// names lists the device identities of an index, ordered by identity.
func names(index map[string]map[string]any) []string {
	listed := make([]string, 0, len(index))
	for name := range index {
		listed = append(listed, name)
	}
	sort.Strings(listed)
	return listed
}

// layerOff clears one of the four layers of a record.
func layerOff(name string, facts *harness.Layers) {
	switch name {
	case "present":
		facts.Present = false
	case "recognized":
		facts.Recognized = false
	case "driverReady":
		facts.DriverReady = false
	case "published":
		facts.Published = false
	}
}
