package ui

// Deliberate violation: ui must not import contract. ./run check lints this
// fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/internal/contract"
