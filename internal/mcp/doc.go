// Package mcp is the MCP component. It reaches the API only through the
// contract client, never through the engine or api packages.
//
// New returns a server with ten tools. Each takes an optional project (an
// ASCII ProjectId, lower-cased and checked before any request) except
// list_projects and current_context, which take nothing:
//
//	search_knowledge: a query
//	get_article:      an id, from search_knowledge
//	find_skills:      a query
//	get_skill:        a name, and an optional file, a path from the skill's file list
//	list_skills:      only the project
//	list_agents:      only the project
//	list_loops:       only the project
//	get_loop:         a loop name
//	list_projects:    nothing
//	current_context:  nothing
//
// current_context reports the
// default Project set with WithProject, the problem set with
// WithProjectProblem, or that there is none. An omitted or empty project
// takes the default, or fails with the problem before any request. The
// contract client it is given carries the caller's credential (the static API
// token today) and sends it on every call. API errors become tool errors with
// the envelope's message and hint, and a rate-limited call says how many
// seconds to wait, from the Retry-After the agent cannot see. Error answers
// are read by contract.Answer, which hands only a 200 to the generated parser.
// With the Local option (goldie mcp --dir) a server that cannot be reached is
// named the embedded server, with no address hint.
// Text from the API is untrusted, so it is cleaned and only ever returned as
// tool output, never as tool metadata. get_article of a deprecated article
// starts with the notice goldie lookup prints on stderr, naming the cleaned
// replacement. MCP never asks for drafts.
//
// Prompts and resources follow one pattern for each Kind that gets them. A
// prompt is one per skill, named goldie-<name>, for the session's default
// Project; getting it returns the cleaned SKILL.md, not quoted with "| " the
// way get_skill and the resources are. The list is read on a session's first
// prompts request and kept for the session, so it is fixed from then on; the
// SDK's one server-wide prompt table cannot say that, so an unknown prompt is
// told to call list_skills or to restart the MCP session, and one receiving
// middleware answers prompts/list and prompts/get. The first later Kind with
// prompts turns it into per-Kind providers: lists concatenated, gets
// dispatched by name. The name goldie-<skill> is pinned by #411, so a later
// Kind must pick a Kind-qualified name.
// A resource is a goldie://<kind>/{name}[/{path}] template: goldie://skills/
// {name}/{+path} (reserved expansion, so the path keeps its slashes) for a
// file of a bundle (quoted like get_skill) and
// goldie://agents/{name} for an agent. A URI is parsed before any request and
// never selects a Project; a miss is the protocol's resource-not-found error,
// which names no address, path or token. A later Kind adds its template and
// its handler the same way. The server sends no list_changed notification
// (prompts and resources are advertised without listChanged), for the reason
// the tools are not: nothing in the component sees a sync, and a client
// waiting for one could hold up a drain.
//
// The integration tests are in internal/app, because this package may not
// import the API or the composition root, not even in tests.
package mcp
