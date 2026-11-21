package api

// Deliberate violation: api must not import mcp. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/mcp"
