// Command goldie is the single goldie binary. Its roles (server, mcp and the
// client commands) are wired together in internal/app.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/thschue/goldie/internal/platform"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "goldie:", err)
		os.Exit(1)
	}
}

// run is the testable entry point. The roles arrive with later issues.
func run(args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "version" {
		_, err := fmt.Fprintln(out, platform.Version)
		return err
	}
	return fmt.Errorf("no commands yet (try: goldie version)")
}
