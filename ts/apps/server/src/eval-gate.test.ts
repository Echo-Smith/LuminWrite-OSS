import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

import { runEvalCli } from "./eval-gate.js";

describe("eval 闸门", () => {
  it("faux 跑完写改回滚、嵌入和华丽笔记地板", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-eval-gate-"));
    const out = join(dir, "report.md");
    const decoy = "eval-decoy-token-should-stay-out";
    let stdout = "";
    let stderr = "";
    const code = await runEvalCli(
      ["gate", "--dir", dir, "--out", out],
      { OPENAI_API_KEY: decoy, LW_LLM_API_KEY: decoy },
      {
        write(chunk) {
          stdout += chunk;
        },
      },
      {
        write(chunk) {
          stderr += chunk;
        },
      },
    );
    const report = await readFile(out, "utf8").catch(() => "");
    if (code !== 0) throw new Error(`${stderr}\n${report}`);
    expect(code).toBe(0);
    expect(report).toContain("H2");
    expect(report).toContain("H3");
    expect(report).toContain("**通过**");
    expect(report).not.toContain(decoy);
    expect(stdout).not.toContain(decoy);
    expect(stderr).not.toContain(decoy);
    await rm(dir, { recursive: true, force: true });
  }, 60_000);

  it("真实模型开关没配齐时不读密钥", async () => {
    const decoy = "eval-decoy-token-should-stay-out";
    let stderr = "";
    const code = await runEvalCli(
      ["gate"],
      { LW_EVAL_LIVE: "1", LW_LLM_API_KEY: decoy },
      { write() {} },
      {
        write(chunk) {
          stderr += chunk;
        },
      },
    );
    expect(code).toBe(1);
    expect(stderr).toContain("LW_LLM_ENABLE");
    expect(stderr).not.toContain(decoy);
  });
});
