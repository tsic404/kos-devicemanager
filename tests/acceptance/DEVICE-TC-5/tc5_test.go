// Package devicet5 verifies the empty state of the device list and the shape of
// an accelerator: a node the collection reported no device for shows the empty
// state with its reason, and a GPU is shown with the same four layers, in the
// same shape, as an ordinary device (DEVICE-AC-3, DEVICE-TC-5).
package devicet5

import (
	"context"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// layerNames are the four layers as the wire names them (NODE-C-70).
var layerNames = []string{"present", "recognized", "driverReady", "published"}

// TestDeviceListShowsEmptyStateAndAcceleratorsAlike reads a node whose facts
// carry no device, a node the collection never reported, and a node with an
// accelerator beside a disk.
func TestDeviceListShowsEmptyStateAndAcceleratorsAlike(t *testing.T) {
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster := harness.Start()
	cluster.Node(t, "node-no-device")
	cluster.Node(t, "node-silent")
	cluster.Node(t, "node-gpu")
	cluster.Collected(t, "node-no-device", "node-agent", collected)
	cluster.Collected(t, "node-gpu", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "G-1", Model: "A100", Kind: "gpu"},
		harness.Device{Name: "sda", Serial: "S-1", Model: "PM9A3", Kind: "disk"},
	)

	item := deviceListItem(t, cluster, collected.Add(30*time.Second))
	nodes := harness.ByName(t, harness.Entries(t, harness.Wire(t, item.Value), "nodes"))
	if len(nodes) != 3 {
		t.Fatalf("nodes = %+v, want the three nodes", nodes)
	}

	for _, name := range []string{"node-no-device", "node-silent"} {
		empty := nodes[name]
		if devices := harness.Entries(t, empty, "devices"); len(devices) != 0 {
			t.Fatalf("node %s lists devices: %+v", name, devices)
		}
		if reason := harness.String(t, empty, "emptyReason"); reason == "" {
			t.Fatalf("node %s shows an empty state without a reason", name)
		}
	}

	devices := harness.ByName(t, harness.Entries(t, nodes["node-gpu"], "devices"))
	accelerator, found := devices["gpu0"]
	if !found {
		t.Fatalf("devices = %+v, want the accelerator gpu0", devices)
	}
	disk := devices["sda"]
	if got := harness.String(t, accelerator, "kind"); got != "gpu" {
		t.Fatalf("accelerator kind = %q, want gpu", got)
	}
	if got := harness.String(t, disk, "kind"); got != "disk" {
		t.Fatalf("ordinary device kind = %q, want disk", got)
	}
	acceleratorFields, diskFields := harness.Keys(accelerator), harness.Keys(disk)
	if !harness.EqualStrings(acceleratorFields, diskFields) {
		t.Fatalf("accelerator fields = %v, ordinary device fields = %v, want the same shape",
			acceleratorFields, diskFields)
	}
	for _, layer := range layerNames {
		if !harness.Bool(t, accelerator, layer) {
			t.Fatalf("accelerator layer %q = false, want the layer shown like any other device", layer)
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
