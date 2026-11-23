package mcp

// Deliberate violation: mcp must not import contract/project (the server-side
// MCP takes only plain values: WithProject, WithProjectProblem and
// WithRejectedRemote; only internal/app/mcpstdio.go detects, #259). ./run check
// lints this fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/contract/project"
