package mcp

// Deliberate violation: mcp must not import cli. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/cli"
