import { readdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { parseRole } from "./parse.js";
import type { RoleDef } from "./schema.js";

export function builtinRoles(): RoleDef[] {
  const dir = join(dirname(fileURLToPath(import.meta.url)), "builtin");
  return readdirSync(dir)
    .filter((name) => name.endsWith(".md"))
    .sort()
    .map((name) => parseRole(readFileSync(join(dir, name), "utf8"), "builtin"));
}
