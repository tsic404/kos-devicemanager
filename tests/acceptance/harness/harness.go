// Package harness builds the world a device-view acceptance case runs against:
// an API surface holding the Node and NodeDevice objects, the install
// evaluation that projects the declared pages, and the panel shell over the
// product's readings. The shell, install evaluation and cluster reader are the
// extension host's; the cases judge this product's declaration and readings
// through them (DASH-C-22, KOSD-C-53).
package harness

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/api/v1beta1"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/identity"
	"github.com/tsic404/kosd/pkg/pages"
	"github.com/tsic404/kosd/pkg/write"
)

var (
	nodes = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	facts = schema.GroupVersionResource{
		Group:    api.Group,
		Version:  v1beta1.GroupVersion.Version,
		Resource: "nodedevices",
	}
)

// Cluster is the API surface the cases read.
type Cluster struct {
	// Dynamic holds the Node and NodeDevice objects.
	Dynamic *dynamicfake.FakeDynamicClient
}

// Start returns an empty cluster.
func Start() *Cluster {
	return &Cluster{Dynamic: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			nodes: "NodeList",
			facts: "NodeDeviceList",
		})}
}

// Node creates one cluster node.
func (c *Cluster) Node(t *testing.T, name string) {
	t.Helper()
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]any{"name": name},
		"status": map[string]any{
			"capacity": map[string]any{
				"cpu":    "8",
				"memory": "32Gi",
			},
		},
	}}
	if _, err := c.Dynamic.Resource(nodes).Create(context.Background(), object, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create node %s: %v", name, err)
	}
}

// Device is one device fact entry a case states.
type Device struct {
	Name   string
	Serial string
	Model  string
	Kind   string
	// Layers are the four independent fact layers of the entry; nil means the
	// collector reported the device on all four (NODE-C-70).
	Layers *Layers
	// Occupancy is the system holding the device; nil means the entry carries
	// no occupancy, which is a free device (NODE-C-70).
	Occupancy *Occupancy
	// ObservedAt is the entry's own collection time; nil leaves the entry
	// without one, the oldest position among its serial number (NODE-C-70).
	ObservedAt *time.Time
}

// Layers are the four independent facts of one device entry; one being true
// never implies another (NODE-C-37).
type Layers struct {
	Present     bool
	Recognized  bool
	DriverReady bool
	Published   bool
}

// Occupancy is the system holding a device entry; an empty Reference means the
// collection did not name the object inside that system (NODE-C-70).
type Occupancy struct {
	Source    string
	Reference string
}

// Collected creates the device fact object of one node; the case states the
// collection time the fact carries (NODE-C-70).
func (c *Cluster) Collected(t *testing.T, node, collector string, observedAt time.Time, devices ...Device) {
	t.Helper()
	entries := make([]v1beta1.DeviceFact, 0, len(devices))
	for _, device := range devices {
		layers := Layers{Present: true, Recognized: true, DriverReady: true, Published: true}
		if device.Layers != nil {
			layers = *device.Layers
		}
		entry := v1beta1.DeviceFact{
			Name: device.Name, Serial: device.Serial, Model: device.Model, Kind: device.Kind,
			IsPresent: layers.Present, IsRecognized: layers.Recognized,
			IsDriverReady: layers.DriverReady, IsPublished: layers.Published,
		}
		if device.Occupancy != nil {
			entry.Occupancy = &v1beta1.DeviceOccupancy{
				Source:    device.Occupancy.Source,
				Reference: device.Occupancy.Reference,
			}
		}
		if device.ObservedAt != nil {
			entry.ObservedAt = &metav1.Time{Time: *device.ObservedAt}
		}
		entries = append(entries, entry)
	}
	fact := v1beta1.NodeDevice{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1beta1.GroupVersion.String(), Kind: "NodeDevice"},
		ObjectMeta: metav1.ObjectMeta{Name: node},
		Spec: v1beta1.NodeDeviceSpec{
			Node:       node,
			Collector:  collector,
			ObservedAt: &metav1.Time{Time: observedAt},
			Devices:    entries,
		},
	}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&fact)
	if err != nil {
		t.Fatalf("encode device facts of %s: %v", node, err)
	}
	if _, err := c.Dynamic.Resource(facts).Create(context.Background(), &unstructured.Unstructured{Object: object}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create device facts of %s: %v", node, err)
	}
}

// Snapshot renders every object of the cluster, ordered by identity, so a case
// can show that a read or an uninstall changed nothing.
func (c *Cluster) Snapshot(t *testing.T) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	for _, resource := range []schema.GroupVersionResource{nodes, facts} {
		list, err := c.Dynamic.Resource(resource).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatalf("list %s: %v", resource.Resource, err)
		}
		items := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			encoded, err := json.Marshal(item.Object)
			if err != nil {
				t.Fatalf("encode %s: %v", resource.Resource, err)
			}
			items = append(items, string(encoded))
		}
		sort.Strings(items)
		snapshot[resource.Resource] = strings.Join(items, "\n")
	}
	return snapshot
}

// Component is one system component request a case states.
type Component struct {
	// Name is the system component object's name; the kernel identifies the
	// product by its declaration, never by this name (KOSD-C-17).
	Name string
	// Declaration is what the package carries.
	Declaration extension.Declaration
	// IsDisabled is the component's disable request (KOSD-C-28).
	IsDisabled bool
	// IsTerminating marks a component whose deletion is under way.
	IsTerminating bool
	// Created orders the install requests.
	Created time.Time
}

// Install evaluates the components as the extension host does: every request
// carries a package artifact, and the digest binds the package content
// (KOSD-C-17, KOSD-C-28).
func Install(t *testing.T, components ...Component) extension.Assessment {
	t.Helper()
	requests := make([]extension.Request, 0, len(components))
	for _, component := range components {
		content, digest, err := extension.Pack(component.Declaration, nil)
		if err != nil {
			t.Fatalf("pack declaration of %s: %v", component.Name, err)
		}
		requests = append(requests, extension.Request{
			Name:          component.Name,
			Created:       component.Created,
			IsDisabled:    component.IsDisabled,
			IsTerminating: component.IsTerminating,
			Package: &extension.Package{
				Reference: component.Declaration.Identifier + "@" + component.Declaration.Version,
				Digest:    digest,
				Content:   content,
			},
		})
	}
	return extension.Evaluate(requests)
}

// Shell renders the pages the active components declare over the product's
// readings.
func Shell(assessment extension.Assessment, readings pages.Readings, now func() time.Time) *pages.Service {
	return pages.New(assessment.Registry, pages.Config{Readings: readings, Now: now})
}

// Session is the account a case reads as.
func Session(role write.Role) identity.Session {
	return identity.Session{Username: "operator", Role: role}
}

// PageIDs lists the page identities a shell offers, ordered as the shell lists
// them.
func PageIDs(service *pages.Service, role write.Role) []string {
	summaries := service.List(Session(role))
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	return ids
}

// ItemOf returns one rendered item by identity.
func ItemOf(page pages.Page, id string) (pages.Item, bool) {
	for _, item := range page.Items {
		if item.ID == id {
			return item, true
		}
	}
	return pages.Item{}, false
}

// SummaryIDs lists the identities of the pages one assessment's registry holds,
// ordered by identity.
func SummaryIDs(summaries []pages.Summary) []string {
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.ID)
	}
	sort.Strings(ids)
	return ids
}

// Contains reports whether values holds target.
func Contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// Without returns values without target.
func Without(values []string, target string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			kept = append(kept, value)
		}
	}
	return kept
}

// EqualStrings reports whether two ordered string lists are equal.
func EqualStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// SameSnapshot reports whether two cluster snapshots are equal.
func SameSnapshot(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

// Wire decodes a page value as the browser receives it, so a case asserts the
// fields the page carries instead of the Go type behind them.
func Wire(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode the page value: %v", err)
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode the page value: %v", err)
	}
	return decoded
}

// Entries returns the object entries of a list field; an absent list is an
// empty one, which is how an empty state travels.
func Entries(t *testing.T, object map[string]any, key string) []map[string]any {
	t.Helper()
	raw, found := object[key]
	if !found || raw == nil {
		return nil
	}
	list, isList := raw.([]any)
	if !isList {
		t.Fatalf("field %q = %T, want a list", key, raw)
	}
	entries := make([]map[string]any, 0, len(list))
	for index, entry := range list {
		decoded, isObject := entry.(map[string]any)
		if !isObject {
			t.Fatalf("entry %d of %q = %T, want an object", index, key, entry)
		}
		entries = append(entries, decoded)
	}
	return entries
}

// Object returns one nested object field.
func Object(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	raw, found := object[key]
	if !found {
		t.Fatalf("object carries no %q field: %+v", key, object)
	}
	nested, isObject := raw.(map[string]any)
	if !isObject {
		t.Fatalf("field %q = %T, want an object", key, raw)
	}
	return nested
}

// String returns one text field; a missing field or another type fails the
// case, so a field the page stops carrying is not read as empty.
func String(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	raw, found := object[key]
	if !found {
		t.Fatalf("object carries no %q field: %+v", key, object)
	}
	text, isText := raw.(string)
	if !isText {
		t.Fatalf("field %q = %T, want a string", key, raw)
	}
	return text
}

// Bool returns one boolean field; a missing field or another type fails the
// case.
func Bool(t *testing.T, object map[string]any, key string) bool {
	t.Helper()
	raw, found := object[key]
	if !found {
		t.Fatalf("object carries no %q field: %+v", key, object)
	}
	flag, isBool := raw.(bool)
	if !isBool {
		t.Fatalf("field %q = %T, want a boolean", key, raw)
	}
	return flag
}

// Keys returns the sorted field names of one decoded object.
func Keys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ByName indexes decoded entries by their name field.
func ByName(t *testing.T, entries []map[string]any) map[string]map[string]any {
	t.Helper()
	indexed := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		indexed[String(t, entry, "name")] = entry
	}
	return indexed
}
