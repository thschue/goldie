package contract

// Deliberate violation: contract must not import platform. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/platform"
