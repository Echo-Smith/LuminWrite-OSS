export { overBudget, type RoleSpend } from "./budget.js";
export { builtinRoles } from "./load.js";
export { parseRole, parseYamlSubset } from "./parse.js";
export type { RoleBudget, RoleDef, RoleOrigin, RoleOutput } from "./schema.js";
export { outsideScope, targetedBlockId } from "./scope.js";
export { allowedToolNames } from "./tools.js";

export const phase = "roles" as const;
export const note = "角色是提示词、工具名单和模型槽。何时 delegate 由主写作代理决定。";
