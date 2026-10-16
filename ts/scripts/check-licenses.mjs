import { execFile } from "node:child_process";
import { promisify } from "node:util";

const exec = promisify(execFile);
const { stdout } = await exec("pnpm", ["licenses", "list", "--json"], {
  cwd: new URL("..", import.meta.url).pathname,
});
const parsed = JSON.parse(stdout);

function forbidden(license) {
  const text = String(license ?? "");
  return text.split(/\s+(?:OR|AND)\s+/i).some((part) => {
    if (/LGPL/i.test(part)) return false;
    return /AGPL|SSPL|UNLICENSED|Elastic|GPL/i.test(part);
  });
}

const failures = [];
const groups = Array.isArray(parsed) ? { unknown: parsed } : parsed;
for (const [license, packages] of Object.entries(groups)) {
  const list = Array.isArray(packages) ? packages : [packages];
  for (const item of list) {
    const name = item?.name ?? "";
    const declared = item?.license ?? license;
    if (name.startsWith("@lwc/") || name.startsWith("@tiptap-pro/")) {
      failures.push(`${name} 不在 OSS 边界内`);
    }
    if (forbidden(declared) || forbidden(license)) {
      failures.push(`${name} 许可证 ${declared}`);
    }
  }
}
if (failures.length > 0) {
  process.stderr.write(`${failures.join("\n")}\n`);
  process.exit(1);
}
process.stdout.write("许可证检查通过\n");
