//go:build unix

package cli

import "syscall"

// openFlags keeps opening a FIFO or a terminal from blocking, and from making
// a terminal the controlling one. A regular file reads the same either way.
const openFlags = syscall.O_NONBLOCK | syscall.O_NOCTTY
