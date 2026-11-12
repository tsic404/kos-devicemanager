// Command kosdevicemanager builds the product's installable package. Packaging
// goes through the SDK, so the artifact and its digest come from the one path
// every author uses (SDK-C-1, SDK-C-4).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsic404/kos-devicemanager/pkg/devicemanager"
)

func main() {
	directory := flag.String("out", "dist", "directory the artifact is written to")
	flag.Parse()
	if err := run(*directory); err != nil {
		fmt.Fprintln(os.Stderr, "build package:", err)
		os.Exit(1)
	}
}

// run writes the package archive and the digest an install request names.
func run(directory string) error {
	pkg, archive, err := devicemanager.Build(nil)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	path := filepath.Join(directory, devicemanager.Identifier+"-"+devicemanager.Version+".tar.gz")
	if err := os.WriteFile(path, archive, 0o644); err != nil {
		return fmt.Errorf("write package: %w", err)
	}
	if err := os.WriteFile(path+".digest", []byte(pkg.Digest+"\n"), 0o644); err != nil {
		return fmt.Errorf("write digest: %w", err)
	}
	fmt.Printf("%s\n%s\n", path, pkg.Digest)
	return nil
}
