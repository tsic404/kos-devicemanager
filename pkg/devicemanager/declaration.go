// Package devicemanager is the kos-devicemanager product: the read-only device
// view of the panel. It declares the device page and resolves its node summary
// from the Node objects and the device facts the node collector wrote. The page
// offers no write entry, because device facts belong to the collector and
// hardware actions are outside the product (DEVICE-C-1, DEVICE-C-2).
package devicemanager

import (
	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/extension"
	"github.com/tsic404/kosd/pkg/write"
)

// Identifier is the product's extension identity across versions (KOSD-C-17).
const Identifier = "kos-devicemanager"

// Version is the package version this product ships.
const Version = "1.0.0"

// PageDevices is the device page (DEVICE-C-1).
const PageDevices = "devices"

// ItemNodeSummary is the node identity and hardware summary item (DEVICE-C-2).
const ItemNodeSummary = "node-summary"

// ReadOnlyReason explains why the page offers no modification entry. The fact
// side is the collector's alone, and hardware actions are outside the product
// (DEVICE-C-1, KOSD-NG-8).
const ReadOnlyReason = "本页只读：设备事实由节点采集器写入，硬件动作不在设备视图内"

// Declaration returns the contribution kos-devicemanager ships (KOSD-C-18). It
// states the same read scope the package manifest carries; a unit test holds the
// two together.
func Declaration() extension.Declaration {
	return extension.Declaration{
		Identifier:       Identifier,
		Version:          Version,
		FrameworkVersion: extension.FrameworkVersion,
		Contributions:    extension.Contributions{Pages: []extension.Page{DevicesPage()}},
		Permissions: []extension.Permission{{
			Contribution: PageDevices,
			ReadScope: extension.ReadScope{
				Groups: []string{"", api.Group},
				Kinds: []extension.GroupKind{
					{Kind: "Node"},
					{Group: api.Group, Kind: "NodeDevice"},
				},
			},
			RequiredRole: string(write.RoleGuest),
		}},
	}
}

// DevicesPage is the device page the shell renders (DEVICE-C-1). Each declared
// item carries its source type, so a reader can tell an object source from a
// computed one without guessing (KOSD-C-53).
func DevicesPage() extension.Page {
	return extension.Page{
		ID:             PageDevices,
		Title:          "设备",
		Location:       "panel",
		ReadOnly:       true,
		ReadOnlyReason: ReadOnlyReason,
		Items: []extension.PageItem{{
			ID:        ItemNodeSummary,
			Label:     "节点硬件摘要",
			Source:    extension.PageSourceObject,
			Reference: Identifier + "/" + ItemNodeSummary,
		}},
	}
}
