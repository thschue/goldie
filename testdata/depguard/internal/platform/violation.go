package platform

// Deliberate violation: platform must not import app. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/app"
