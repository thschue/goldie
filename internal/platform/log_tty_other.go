//go:build !linux && !darwin

package platform

import "os"

// isTerminal is false where no terminal check is built: logs stay JSON.
func isTerminal(*os.File) bool { return false }
