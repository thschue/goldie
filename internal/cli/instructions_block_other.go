//go:build !unix

package cli

// openFlags adds nothing where opening a file has no non-blocking mode.
const openFlags = 0

// noFollow adds nothing where there is no O_NOFOLLOW; the Lstat check refuses
// a symlink first.
const noFollow = 0
