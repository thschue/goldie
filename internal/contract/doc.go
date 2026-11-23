// Package contract holds the OpenAPI document and the Go types and strict
// net/http server interface generated from it (contract.gen.go, written by
// `./run regen`, never edited by hand; `./run check` fails on drift). It also
// holds what every API client shares: NewAPIClient, the hardened client (no
// redirects, a 16 MiB answer cap, X-Goldie-Client), CleanText, CleanLine and
// CleanList for untrusted server text, Answer, which reads one API answer and
// classifies every failure, and ParseOK and DecodeAPIError beneath it for the
// error envelope. Its subpackages hold other shared helpers: contract/project detects
// the Project of a working directory for the CLI and MCP, contract/contracttest
// checks responses against the document and builds example request paths from it.
//
// The hand-written helpers sit beside the generated code: the endpoint
// reference Markdown is rendered from the document, and CheckArticleID rejects
// article ids that URL path cleaning would resolve away. The package imports no
// other goldie module.
//
// The package comment of contract/contracttest describes Conforms, ExamplePath
// and ResolveRef.
package contract
