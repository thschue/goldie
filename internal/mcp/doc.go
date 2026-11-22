// Package mcp is the MCP component. It reaches the API only through the
// contract client, never through the engine or api packages.
//
// New returns a server with two tools, search_knowledge and get_article. The
// contract client it is given carries the caller's credential (the static API
// token today) and sends it on every call. API errors become tool errors with
// the envelope's message and hint; text from the API is untrusted, so it is
// cleaned and only ever returned as tool output, never as tool metadata.
//
// The integration tests are in internal/app, because this package may not
// import the API or the composition root, not even in tests.
package mcp
