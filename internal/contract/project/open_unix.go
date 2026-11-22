//go:build unix

package project

import "syscall"

// openFlags keeps opening a FIFO or a terminal from blocking, and from making
// a terminal the controlling one. A regular file reads the same either way.
const openFlags = syscall.O_NONBLOCK | syscall.O_NOCTTY

// noFollow makes WriteConfig's open fail on a symlinked config.yaml, on top of
// O_EXCL, which already refuses any existing entry.
const noFollow = syscall.O_NOFOLLOW
