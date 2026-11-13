package devicemanager

import (
	"testing"
)

// TestDeclarationLoads checks that the kernel accepts the contribution and that
// the page states why it is read-only (KOSD-C-18, DEVICE-C-1).
func TestDeclarationLoads(t *testing.T) {
	loaded, err := Declaration().Load()
	if err != nil {
		t.Fatalf("load declaration: %v", err)
	}
	if len(loaded.Problems) != 0 {
		t.Fatalf("problems = %+v, want none", loaded.Problems)
	}
	if loaded.Identifier != Identifier || loaded.Version != Version {
		t.Fatalf("loaded %s@%s, want %s@%s", loaded.Identifier, loaded.Version, Identifier, Version)
	}
	if len(loaded.Contributions.Pages) != 1 || loaded.Contributions.Pages[0].ID != PageDevices {
		t.Fatalf("pages = %+v, want the device page", loaded.Contributions.Pages)
	}
	if !loaded.Contributions.Pages[0].ReadOnly || loaded.Contributions.Pages[0].ReadOnlyReason == "" {
		t.Fatal("the device page does not state why it is read-only")
	}
}
