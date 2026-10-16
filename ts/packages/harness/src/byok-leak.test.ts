import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT, type Context } from "@earendil-works/chord/context";
import { fauxAssistantMessage } from "@earendil-works/pi-ai";
import { createRegistry, Harness } from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";
import { byokFromBase, createClosedModels, fauxFor } from "@luminwrite/models";
import { describe, expect, it } from "vitest";

const ctx: Context = BACKGROUND_CONTEXT;

describe("凭据不进 Pi 存储", () => {
  it("跑完一个回合后，SQLite、事件和用量里都没有 key", async () => {
    const key = "lwtest_leak_probe_key";
    const dir = await mkdtemp(join(tmpdir(), "lw-byok-leak-"));
    const sqlitePath = join(dir, "pi.sqlite");
    const logs: string[] = [];
    const original = console.error;
    console.error = (...args: unknown[]) => {
      logs.push(args.map((item) => String(item)).join(" "));
    };
    const storage = await openNodeSqliteStorage(sqlitePath);
    const models = createClosedModels();
    const faux = fauxFor(models);
    models.setProvider(
      byokFromBase(
        faux.provider,
        { id: "cred_leak", label: "泄漏检查", baseUrl: null },
        async () => ({
          reveal: () => key,
        }),
      ),
    );
    faux.appendResponses([fauxAssistantMessage("完成")]);
    const harness = await Harness.open(storage, { models, registry: createRegistry() }, ctx);
    try {
      harness.resume();
      const conversation = await harness.createConversation(
        {
          ownership: { kind: "ownerless" },
          agent: { model: { provider: "byok_cred_leak", modelId: "faux-1" } },
        },
        ctx,
      );
      const submission = await conversation.submit(
        { type: "input", content: "你好", requestId: "leak-1", whenBusy: "followUp" },
        ctx,
      );
      await submission.wait(ctx);
    } finally {
      console.error = original;
      await harness.close(ctx);
    }
    const bytes = await readFile(sqlitePath);
    const ledger = JSON.stringify({
      model: "faux-1",
      credentialId: "cred_leak",
      costMicros: 0,
      priceKnown: false,
    });
    const event = JSON.stringify({ type: "model.fallback", from: "test/faux", to: "test/faux" });
    const haystack = Buffer.concat([
      bytes,
      Buffer.from(logs.join("\n")),
      Buffer.from(ledger),
      Buffer.from(event),
    ]);
    expect(haystack.includes(Buffer.from(key))).toBe(false);
    await rm(dir, { recursive: true, force: true });
  });
});
