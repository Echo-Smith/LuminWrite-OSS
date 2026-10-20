import type { RoleDef } from "./schema.js";

const SOURCE_TOOLS = new Set(["search_kb", "read_source", "cite_evidence"]);

export function allowedToolNames(role: RoleDef): string[] {
  const denied = new Set(role.tools.deny ?? []);
  const names = role.tools.allow.filter((name) => !denied.has(name));
  if (role.scope.includeSources) return names;
  return names.filter((name) => !SOURCE_TOOLS.has(name));
}
