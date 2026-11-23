package main

// Allowed: cmd/goldie wires the built-in examples into goldie server --dev.
// The depguard fixture check fails if this file is reported.
import _ "github.com/thschue/goldie/examples"

func main() {}
