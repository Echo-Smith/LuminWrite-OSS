import { readdir, readFile } from "node:fs/promises";
import { join } from "node:path";

const root = new URL("..", import.meta.url);
const skip = new Set(["node_modules", "dist", "coverage"]);
const skipFiles = new Set(["biome.json"]);

function forbidden(text, fileName) {
  if (fileName === "package.json") return /"@(?:lwc|tiptap-pro)\//.test(text);
  return /(?:from|import|require)\s*\(?\s*["']@(?:lwc|tiptap-pro)\//.test(text);
}

async function walk(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const hits = [];
  for (const entry of entries) {
    if (skip.has(entry.name) || skipFiles.has(entry.name)) continue;
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      hits.push(...(await walk(path)));
      continue;
    }
    if (!/\.(ts|tsx|js|mjs|json)$/.test(entry.name)) continue;
    const text = await readFile(path, "utf8");
    if (forbidden(text, entry.name)) hits.push(path);
  }
  return hits;
}

const hits = await walk(root.pathname);
if (hits.length > 0) {
  process.stderr.write(`OSS 边界被打破：\n${hits.join("\n")}\n`);
  process.exit(1);
}
process.stdout.write("边界检查通过：没有 @lwc/ 或 @tiptap-pro/\n");
