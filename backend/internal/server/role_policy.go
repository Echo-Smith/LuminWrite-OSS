// Default role capability grants (V3.0 M1.5 slice 3, docs/25 §25.4): the
// editorial roles' visibility over the unified capability view, following
// the role isolation design (docs/20 §20.7 / roadmap §11). These grants are
// the resolver-side plumbing only — dispatch enforcement points arrive with
// the Role Capability Policy milestone (V3.0 M5); nothing here changes what
// the editorial DAG or the governed runtime actually execute today.
package server

import "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/capability"

// editorialRolePolicy grants each editorial DAG role the capability-id
// prefixes its role design allows, over the unified view.
//
//	researcher → external research, retrieval tooling, KB, evidence chain
//	writer     → document drafting/styling tooling, memory, editorial writing tools
//	reviewer   → validators, quality evidence, editorial review tools
//	admin      → everything (operator posture, AllowAll)
func editorialRolePolicy() capability.RolePolicy {
	return capability.PrefixRolePolicy{Grants: map[string][]string{
		"researcher": {
			"tool.mcp__", // MCP tools are external-system access by design
			"editorial.tool.search", "editorial.tool.search_knowledge", "editorial.tool.fetch_url",
			"editorial.agent.researcher",
			"core.retrieval.search", "core.retrieval.strict_search", "core.validation.evidence", "core.validation.fact",
		},
		"writer": {
			"editorial.tool.write", "editorial.tool.polish", "editorial.tool.factcheck",
			"editorial.agent.writer",
			"core.outline.generate", "core.draft.generate", "core.document.finalize",
		},
		"reviewer": {
			"editorial.tool.factcheck",
			"editorial.agent.reviewer",
			"core.validation.quality", "core.validation.evidence", "core.validation.fact",
		},
		"admin": {"*"},
	}}
}
