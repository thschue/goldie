package app

// Allowed: mcpstdio.go is the stdio role, which detects the Project on the
// user's machine. The depguard fixture check fails if this file is reported.
import _ "github.com/thschue/goldie/internal/contract/project"
