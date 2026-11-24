# Demo platform

The built-in example content: `goldie server --dev` serves it from a directory with no content of
its own. It is original content for a small fictional platform, Harbor, and has nothing to do with
goldie's own process, which lives in `examples/knowledge` and is not built in.

`global/` holds:
- four articles: databases, logging, retries and feature flags;
- one skill, `service-health`, with a script;
- one agent, `release-reviewer`;
- one loop, `release-watch`.

`projects/github.com/harbor/checkout/` registers one demo Project, `github.com/harbor/checkout`, with:
- one article that overrides a global one: `retries`;
- one article only the Project has: `payment-flow`.

The binary embeds this directory through `examples/embed.go`. Edit it like any content directory.
