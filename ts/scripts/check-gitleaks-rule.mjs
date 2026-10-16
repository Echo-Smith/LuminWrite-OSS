import { execFile } from "node:child_process";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const config = new URL("../../.gitleaks.toml", import.meta.url).pathname;
const dir = await mkdtemp(join(tmpdir(), "lw-gitleaks-"));
const sample = `sk-${"a".repeat(24)}`;
try {
  await writeFile(join(dir, "leak.txt"), `token=${sample}\n`);
  try {
    await exec("gitleaks", ["detect", "--no-git", "--redact", "--config", config, "--source", dir]);
    process.stderr.write("自定义规则没有拦住裸 sk- 密钥\n");
    process.exit(1);
  } catch (error) {
    const code = error && typeof error === "object" && "code" in error ? error.code : undefined;
    if (code !== 1) throw error;
  }
  process.stdout.write("自定义 sk- 规则能命中\n");
} finally {
  await rm(dir, { recursive: true, force: true });
}
