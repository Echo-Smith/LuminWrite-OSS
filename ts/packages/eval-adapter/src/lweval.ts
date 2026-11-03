import { spawn } from "node:child_process";
import { access } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/** 从当前文件往上找仓库里的 `eval/lw-eval`。v0.3 已经在 main，不再复制一份。 */
export async function lwEvalRoot(): Promise<string> {
  let dir = dirname(fileURLToPath(import.meta.url));
  for (let hop = 0; hop < 8; hop += 1) {
    const candidate = join(dir, "eval", "lw-eval");
    try {
      await access(join(candidate, "lweval", "cli.py"));
      return candidate;
    } catch {
      const parent = dirname(dir);
      if (parent === dir) break;
      dir = parent;
    }
  }
  throw new Error("找不到 eval/lw-eval");
}

export async function runLwEval(args: readonly string[]): Promise<unknown> {
  const root = await lwEvalRoot();
  const result = await spawnText("python3", ["-m", "lweval", ...args], root);
  if (result.code !== 0) {
    throw new Error(result.stderr.trim() || `lweval 退出 ${result.code}`);
  }
  return JSON.parse(result.stdout) as unknown;
}

/** 只跑仓库里的指标函数。脚本是固定的，参数不拼进源码。 */
export async function runLwEvalScript(script: string, args: readonly string[]): Promise<unknown> {
  const root = await lwEvalRoot();
  const result = await spawnText("python3", ["-c", script, root, ...args], root);
  if (result.code !== 0) {
    throw new Error(result.stderr.trim() || `lweval 脚本退出 ${result.code}`);
  }
  return JSON.parse(result.stdout) as unknown;
}

function spawnText(
  command: string,
  args: readonly string[],
  cwd: string,
): Promise<{ stdout: string; stderr: string; code: number }> {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd,
      env: { ...process.env, PYTHONDONTWRITEBYTECODE: "1" },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout: Buffer[] = [];
    const stderr: Buffer[] = [];
    child.stdout.on("data", (chunk: Buffer) => stdout.push(chunk));
    child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
    child.on("error", reject);
    child.on("close", (code) => {
      resolve({
        stdout: Buffer.concat(stdout).toString("utf8"),
        stderr: Buffer.concat(stderr).toString("utf8"),
        code: code ?? 1,
      });
    });
  });
}
