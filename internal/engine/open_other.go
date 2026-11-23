//go:build !unix

package engine

// openFlags adds nothing where opening a file has no non-blocking mode;
// contentFS still refuses what it opened unless it is a regular file.
const openFlags = 0
