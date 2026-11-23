# Triage labels

Skills and loops speak in five canonical triage roles. In this repository each role's label
string is the same as its name:

| Role | Label | Meaning |
|---|---|---|
| needs-triage | `needs-triage` | Needs evaluation |
| needs-info | `needs-info` | Waiting on a named fact or decision |
| ready-for-agent | `ready-for-agent` | Fully specified; an agent can pick it up with no human context |
| ready-for-human | `ready-for-human` | Needs a human: credentials, release operations, a design call |
| wontfix | `wontfix` | Will not be actioned |

Supporting labels:
- `epic`: tracks sub-issues and is never claimed directly
- `grilling-needed`: an open design question; run `/grilling` before it can be ready
- `question`: a maintainer decision (issues); a product question awaiting its answer (PRs)
- `priority:P1` (bugs, security, the work that unblocks the most; at most 2 besides bugs),
  `priority:P2` (value work, the default), `priority:P3` (polish): one on every ready issue
- `bug`: ranks first; `security`: a tag (MEDIUM+ goes to a private advisory)

- `area:engine`, `area:api`, `area:cli`, `area:mcp`, `area:platform`, `area:ui`, `area:contract`:
  the lane, set by the product session
- `harness`: about the loops themselves; only the maintainer works it

The loops add two flow labels, `in-progress` and `needs-maintainer`, defined in the
[shared rules](./README.md#labels) with the `needs-info` reasons.
