// Package devicet3 verifies the node hardware summary: every node lists its
// identity and the collection time its facts carry, a node whose collection is
// missing is marked unobservable and stays listed, and the other nodes keep
// their full summary (DEVICE-AC-2, DEVICE-TC-3).
package devicet3

import (
	"context"
	"encoding/json"
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

func TestNodeSummaryMarksMissingCollection(t *testing.T) {
	cluster := harness.Start()
	cluster.Node(t, "node-ready")
	cluster.Node(t, "node-silent")
	collected := time.Date(2026, 3, 1, 11, 58, 0, 0, time.UTC)
	cluster.Collected(t, "node-ready", "node-agent", collected,
		harness.Device{Name: "gpu0", Serial: "g0", Model: "A100", Kind: "gpu"},
		harness.Device{Name: "gpu1", Serial: "g1", Model: "A100", Kind: "gpu"})
	now := func() time.Time { return collected.Add(30 * time.Second) }

	assessment := harness.Install(t, harness.Component{Name: "kos-devicemanager", Declaration: devicemanager.Declaration()})
	service := harness.Shell(assessment, &devicemanager.Readings{Dynamic: cluster.Dynamic, Now: now}, now)

	page, failure := service.Read(context.Background(), harness.Session(write.RoleGuest), devicemanager.PageDevices)
	if failure != nil {
		t.Fatalf("read the device page: %v", failure)
	}
	item, found := harness.ItemOf(page, devicemanager.ItemNodeSummary)
	if !found {
		t.Fatalf("page carries no %q item: %+v", devicemanager.ItemNodeSummary, page.Items)
	}
	if item.Source != extension.PageSourceObject || item.ObservedAt == nil {
		t.Fatalf("summary item = %+v, want an object item with an observation time", item)
	}

	value := decodeSummary(t, item.Value)
	if len(value.Nodes) != 2 {
		t.Fatalf("summaries = %+v, want both nodes", value.Nodes)
	}
	if value.Nodes[0].Name != "node-ready" || value.Nodes[1].Name != "node-silent" {
		t.Fatalf("summary order = %q, %q, want identity order", value.Nodes[0].Name, value.Nodes[1].Name)
	}

	ready := value.Nodes[0]
	if !ready.Observable {
		t.Fatalf("the collected node is marked unobservable: %+v", ready)
	}
	if ready.CollectedAt != collected.Format(time.RFC3339) {
		t.Fatalf("collected node time = %q, want %q", ready.CollectedAt, collected.Format(time.RFC3339))
	}
	if ready.Hardware == nil || ready.Hardware.DeviceCount != 2 {
		t.Fatalf("collected node hardware = %+v, want two devices", ready.Hardware)
	}
	if ready.Reason != "" {
		t.Fatalf("an observable summary carries a reason: %q", ready.Reason)
	}

	silent := value.Nodes[1]
	if silent.Observable {
		t.Fatalf("the node without collection is observable: %+v", silent)
	}
	if silent.Reason == "" {
		t.Fatal("the node without collection carries no reason")
	}
	if silent.CollectedAt != "" || silent.Hardware != nil {
		t.Fatalf("an unobservable summary invents collected data: %+v", silent)
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
