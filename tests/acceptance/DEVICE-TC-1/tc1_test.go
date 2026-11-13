// Package devicet1 verifies the device page of the device view: every display
// item carries its source type and the observation time of that source, the
// page states why it is read-only, and it offers no modification entry
// (DEVICE-AC-1, DEVICE-TC-1).
package devicet1

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
	"github.com/tsic404/kos-devicemanager/tests/acceptance/harness"
)

// summary is the value of the node summary item as the page carries it.
type summary struct {
	Nodes []struct {
		Name        string `json:"name"`
		Observable  bool   `json:"observable"`
		CollectedAt string `json:"collectedAt"`
		Hardware    *struct {
			DeviceCount int `json:"deviceCount"`
			Devices     []struct {
				Kind  string `json:"kind"`
				Model string `json:"model"`
				Count int    `json:"count"`
			} `json:"devices"`
		} `json:"hardware"`
		Reason string `json:"reason"`
	} `json:"nodes"`
}

func TestDevicePageCarriesSourceTypesAndObservationTimes(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-a")
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster.Collected(t, "node-a", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "g0", Model: "A100", Kind: "gpu"},
		harness.Device{Name: "gpu1", Serial: "g1", Model: "A100", Kind: "gpu"},
		harness.Device{Name: "sda", Serial: "s0", Model: "PM9A3", Kind: "disk"})

	// The read happens well after the collection, so an observation time taken
	// from the read clock would be distinguishable from the collected one.
	readAt := collected.Add(30 * time.Second)
	now := func() time.Time { return readAt }

	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)
	session := harness.Session(write.RoleGuest)

	summaries := service.List(session)
	if len(summaries) != 1 || summaries[0].ID != devicemanager.PageDevices {
		t.Fatalf("listed pages = %v, want only %q", harness.SummaryIDs(summaries), devicemanager.PageDevices)
	}
	if !summaries[0].ReadOnly || summaries[0].ReadOnlyReason == "" {
		t.Fatalf("page summary does not state the read-only reason: %+v", summaries[0])
	}

	page, failure := service.Read(context.Background(), session, devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	if !page.ReadOnly || page.ReadOnlyReason == "" {
		t.Fatalf("the page does not state why it is read-only: readOnly=%v reason=%q", page.ReadOnly, page.ReadOnlyReason)
	}
	if len(page.Actions) != 0 {
		t.Fatalf("a read-only page offers actions: %+v", page.Actions)
	}

	// Every declared item states the source type that judges it and the moment
	// that source observed the value; the page carries the set its items
	// declare (KOSD-C-53, DEVICE-C-1).
	declared := map[string]string{
		devicemanager.ItemNodeSummary:   extension.PageSourceObject,
		devicemanager.ItemDeviceList:    extension.PageSourceObject,
		devicemanager.ItemLayerAlerts:   extension.PageSourceComputed,
		devicemanager.ItemFactStaleness: extension.PageSourceComputed,
	}
	if len(page.Items) != len(declared) {
		t.Fatalf("page items = %+v, want the %d items the device page declares", page.Items, len(declared))
	}
	for _, rendered := range page.Items {
		source, known := declared[rendered.ID]
		if !known {
			t.Fatalf("the page carries an item it does not declare: %q", rendered.ID)
		}
		if rendered.Source != source {
			t.Fatalf("item %q source = %q, want %q", rendered.ID, rendered.Source, source)
		}
		if rendered.ObservedAt == nil {
			t.Fatalf("item %q carries no observation time", rendered.ID)
		}
	}
	sources := append([]string(nil), page.Sources...)
	sort.Strings(sources)
	if want := []string{extension.PageSourceComputed, extension.PageSourceObject}; !harness.EqualStrings(sources, want) {
		t.Fatalf("page sources = %v, want %v", sources, want)
	}

	item, found := harness.ItemOf(page, devicemanager.ItemNodeSummary)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemNodeSummary, page.Items)
	}
	if item.Source != extension.PageSourceObject {
		t.Fatalf("item source = %q, want %q", item.Source, extension.PageSourceObject)
	}
	if item.ObservedAt == nil {
		t.Fatal("item carries no observation time")
	}
	if !item.ObservedAt.Equal(collected) {
		t.Fatalf("item observed at %v, want the collection time %v the object carries", item.ObservedAt, collected)
	}
	if item.Entry != nil {
		t.Fatalf("a read-only page item carries a modification entry: %+v", item.Entry)
	}

	value := decodeSummary(t, item.Value)
	if len(value.Nodes) != 1 {
		t.Fatalf("summaries = %+v, want one node", value.Nodes)
	}
	node := value.Nodes[0]
	if node.Name != "node-a" {
		t.Fatalf("node identity = %q, want node-a", node.Name)
	}
	if !node.Observable || node.CollectedAt != collected.Format(time.RFC3339) {
		t.Fatalf("node summary = %+v, want an observable summary collected at %v", node, collected)
	}
	if node.Hardware == nil || node.Hardware.DeviceCount != 3 {
		t.Fatalf("hardware summary = %+v, want three collected devices", node.Hardware)
	}
	counts := map[string]int{}
	for _, device := range node.Hardware.Devices {
		counts[device.Kind+"/"+device.Model] = device.Count
	}
	if counts["gpu/A100"] != 2 || counts["disk/PM9A3"] != 1 {
		t.Fatalf("hardware counts = %v, want two A100 GPUs and one disk", counts)
	}
}

// decodeSummary reads the item value as the page carries it over the wire.
func decodeSummary(t *testing.T, value any) summary {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode node summary: %v", err)
	}
	decoded := summary{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode node summary: %v", err)
	}
	return decoded
}
