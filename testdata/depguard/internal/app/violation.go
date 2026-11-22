package app

// Deliberate violation: production code must not import contracttest. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/contract/contracttest"
