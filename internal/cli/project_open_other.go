//go:build !unix

package cli

// openFlags adds nothing where opening a file has no non-blocking mode.
const openFlags = 0
