package devicemanager

import (
	"github.com/tsic404/kos-sdk/packaging"
)

// Build returns the product's installable package and its archive bytes. The
// artifact goes through the SDK's one packaging path, so a first-party system
// application and a third-party extension produce the same structure
// (KOSD-C-17, SDK-C-1, SDK-C-4).
func Build(assets []packaging.Asset) (packaging.Package, []byte, error) {
	document, err := Document()
	if err != nil {
		return packaging.Package{}, nil, err
	}
	return packaging.Pack(document, assets)
}
