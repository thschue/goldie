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
- `question`: a decision the maintainer has to make
- `priority`: at most 2 open at a time
- `bug`, `security`: ranking hints

The agent loops add four flow labels on top of these: `in-progress`, `in-review`,
`changes-requested` and `needs-maintainer`. The [label contract](./label-contract.md) defines every
label, who may set or remove it, and who may set the first `ready-for-agent`.
