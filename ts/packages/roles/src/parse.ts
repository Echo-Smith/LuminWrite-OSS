import { Check, Errors } from "typebox/value";

import { type RoleDef, RoleFrontmatter, type RoleOrigin } from "./schema.js";

function scalar(raw: string): string | number | boolean | null {
  if (raw === "true") return true;
  if (raw === "false") return false;
  if (raw === "null" || raw === "~") return null;
  if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(raw)) return Number(raw);
  if ((raw.startsWith('"') && raw.endsWith('"')) || (raw.startsWith("'") && raw.endsWith("'"))) {
    return raw.slice(1, -1);
  }
  return raw;
}

function nextContent(lines: readonly string[], from: number): string | undefined {
  for (let index = from; index < lines.length; index += 1) {
    const raw = lines[index] ?? "";
    if (!raw.trim() || raw.trimStart().startsWith("#")) continue;
    return raw.trim();
  }
  return undefined;
}

/** 只覆盖内置角色用到的子集：标量、一层对象、字符串列表。 */
export function parseYamlSubset(text: string): Record<string, unknown> {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const root: Record<string, unknown> = {};
  const stack: Array<{ indent: number; value: Record<string, unknown> | unknown[] }> = [
    { indent: -1, value: root },
  ];

  for (let index = 0; index < lines.length; index += 1) {
    const raw = lines[index] ?? "";
    if (!raw.trim() || raw.trimStart().startsWith("#")) continue;
    const indent = raw.search(/\S/);
    const line = raw.trim();
    while (stack.length > 1) {
      const top = stack[stack.length - 1];
      if (!top || indent > top.indent) break;
      stack.pop();
    }
    const current = stack[stack.length - 1];
    if (!current) throw new Error("frontmatter 结构坏了");

    if (line.startsWith("- ")) {
      if (!Array.isArray(current.value)) throw new Error("列表项没有挂在数组上");
      current.value.push(scalar(line.slice(2).trim()));
      continue;
    }

    const colon = line.indexOf(":");
    if (colon <= 0) throw new Error(`无法解析：${line}`);
    const key = line.slice(0, colon).trim();
    const rest = line.slice(colon + 1).trim();
    if (Array.isArray(current.value)) throw new Error("键不能写在列表里");
    if (Object.hasOwn(current.value, key)) throw new Error(`重复字段 ${key}`);
    if (rest) {
      current.value[key] = scalar(rest);
      continue;
    }

    const upcoming = nextContent(lines, index + 1);
    if (upcoming?.startsWith("- ")) {
      const list: unknown[] = [];
      current.value[key] = list;
      stack.push({ indent, value: list });
      continue;
    }
    const child: Record<string, unknown> = {};
    current.value[key] = child;
    stack.push({ indent, value: child });
  }
  return root;
}

function splitFrontmatter(markdown: string): { front: string; body: string } | undefined {
  const match = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(markdown);
  if (!match?.[1]) return undefined;
  return { front: match[1], body: match[2] ?? "" };
}

export function parseRole(markdown: string, origin: RoleOrigin): RoleDef {
  const split = splitFrontmatter(markdown);
  if (!split) throw new Error("缺少 frontmatter");
  const data = parseYamlSubset(split.front);
  if (!Check(RoleFrontmatter, data)) {
    const issues = Errors(RoleFrontmatter, data).map(
      (error) => `${error.instancePath || "/"} ${error.message}`,
    );
    throw new Error(issues.join("；") || "角色 frontmatter 不合法");
  }
  return {
    id: data.id,
    name: data.name,
    version: data.version,
    description: data.description,
    output: data.output,
    tools: {
      allow: [...data.tools.allow],
      ...(data.tools.deny ? { deny: [...data.tools.deny] } : {}),
    },
    scope: { ...data.scope },
    model: {
      slot: data.model.slot,
      ...(data.model.prefer ? { prefer: [...data.model.prefer] } : {}),
      ...(data.model.temperature !== undefined ? { temperature: data.model.temperature } : {}),
    },
    budget: { ...data.budget },
    prompt: split.body.trim(),
    origin,
  };
}
