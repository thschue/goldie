package cli

// Deliberate violation: cli must not import ui. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/ui"
