package devicemanager

import (
	"context"
	"sort"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/controlplane"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/pages/cluster"
)

// NoFactsReason explains a node whose collection is missing. The node stays on
// the page; only its summary is marked unobservable (DEVICE-C-2).
const NoFactsReason = "该节点尚无设备事实：采集缺失，硬件摘要不可观察"

// Readings resolves the items the device page declares (KOSD-C-53). It reads
// the Node objects and the device facts the collector wrote, and never writes.
type Readings struct {
	// Dynamic reads the cluster.
	Dynamic dynamic.Interface
	// Now supplies the read clock; defaults to time.Now.
	Now func() time.Time
}

// Read implements pages.Readings. An object item's observation is the
// collection time the object carries, not the read clock: the projection does
// not pretend inputs it did not observe are fresh (DEVICE-C-1, KOSD-C-13).
func (r *Readings) Read(ctx context.Context, _ identity.Session, item extension.PageItem) (pages.Reading, *controlplane.Error) {
	switch item.ID {
	case ItemNodeSummary:
		value, observedAt, failure := r.nodeSummary(ctx)
		if failure != nil {
			return pages.Reading{}, failure
		}
		return pages.Reading{Value: value, ObservedAt: observedAt}, nil
	default:
		return pages.Reading{}, unknownItem(item.ID)
	}
}

// nodeSummary lists every node with its identity, its hardware summary and the
// collection time the facts carry (DEVICE-C-2). Nodes come from the Node list,
// so a node whose collection is missing is still shown. The item's observation
// is the oldest collection time it rests on, the least fresh input of the
// summary; with no fact at all the read itself is the observation.
func (r *Readings) nodeSummary(ctx context.Context) (any, time.Time, *controlplane.Error) {
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

	summaries := make([]nodeSummary, 0, len(nodes))
	observed := time.Time{}
	for _, node := range nodes {
		fact, hasFacts := facts[node.Name]
		summary := nodeSummary{Name: node.Name, IsObservable: hasFacts && fact.Spec.ObservedAt != nil}
		switch {
		case !hasFacts:
			summary.Reason = NoFactsReason
		case fact.Spec.ObservedAt == nil:
			summary.Reason = "设备事实没有采集时间：该节点的摘要不可观察"
		default:
			collected := fact.Spec.ObservedAt.Time
			hardware := hardwareOf(fact)
			summary.CollectedAt = collected.UTC().Format(time.RFC3339)
			summary.Hardware = &hardware
			if observed.IsZero() || collected.Before(observed) {
				observed = collected
			}
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(left, right int) bool { return summaries[left].Name < summaries[right].Name })
	if observed.IsZero() {
		observed = reader.Clock()
	}
	return map[string]any{"nodes": summaries}, observed, nil
}

// nodeSummary is one node's identity and hardware overview as the page shows
// it.
type nodeSummary struct {
	Name         string           `json:"name"`
	IsObservable bool             `json:"observable"`
	CollectedAt  string           `json:"collectedAt"`
	Hardware     *hardwareSummary `json:"hardware,omitempty"`
	Reason       string           `json:"reason,omitempty"`
}

// hardwareSummary counts the devices the collection reports, grouped by class
// and model. The four fact layers stay on the device list; this is an overview,
// not a merged health state (DEVICE-C-2).
type hardwareSummary struct {
	DeviceCount int                 `json:"deviceCount"`
	Devices     []deviceKindSummary `json:"devices"`
}

// deviceKindSummary is one class and model of the devices a node reports.
type deviceKindSummary struct {
	Kind  string `json:"kind,omitempty"`
	Model string `json:"model,omitempty"`
	Count int    `json:"count"`
}

// hardwareOf summarizes the device facts of one node.
func hardwareOf(fact v1beta1.NodeDevice) hardwareSummary {
	counts := make(map[deviceKind]int, len(fact.Spec.Devices))
	for _, device := range fact.Spec.Devices {
		counts[deviceKind{kind: device.Kind, model: device.Model}]++
	}
	keys := make([]deviceKind, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].kind != keys[right].kind {
			return keys[left].kind < keys[right].kind
		}
		return keys[left].model < keys[right].model
	})
	devices := make([]deviceKindSummary, 0, len(keys))
	for _, key := range keys {
		devices = append(devices, deviceKindSummary{Kind: key.kind, Model: key.model, Count: counts[key]})
	}
	return hardwareSummary{DeviceCount: len(fact.Spec.Devices), Devices: devices}
}

// deviceKind is the grouping key of one reported device.
type deviceKind struct {
	kind  string
	model string
}

// factsByNode indexes the stored device facts by the node they belong to. The
// object name is the node name (NODE-C-70).
func factsByNode(objects []map[string]any) (map[string]v1beta1.NodeDevice, *controlplane.Error) {
	facts := make(map[string]v1beta1.NodeDevice, len(objects))
	for _, object := range objects {
		decoded := v1beta1.NodeDevice{}
		if failure := cluster.Decode(object, &decoded); failure != nil {
			return nil, failure
		}
		facts[decoded.Name] = decoded
	}
	return facts, nil
}

// reader is the cluster read seam for this call.
func (r *Readings) reader() *cluster.Reader {
	return &cluster.Reader{Dynamic: r.Dynamic, Now: r.Now}
}

// unknownItem marks an item this product does not declare.
func unknownItem(id string) *controlplane.Error {
	return &controlplane.Error{
		Code:        controlplane.CodeCapabilityMissing,
		Message:     "kos-devicemanager 没有声明页面条目 " + id,
		Stage:       controlplane.StageCapability,
		SideEffects: controlplane.SideEffectsNone,
	}
}
