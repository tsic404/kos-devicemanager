package devicemanager

import (
	"context"
	"time"

	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/device"
)

// staleFactAfter is NODE-C-37's staleness bound: a device fact not refreshed for
// longer than this is marked stale. The bound is the referenced project's, read
// from the kernel's own device rules instead of restated here (NODE-C-37).
const staleFactAfter = device.StaleAfter

// layerAlert is one fact layer that does not hold on one device: the device and
// the layer, without a cause and without a repair (DEVICE-C-5).
type layerAlert struct {
	Node   string `json:"node"`
	Device string `json:"device"`
	Layer  string `json:"layer"`
}

// layerAlertList is the missing-layer alert item value. Each missing layer of
// each device is its own alert, so one layer never stands in for another
// (DEVICE-C-5).
type layerAlertList struct {
	Alerts []layerAlert `json:"alerts"`
}

// layerAlerts resolves the alerts of every device layer that does not hold. The
// alerts derive from the four fact layers alone: they name no cause, and the
// item offers no repair, re-collection or hardware entry. The observation is
// the oldest collection time the facts state (DEVICE-C-5, DEVICE-C-1).
func (r *Readings) layerAlerts(ctx context.Context) (any, time.Time, *controlplane.Error) {
	inventory, failure := r.inventory(ctx)
	if failure != nil {
		return nil, time.Time{}, failure
	}
	alerts := make([]layerAlert, 0)
	for _, view := range inventory.views {
		for _, record := range view.records {
			for _, layer := range missingLayersOf(record.entry) {
				alerts = append(alerts, layerAlert{Node: view.node, Device: record.entry.Name, Layer: layer})
			}
		}
	}
	return layerAlertList{Alerts: alerts}, inventory.observed, nil
}

// missingLayersOf lists the layers of one device that do not hold, in the order
// the contract names them (NODE-C-37).
func missingLayersOf(entry deviceEntry) []string {
	missing := make([]string, 0, 4)
	if !entry.Present {
		missing = append(missing, "present")
	}
	if !entry.Recognized {
		missing = append(missing, "recognized")
	}
	if !entry.DriverReady {
		missing = append(missing, "driverReady")
	}
	if !entry.Published {
		missing = append(missing, "published")
	}
	return missing
}

// staleDevice is one device whose collection is older than the bound: the
// device and the moment it was last observed (DEVICE-C-6).
type staleDevice struct {
	Node           string `json:"node"`
	Device         string `json:"device"`
	LastObservedAt string `json:"lastObservedAt"`
}

// factStalenessList is the fact staleness item value: the devices beyond
// NODE-C-37's bound with the moment each was last observed. The read's own
// freshness is the item's stale flag, a separate marker that neither replaces
// this list nor is replaced by it (DEVICE-C-6, KOSD-C-13).
type factStalenessList struct {
	Devices []staleDevice `json:"devices"`
}

// factStaleness resolves the devices whose last observation is older than
// NODE-C-37's bound. A device whose collection states no time cannot be judged
// and is not marked; the bound is strict, so a fact exactly at it is still
// fresh (DEVICE-C-6).
func (r *Readings) factStaleness(ctx context.Context) (any, time.Time, *controlplane.Error) {
	inventory, failure := r.inventory(ctx)
	if failure != nil {
		return nil, time.Time{}, failure
	}
	now := r.reader().Clock()
	stale := make([]staleDevice, 0)
	for _, view := range inventory.views {
		for _, record := range view.records {
			if record.observedAt.IsZero() || now.Sub(record.observedAt) <= staleFactAfter {
				continue
			}
			stale = append(stale, staleDevice{
				Node:           view.node,
				Device:         record.entry.Name,
				LastObservedAt: collectedText(record.observedAt),
			})
		}
	}
	return factStalenessList{Devices: stale}, inventory.observed, nil
}
