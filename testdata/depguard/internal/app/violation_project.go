package app

// Deliberate violation: app must not import contract/project outside
// mcpstdio.go (the server never detects a Project from its own working
// directory, #233). ./run check lints this fixture module and fails unless
// depguard reports it.
import _ "github.com/thschue/goldie/internal/contract/project"
