# 20. Project identity is exactly host/org/repo, matched case-insensitively

Status: accepted

## Context

The engine stores project Scopes under projects/<host>/<org>/<repo>/, keeping the case of org and repo and rejecting Projects that differ only in case (projectDirs, internal/engine/source.go). Clients (CLI, MCP stdio) take Project ids from git remotes, GOLDIE_PROJECT, --project and .goldie/config.yaml, and lower-case them so that SSH, HTTPS and differently cased remotes agree. GitLab subgroups (host/group/sub/repo) do not fit three segments. The engine and the clients cannot share Go code under the module rules.

## Decision

A Project id is exactly three segments: a lower-case DNS host that starts with a letter or digit, then an org and a repo of [A-Za-z0-9._-] that do not start with a dot. The contract defines it once as a ProjectId schema with a pattern; the API validates project= against it. Clients send ids lower-cased. The server matches project= case-insensitively against registered Projects, which is unambiguous because the engine already rejects case-only duplicates. Remotes with more or fewer path segments yield no detected Project; users set --project, GOLDIE_PROJECT or .goldie/config.yaml.

## Consequences

One definition in the contract, with clients and server checking the same pattern; the engine's layout check (validProjectSegment, and the 512-byte cap in projectDirs) stays as the Source-side guard, held to the contract by one shared table of accepted and rejected ids (internal/contract/testdata/project_ids.txt) that the contract and the engine tests both read. GitLab subgroup repositories are not detected automatically and cannot be a project Scope. Widening the shape needs a superseding ADR and a contract-compatibility review (`./run contract-compat`): ProjectId also appears in a response (ProjectList.items), so accepting more segments there changes what an existing value means, which ADR 0011 forbids. Lower-casing on the client means project ids reaching the server are lower case, so telemetry that records them (none does yet) will see lower case, and a UI (#164) will have to show the registered spelling.
