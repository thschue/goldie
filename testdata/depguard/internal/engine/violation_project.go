package engine

// Deliberate violation: engine must not import contract/project. The engine
// rule reports it (it denies all of internal/contract), not project-detection;
// the engine clause in project-detection stays as defence in depth should the
// engine rule ever loosen (the server never detects a Project from its own
// working directory, #233). ./run check lints this fixture module and fails
// unless depguard reports it.
import _ "github.com/thschue/goldie/internal/contract/project"
