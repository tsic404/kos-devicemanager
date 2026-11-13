package devicemanager

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
)

// NoDeviceFactsReason explains a node whose device facts were never collected:
// its device list has nothing to show (DEVICE-C-3).
const NoDeviceFactsReason = "该节点尚无设备事实：采集缺失，设备清单不可观察"

// NoDevicesReason explains a node whose collection reported no device: the list
// is empty because there is nothing to list, not because the list failed
// (DEVICE-C-3).
const NoDevicesReason = "该节点本次采集没有设备"

// The two states the page shows for a device's occupancy; the four fact layers
// are judged separately (NODE-C-37).
const (
	occupancyOccupied = "occupied"
	occupancyFree     = "free"
)

// deviceList is the device list item: every node with the devices its
// collection reported (DEVICE-C-3).
type deviceList struct {
	Nodes []nodeDevices `json:"nodes"`
}

// nodeDevices is one node's device entries. A node with nothing to list carries
// the empty state and the reason it is empty instead (DEVICE-C-3).
type nodeDevices struct {
	Name        string        `json:"name"`
	Devices     []deviceEntry `json:"devices,omitempty"`
	EmptyReason string        `json:"emptyReason,omitempty"`
}

// deviceEntry is one device as the page shows it: its identity, the collection
// time of its own record, the four layers as independent values, and the
// occupancy. No field folds the layers into one state (DEVICE-C-3).
type deviceEntry struct {
	Name        string    `json:"name"`
	Serial      string    `json:"serial,omitempty"`
	Model       string    `json:"model,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	CollectedAt string    `json:"collectedAt,omitempty"`
	Present     bool      `json:"present"`
	Recognized  bool      `json:"recognized"`
	DriverReady bool      `json:"driverReady"`
	Published   bool      `json:"published"`
	Occupancy   occupancy `json:"occupancy"`
}

// occupancy is the occupancy every device carries. A record without occupancy
// is free, so the state is explicit rather than a missing field; the holding
// system and the object inside it appear only when collected (DEVICE-C-4).
type occupancy struct {
	State     string `json:"state"`
	Source    string `json:"source,omitempty"`
	Reference string `json:"reference,omitempty"`
}

// deviceList reads the device list (DEVICE-C-3, DEVICE-C-4). Nodes come from
// the Node list, so a node whose collection is missing keeps its empty state on
// the page. The item's observation is the oldest collection time the facts it
// rests on carry; with no fact at all the read itself is the observation
// (DEVICE-C-1, KOSD-C-13).
func (r *Readings) deviceList(ctx context.Context) (any, time.Time, *controlplane.Error) {
	reader := r.reader()
	nodes, failure := reader.Nodes(ctx)
	if failure != nil {
		return nil, time.Time{}, failure
	}
	objects, failure := reader.List(ctx, "NodeDevice", "")
	if failure != nil {
		return nil, time.Time{}, failure
	}
	facts, failure := factsByNode(objects)
	if failure != nil {
		return nil, time.Time{}, failure
	}

	lists := make([]nodeDevices, 0, len(nodes))
	observed := time.Time{}
	for _, node := range nodes {
		fact, hasFacts := facts[node.Name]
		list := nodeDevices{Name: node.Name}
		switch {
		case !hasFacts:
			list.EmptyReason = NoDeviceFactsReason
		case len(fact.Spec.Devices) == 0:
			list.EmptyReason = NoDevicesReason
		default:
			list.Devices = devicesOf(fact)
		}
		if hasFacts && fact.Spec.ObservedAt != nil {
			collected := fact.Spec.ObservedAt.Time
			if observed.IsZero() || collected.Before(observed) {
				observed = collected
			}
		}
		lists = append(lists, list)
	}
	sort.Slice(lists, func(left, right int) bool { return lists[left].Name < lists[right].Name })
	if observed.IsZero() {
		observed = reader.Clock()
	}
	return deviceList{Nodes: lists}, observed, nil
}

// devicesOf lists the device records of one node in identity order. A serial
// number collected more than once appears once, as the newest record; records
// naming no serial number are separate devices, not duplicates of each other
// (NODE-C-70).
func devicesOf(fact v1beta1.NodeDevice) []deviceEntry {
	candidates := make([]deviceRecord, 0, len(fact.Spec.Devices))
	newest := make(map[string]int, len(fact.Spec.Devices))
	for _, device := range fact.Spec.Devices {
		record := deviceRecord{
			entry:      deviceEntryOf(device, fact.Spec.ObservedAt),
			observedAt: recordObservedAt(device, fact.Spec.ObservedAt),
		}
		if device.Serial == "" {
			candidates = append(candidates, record)
			continue
		}
		index, seen := newest[device.Serial]
		if !seen {
			newest[device.Serial] = len(candidates)
			candidates = append(candidates, record)
			continue
		}
		if record.observedAt.After(candidates[index].observedAt) {
			candidates[index] = record
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		leftEntry, rightEntry := candidates[left].entry, candidates[right].entry
		if leftEntry.Name != rightEntry.Name {
			return leftEntry.Name < rightEntry.Name
		}
		if leftEntry.Serial != rightEntry.Serial {
			return leftEntry.Serial < rightEntry.Serial
		}
		return leftEntry.CollectedAt < rightEntry.CollectedAt
	})
	entries := make([]deviceEntry, 0, len(candidates))
	for _, candidate := range candidates {
		entries = append(entries, candidate.entry)
	}
	return entries
}

// deviceRecord is one record with the collection time it rests on, before the
// list is ordered and deduplicated.
type deviceRecord struct {
	entry      deviceEntry
	observedAt time.Time
}

// deviceEntryOf renders one record as the page shows it.
func deviceEntryOf(device v1beta1.DeviceFact, factObserved *metav1.Time) deviceEntry {
	entry := deviceEntry{
		Name:        device.Name,
		Serial:      device.Serial,
		Model:       device.Model,
		Kind:        device.Kind,
		CollectedAt: collectedText(recordObservedAt(device, factObserved)),
		Present:     device.IsPresent,
		Recognized:  device.IsRecognized,
		DriverReady: device.IsDriverReady,
		Published:   device.IsPublished,
		Occupancy:   occupancy{State: occupancyFree},
	}
	if device.Occupancy != nil {
		entry.Occupancy = occupancy{
			State:     occupancyOccupied,
			Source:    device.Occupancy.Source,
			Reference: device.Occupancy.Reference,
		}
	}
	return entry
}

// recordObservedAt is the collection time one record rests on: its own, else
// the fact's, whose collection covered every record it carries (NODE-C-70).
func recordObservedAt(device v1beta1.DeviceFact, factObserved *metav1.Time) time.Time {
	switch {
	case device.ObservedAt != nil:
		return device.ObservedAt.Time
	case factObserved != nil:
		return factObserved.Time
	default:
		return time.Time{}
	}
}

// collectedText formats a collection time; a time the collection did not carry
// stays empty instead of borrowing the read clock.
func collectedText(collected time.Time) string {
	if collected.IsZero() {
		return ""
	}
	return collected.UTC().Format(time.RFC3339)
}
