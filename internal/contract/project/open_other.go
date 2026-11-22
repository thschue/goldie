//go:build !unix

package project

// openFlags adds nothing where opening a file has no non-blocking mode.
const openFlags = 0

// noFollow adds nothing where there is no O_NOFOLLOW: O_EXCL still refuses
// any existing entry, a symlink included.
const noFollow = 0
