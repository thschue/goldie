# Adding a skill to a Source

A skill is a directory `skills/<name>/` in a Scope of one of goldie's Sources:

- `global/skills/<name>/` for every Project;
- `projects/<host>/<org>/<repo>/skills/<name>/` for one Project.

The directory holds a `SKILL.md` and any other files the skill needs. The `description` in the
frontmatter of `SKILL.md` shows on the skill's card in the Agent assets page. Agents get the skill
as the MCP prompt `goldie-<name>`.

```markdown
---
description: Deploys the app to Kubernetes.
status: published
---
```

The `status` in the frontmatter decides whether the skill is served:

- `published`, or no `status` (or no frontmatter): served.
- `deprecated`: still served.
- `draft`: left out of the list and not served.
- Any other value, or frontmatter that cannot be parsed: treated as a draft, so a mistake never
  publishes a skill meant to be hidden.

A bundle with a file under `scripts/` is a script skill (`requires_sync`). Agents won't run it from
text: run `goldie pull` to install it.

The nearest Scope wins by name, with its whole bundle. A Project is registered by adding a
`projects/<host>/<org>/<repo>/` directory to a Source.
