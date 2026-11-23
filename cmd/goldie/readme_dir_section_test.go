package main

import (
	"strings"
	"testing"
)

// #306: the --dir / GOLDIE_DIR paragraph belongs to the CLI section of the
// README, wrapped like its neighbours; the MCP section only points at it.
func TestReadmeDirParagraphSitsUnderTheCLISection(t *testing.T) {
	cli := readmeSection(t, "CLI")
	for _, want := range []string{"--dir", "GOLDIE_DIR", "embedded server"} {
		if !strings.Contains(cli, want) {
			t.Errorf("README section CLI does not document %q", want)
		}
	}
	for i, line := range strings.Split(cli, "\n") {
		if len(line) > 110 && !strings.HasPrefix(line, "|") && !strings.Contains(line, "http") {
			t.Errorf("README section CLI line %d is %d characters, not wrapped like its neighbours: %.60s...", i+1, len(line), line)
		}
	}
	mcp := readmeSection(t, "MCP over stdio")
	if !strings.Contains(mcp, "--dir") {
		t.Error("README section MCP over stdio no longer points at --dir")
	}
	if strings.Contains(mcp, "embedded server over a directory of content") ||
		strings.Contains(mcp, "`GOLDIE_TOKEN` is ignored") {
		t.Errorf("README section MCP over stdio still carries the full --dir paragraph:\n%s", mcp)
	}
}
