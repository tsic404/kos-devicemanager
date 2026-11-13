package devicemanager

import (
	"encoding/json"
	"fmt"

	sdkextension "github.com/tsic404/kos-sdk/extension"
	"github.com/tsic404/kos-sdk/manifest"
	"github.com/tsic404/kosd/api"
	"github.com/tsic404/kosd/pkg/extension"
)

// Manifest is the declaration document the installable package carries
// (KOSD-C-18). It is built from the same page the kernel loads, so the artifact
// and the running contribution cannot name different items (SDK-C-3).
func Manifest() (manifest.ExtensionManifest, error) {
	page, err := pageDocument(DevicesPage())
	if err != nil {
		return manifest.ExtensionManifest{}, err
	}
	return manifest.ExtensionManifest{
		Identifier:       Identifier,
		Version:          Version,
		FrameworkVersion: extension.FrameworkVersion,
		Contributions: manifest.Contributions{
			Pages: []map[string]any{page},
		},
		Dependencies: manifest.Dependencies{},
		Permissions:  []manifest.ExtensionPermission{readPermission()},
		// The product holds no configuration and declares no object class, so
		// both blocks are present and empty (KOSD-C-18).
		Configuration: manifest.Configuration{},
		Retention:     []manifest.Retention{},
	}, nil
}

// readPermission states the page's read scope as the package carries it. The
// empty group is the core group, so the manifest names the Node read itself
// instead of leaving it to a second statement (KOSD-C-18).
func readPermission() manifest.ExtensionPermission {
	return manifest.ExtensionPermission{
		Contribution: PageDevices,
		ReadScope: manifest.ReadScope{
			Groups: []string{"", api.Group},
			Kinds: []sdkextension.GroupKind{
				{Kind: "Node"},
				{Group: api.Group, Kind: "NodeDevice"},
			},
		},
		RequiredRole: string(manifest.RoleGuest),
	}
}

// Document returns the manifest as the JSON value the SDK checks and packs.
func Document() (map[string]any, error) {
	declaration, err := Manifest()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		return nil, fmt.Errorf("encode declaration: %w", err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode declaration: %w", err)
	}
	return document, nil
}

// pageDocument renders one page as the package carries it. The payload is the
// kernel page shape, because the artifact states the contribution and the
// kernel loads it as declared (KOSD-C-18).
func pageDocument(page extension.Page) (map[string]any, error) {
	encoded, err := json.Marshal(page)
	if err != nil {
		return nil, fmt.Errorf("encode page %s: %w", page.ID, err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, fmt.Errorf("decode page %s: %w", page.ID, err)
	}
	return document, nil
}
