package app

// Deliberate violation: only cmd/goldie may import examples. ./run check lints
// this fixture module and fails unless depguard reports it.
import _ "github.com/thschue/goldie/examples"
