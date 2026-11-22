// Command configdocs writes the configuration reference Markdown from the
// settings registry: configdocs <output>. `./run regen` calls it.
package main

import (
	"fmt"
	"os"

	"github.com/thschue/goldie/internal/platform"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: configdocs <output>")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], platform.ConfigReference(platform.Settings), 0o644); err != nil { //nolint:gosec // generated docs are world-readable
		fmt.Fprintln(os.Stderr, "configdocs:", err)
		os.Exit(1)
	}
}
