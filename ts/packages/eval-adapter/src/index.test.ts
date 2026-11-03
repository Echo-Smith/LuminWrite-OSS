import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { analyzePlain } from "@luminwrite/plainspeak";
import { fauxResearchPlan } from "@luminwrite/research";
import { describe, expect, it } from "vitest";
import {
  type EvalCapture,
  judgeCapture,
  readResearchPlansForEval,
  runDeterministicSmoke,
  runWriteReviseRollback,
  smokeSamples,
  stripHtml,
} from "./index.js";

describe("评测冒烟", () => {
  it("中性负对照在说人话轴上排最后", () => {
    const report = runDeterministicSmoke();
    expect(report.pass).toBe(true);
    expect(report.ranking.at(-1)?.id).toBe("negative");
    const negative = smokeSamples.find((sample) => sample.role === "negative");
    expect(analyzePlain(negative?.text ?? "").pass).toBe(false);
  });

  it("能读回 research_plans 里的计划", () => {
    const plan = fauxResearchPlan("查显卡时租");
    const stored = readResearchPlansForEval([
      {
        id: "rpl_1",
        turnId: "trn_1",
        sessionId: "ses_1",
        plan,
        gaps: ["q1: 尚未有通过校验的证据"],
        createdAt: "2026-10-20T00:00:00.000Z",
      },
    ]);
    expect(stored[0]?.plan.questions[0]?.id).toBe("q1");
    expect(stored[0]?.gaps).toEqual(["q1: 尚未有通过校验的证据"]);
  });

  it("写改回滚只调用任务 API 和回滚", async () => {
    const calls: string[] = [];
    const result = await runWriteReviseRollback({
      async submitTask(body, key) {
        calls.push(`submit ${body.kind} ${key}`);
        if (body.kind === "write") {
          return { id: "tsk_w", state: "completed", documentId: "doc_1", resultVersionId: "ver_2" };
        }
        return { id: "tsk_r", state: "completed", documentId: "doc_1", resultVersionId: "ver_3" };
      },
      async getTask(id) {
        calls.push(`get ${id}`);
        return id === "tsk_w"
          ? { id, state: "completed", documentId: "doc_1", resultVersionId: "ver_2" }
          : { id, state: "completed", documentId: "doc_1", resultVersionId: "ver_3" };
      },
      async restoreDocument(id, versionId) {
        calls.push(`restore ${id} ${versionId}`);
        return { ok: true, versionId: "ver_4" };
      },
    });
    expect(result).toEqual({
      documentId: "doc_1",
      wroteVersionId: "ver_2",
      revisedVersionId: "ver_3",
    });
    expect(calls).toEqual([
      "submit write eval-write",
      "get tsk_w",
      "submit revise eval-revise",
      "get tsk_r",
      "restore doc_1 ver_2",
    ]);
  });

  it("失败任务带上存下来的供应商错误", async () => {
    await expect(
      runWriteReviseRollback({
        async submitTask() {
          return { id: "tsk_w", state: "submitted" };
        },
        async getTask() {
          return {
            id: "tsk_w",
            state: "failed",
            error: { message: 'model_error 400：Unknown name "store"' },
          };
        },
        async restoreDocument() {
          return { ok: false };
        },
      }),
    ).rejects.toThrow(/任务结束于 failed：model_error 400/);
  });
});

describe("留出集", () => {
  it("去掉网页标签", () => {
    expect(stripHtml("<p>赋能</p><script>secret</script>")).toBe("赋能");
  });

  it("读 txt 和 html，跳过 SOURCES.md", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-heldout-"));
    await writeFile(join(dir, "gov2024_full.txt"), "显卡时租按小时计费。", "utf8");
    await writeFile(
      join(dir, "raw_gov2024.html"),
      "<html><body><p>赋能闭环。</p></body></html>",
      "utf8",
    );
    await writeFile(join(dir, "SOURCES.md"), "只是清单", "utf8");
    const judgment = await judgeCapture(emptyCapture(), { heldoutDir: dir });
    expect(judgment.heldout.files.map((file) => file.name).sort()).toEqual([
      "gov2024_full.txt",
      "raw_gov2024.html",
    ]);
    const html = judgment.heldout.files.find((file) => file.name.endsWith(".html"));
    expect(html?.aitoneScore).toBeGreaterThan(0);
    await rm(dir, { recursive: true, force: true });
  });
});

function emptyCapture(): EvalCapture {
  return {
    generatedAt: "2026-11-03T00:00:00.000Z",
    model: "test/faux",
    live: false,
    exchanges: [],
    scenarios: {
      rollback: {
        ok: false,
        error: "没有正文",
        restoredMatchesWrite: false,
        beforeText: "",
        afterText: "",
      },
      research: { ok: false, questionCount: 0, gaps: [], claimFlags: false, admittedGap: false },
      embeddings: {
        ok: false,
        transport: "openai-compatible",
        endpoint: "local-stub",
        model: "fake",
        dim: 1024,
        requests: 0,
        stored: false,
        searchHit: false,
      },
      floor: {
        ok: false,
        note: "写得华丽一点",
        plainConflict: false,
        injected: false,
        turnStatus: "",
        turnError: null,
        documentUnchanged: false,
        hollowLanded: false,
        probe: "faux-hollow",
      },
    },
  };
}
