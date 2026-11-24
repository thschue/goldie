// Package examples embeds examples/demo, a neutral demo platform: the example
// content goldie server --dev serves from a directory with none of its own.
// The directory is the one source of truth: the binary embeds it as it is.
// goldie's own agents and loops stay in examples/knowledge and are not
// embedded.
package examples

import (
	"embed"
	"io/fs"
)

//go:embed all:demo
var demo embed.FS

// Knowledge is the example content, laid out like a content directory.
func Knowledge() fs.FS {
	sub, err := fs.Sub(demo, "demo")
	if err != nil {
		panic(err) // "demo" is a valid path and is embedded
	}
	return sub
}
