package devicemanager

import (
	"context"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/markers"
)

// NoDeviceFactsReason explains a node whose device facts were never collected:
// its device list has nothing to show (DEVICE-C-3).
const NoDeviceFactsReason = "该节点尚无设备事实：采集缺失，设备清单不可观察"

// NoDevicesReason explains a node whose collection reported no device: the list
// is empty because there is nothing to list, not because the list failed
// (DEVICE-C-3).
const NoDevicesReason = "该节点本次采集没有设备"

// InvalidFactReason explains a node whose stored fact object carries the
// validator's validity marker: the object is not presented as the facts of a
// normal device (DEVICE-C-6).
const InvalidFactReason = "该节点的设备事实被校验器标记为无效"

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
// the empty state and the reason it is empty instead; the invalid marker says
// the fact object itself cannot be presented as a normal device (DEVICE-C-3,
// DEVICE-C-6).
type nodeDevices struct {
	Name        string        `json:"name"`
	Invalid     bool          `json:"invalid,omitempty"`
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

// deviceRecord is one record with the collection time it rests on, before the
// list is ordered and deduplicated.
type deviceRecord struct {
	entry      deviceEntry
	observedAt time.Time
}

// deviceView is one node as the page reads it: its identity, whether the stored
// fact object carries the invalid marker, the device records it holds, and the
// reason it lists nothing when it holds none (DEVICE-C-3, DEVICE-C-6).
type deviceView struct {
	node        string
	invalid     bool
	records     []deviceRecord
	emptyReason string
}

// deviceInventory is the device side one page read rests on: every node with
// the records its fact object holds, and the oldest collection time those facts
// state (DEVICE-C-1, KOSD-C-13).
type deviceInventory struct {
	views    []deviceView
	observed time.Time
}

// inventory reads the Node list and the device facts and pairs them. Nodes come
// from the Node list, so a node whose collection is missing keeps its empty
// state on the page. An invalid fact object is shown as invalid and its records
// are dropped, so it never stands in as the facts of a normal device
// (DEVICE-C-3, DEVICE-C-6). The observation is the oldest collection time the
// facts state; with no fact at all the read itself is the observation
// (DEVICE-C-1, KOSD-C-13).
func (r *Readings) inventory(ctx context.Context) (deviceInventory, *controlplane.Error) {
	reader := r.reader()
	nodes, failure := reader.Nodes(ctx)
	if failure != nil {
		return deviceInventory{}, failure
	}
	objects, failure := reader.List(ctx, "NodeDevice", "")
	if failure != nil {
		return deviceInventory{}, failure
	}
	facts, failure := factsByNode(objects)
	if failure != nil {
		return deviceInventory{}, failure
	}

	views := make([]deviceView, 0, len(nodes))
	observed := time.Time{}
	for _, node := range nodes {
		fact, hasFacts := facts[node.Name]
		view := deviceView{node: node.Name}
		switch {
		case !hasFacts:
			view.emptyReason = NoDeviceFactsReason
		case isInvalidFact(fact):
			view.invalid = true
			view.emptyReason = InvalidFactReason
		case len(fact.Spec.Devices) == 0:
			view.emptyReason = NoDevicesReason
		default:
			view.records = recordsOf(fact)
		}
		if hasFacts && fact.Spec.ObservedAt != nil {
			collected := fact.Spec.ObservedAt.Time
			if observed.IsZero() || collected.Before(observed) {
				observed = collected
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(left, right int) bool { return views[left].node < views[right].node })
	if observed.IsZero() {
		observed = reader.Clock()
	}
	return deviceInventory{views: views, observed: observed}, nil
}

// isInvalidFact reports whether the stored fact object carries the validator's
// validity marker. The marker is the label KOSD-C-02 places on an object whose
// desired state was rejected; the read never trusts such an object as facts
// (DEVICE-C-6).
func isInvalidFact(fact v1beta1.NodeDevice) bool {
	return fact.Labels[markers.Invalid] != ""
}

// deviceList renders the device list item from the inventory (DEVICE-C-3,
// DEVICE-C-4, DEVICE-C-6).
func (r *Readings) deviceList(ctx context.Context) (any, time.Time, *controlplane.Error) {
	inventory, failure := r.inventory(ctx)
	if failure != nil {
		return nil, time.Time{}, failure
	}
	lists := make([]nodeDevices, 0, len(inventory.views))
	for _, view := range inventory.views {
		list := nodeDevices{Name: view.node, Invalid: view.invalid, EmptyReason: view.emptyReason}
		if len(view.records) > 0 {
			list.Devices = entriesOf(view.records)
		}
		lists = append(lists, list)
	}
	return deviceList{Nodes: lists}, inventory.observed, nil
}

// recordsOf lists the device records of one node in identity order. A serial
// number collected more than once appears once, as the newest record; records
// naming no serial number are separate devices, not duplicates of each other
// (NODE-C-70).
func recordsOf(fact v1beta1.NodeDevice) []deviceRecord {
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
	return candidates
}

// entriesOf renders the device records as the list shows them.
func entriesOf(records []deviceRecord) []deviceEntry {
	entries := make([]deviceEntry, 0, len(records))
	for _, record := range records {
		entries = append(entries, record.entry)
	}
	return entries
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
