package engine

// Deliberate violation: engine must not import contract/project (the server
// never detects a Project from its own working directory, #233). ./run check
// lints this fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/contract/project"
