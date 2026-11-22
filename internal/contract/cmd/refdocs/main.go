// Command refdocs writes the endpoint reference Markdown for an OpenAPI
// document: refdocs <spec> <output>. `./run regen` calls it.
package main

import (
	"fmt"
	"os"

	"github.com/thschue/goldie/internal/contract"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: refdocs <spec> <output>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "refdocs:", err)
		os.Exit(1)
	}
}

func run(specPath, outPath string) error {
	spec, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	out, err := contract.Reference(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, out, 0o644) //nolint:gosec // generated docs are world-readable
}
