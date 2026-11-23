# Architecture decision records

Decisions that are hard to reverse, surprising and a real trade-off. Accepted ones bind agents.
To change one, add a new ADR that supersedes it; do not rewrite history. Format: Context, Decision,
Consequences.

- [0001. A single binary with modules and roles](0001-single-binary-with-modules-and-roles.md)
- [0002. Clients are pure HTTP clients; local mode is an embedded server](0002-clients-are-pure-http-clients.md)
- [0003. MCP as its own component](0003-mcp-as-its-own-component.md)
- [0004. Sources with Claims and preference](0004-sources-with-claims-and-preference.md)
- [0005. Keyword-only search](0005-keyword-only-search.md)
- [0006. On-demand delivery with an optional pull](0006-on-demand-and-pull-delivery.md)
- [0007. Managed instruction blocks](0007-managed-instruction-blocks.md)
- [0008. Auth model: named tokens and GitHub, with cookie sessions](0008-auth-model.md)
- [0009. OpenAPI 3.1, spec first, with code generation](0009-openapi-spec-first-with-codegen.md)
- [0010. Vue with a design-token structure and goldie's own blue brand](0010-vue-design-tokens-and-blue-brand.md)
- [0011. Versioning policy: additive-only contract](0011-versioning-policy.md)
- [0012. Agent loops: mechanical gates, a PR-centric handoff and review lenses](0012-agent-loop-gates-and-handoff.md)
- [0013. The reviewer judges changes to the task runner](0013-reviewer-judges-check-tooling-changes.md)
- [0014. The error envelope always carries a next-step hint](0014-error-envelope-always-carries-a-hint.md)
- [0015. Article identity is its path; frontmatter name is the title](0015-article-identity-is-its-path.md)
- [0016. Nested article ids travel as one percent-encoded path segment](0016-nested-article-ids-are-one-encoded-path-segment.md)
- [0017. Observability through OpenTelemetry, configured by the standard OTEL_* variables](0017-observability-through-opentelemetry.md)
- [0018. A product session and delivery streams replace the three cooperating loops](0018-product-and-delivery-sessions.md)
- [0019. The SPA's build output is not committed; the binary embeds it when built, otherwise a placeholder](0019-ui-build-output-not-committed.md)
- [0020. Project identity is exactly host/org/repo, matched case-insensitively](0020-project-identity-is-host-org-repo-matched-case-insensitively.md)
- [0021. Item references are `<kind>:<name>`, resolved in the referrer's view](0021-item-references-resolve-in-the-referrers-view.md)
- [0022. Federated activity across replicas](0022-federated-activity-across-replicas.md)
- [0023. Git Source URLs are operator-trusted](0023-git-source-urls-are-operator-trusted.md)
- [0024. API wire names are snake_case](0024-api-wire-names-are-snake-case.md)
- [0025. Role rules are marked once in the spec, with `x-goldie-role`](0025-role-rules-are-marked-once-in-the-spec.md)
- [0026. Item digests](0026-item-digests.md)
