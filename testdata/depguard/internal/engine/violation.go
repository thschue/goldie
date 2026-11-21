package engine

// Deliberate violation: engine must not import api. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/api"
