//go:build unix

package cli

import "syscall"

// openFlags keeps opening a FIFO or a terminal from blocking.
const openFlags = syscall.O_NONBLOCK | syscall.O_NOCTTY

// noFollow makes an open fail on a symlink.
const noFollow = syscall.O_NOFOLLOW
