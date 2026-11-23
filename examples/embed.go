// Package examples embeds examples/knowledge, the example content goldie
// server --dev serves from a directory with none of its own. The directory is
// the one source of truth: the binary embeds it as it is.
package examples

import (
	"embed"
	"io/fs"
)

//go:embed all:knowledge
var knowledge embed.FS

// Knowledge is the example content, laid out like a content directory.
func Knowledge() fs.FS {
	sub, err := fs.Sub(knowledge, "knowledge")
	if err != nil {
		panic(err) // "knowledge" is a valid path and is embedded
	}
	return sub
}
