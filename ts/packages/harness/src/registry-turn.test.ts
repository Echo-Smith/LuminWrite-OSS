import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fauxAssistantMessage } from "@earendil-works/pi-ai";
import type { UsageEvent } from "@luminwrite/edition";
import {
  catalogModels,
  createClosedModels,
  fauxFor,
  resolveTurnModels,
  syncByokProviders,
  TurnSpendError,
} from "@luminwrite/models";
import { afterEach, describe, expect, it } from "vitest";

import { MemoryWritingStore } from "./memory-store.js";
import { createWritingRuntime } from "./runtime.js";

const user = { type: "user" as const, userId: "user_1" };
const env = { NODE_ENV: "test" };
const dirs: string[] = [];

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

async function tempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "lw-turn-registry-"));
  dirs.push(dir);
  return dir;
}

function outline(documentId: string, key: string) {
  return {
    documentId,
    kind: "outline" as const,
    instruction: "先写大纲",
    scope: "document" as const,
    delivery: "followup" as const,
    applyMode: "auto" as const,
    idempotencyKey: key,
    actor: user,
  };
}

describe("回合走注册表", () => {
  it("未换档沿用钉住的模型，换档后用新模型", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "档位", ownerId: "user_1" });
    const models = createClosedModels();
    const entries = [
      {
        ref: "custom/other",
        kind: "model" as const,
        spec: { kind: "model" as const, provider: "test", modelId: "faux-1" },
      },
      {
        ref: "tier/custom",
        kind: "tier" as const,
        spec: { kind: "tier" as const, label: "自定义", members: ["custom/other", "test/faux"] },
      },
    ];
    const credentials = [
      {
        id: "cred_test",
        userId: "user_1",
        provider: "test",
        label: "测试",
        baseUrl: null,
        status: "active" as const,
        createdAt: "1",
      },
    ];
    let targetRef = "tier/custom";
    const seen: string[] = [];
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      models,
      env,
      async resolveTurn(input) {
        syncByokProviders({
          models,
          credentials,
          entries,
          env,
          openSecret: async () => ({ reveal: () => "lwtest_turn_registry" }),
        });
        const chain = resolveTurnModels({
          userId: "user_1",
          slot: input.slot,
          entries,
          credentials,
          slots: [{ slot: input.slot, targetRef }],
          catalog: catalogModels(env),
          now: input.now,
          cooldownUntil: input.cooldownUntil,
          ...(input.sessionPin ? { sessionPin: input.sessionPin } : {}),
          ...(input.prefer ? { prefer: input.prefer } : {}),
        });
        seen.push(chain.map((model) => model.ref).join(","));
        return {
          chain,
          conservative: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        };
      },
    });
    const first = await runtime.submitTurn(outline(created.id, "pin-1"));
    expect(first.status).toBe("applied");
    expect((await store.getTurn(first.turnId))?.modelRef).toBe("custom/other");
    expect((await store.getSession(created.sessionId))?.modelPin).toBe("custom/other");

    const second = await runtime.submitTurn(outline(created.id, "pin-2"));
    expect(second.status).toBe("applied");
    expect((await store.getTurn(second.turnId))?.modelRef).toBe("custom/other");
    expect(seen[1]).toBe("custom/other");

    targetRef = "test/faux";
    const third = await runtime.submitTurn(outline(created.id, "pin-3"));
    expect(third.status).toBe("applied");
    expect((await store.getTurn(third.turnId))?.modelRef).toBe("test/faux");
    expect((await store.getSession(created.sessionId))?.modelPin).toBe("test/faux");
    await runtime.close();
  });

  it("第一个 token 之前换凭据，用量记下调用者和密钥", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "回退", ownerId: "user_1" });
    const models = createClosedModels();
    fauxFor(models).appendResponses([
      fauxAssistantMessage([], { stopReason: "error", errorMessage: "429 rate limit" }),
    ]);
    const usage: UsageEvent[] = [];
    const credentials = ["cred_a", "cred_b"].map((id, index) => ({
      id,
      userId: "user_1",
      provider: "test",
      label: id,
      baseUrl: null,
      status: "active" as const,
      createdAt: String(index),
    }));
    const entries = [
      {
        ref: "tier/only",
        kind: "tier" as const,
        spec: { kind: "tier" as const, label: "只测", members: ["test/faux"] },
      },
    ];
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      models,
      env,
      usage: {
        record: async (event) => {
          usage.push(event);
        },
      },
      async resolveTurn(input) {
        syncByokProviders({
          models,
          credentials,
          entries,
          env,
          openSecret: async () => ({ reveal: () => "lwtest_turn_fallback" }),
        });
        const chain = resolveTurnModels({
          userId: "user_1",
          slot: input.slot,
          entries,
          credentials,
          slots: [{ slot: "writer.main", targetRef: "tier/only" }],
          catalog: catalogModels(env),
          now: input.now,
          cooldownUntil: input.cooldownUntil,
          ...(input.sessionPin ? { sessionPin: input.sessionPin } : {}),
        });
        return { chain, conservative: { input: 3, output: 3, cacheRead: 0, cacheWrite: 0 } };
      },
    });
    const actor = { type: "api_client" as const, userId: "user_1", keyId: "key_1" };
    const result = await runtime.submitTurn({ ...outline(created.id, "fall-1"), actor });
    expect(result.status).toBe("applied");
    const events = await runtime.events(created.id);
    expect(events.some((event) => event.type === "model.fallback")).toBe(true);
    expect(usage[0]).toMatchObject({
      userId: "user_1",
      apiKeyId: "key_1",
      credentialId: "cred_b",
      modelRef: "test/faux",
      tierRef: "tier/only",
      priceKnown: false,
    });
    expect(JSON.stringify(usage)).not.toContain("lwtest_turn_fallback");
    await runtime.close();
  });

  it("所有者上限和密钥上限在回合开始前拦住", async () => {
    const dir = await tempDir();
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "上限", ownerId: "user_1" });
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      env,
      turnSpend: async (actor) => ({
        userMicros: actor.type === "user" ? 2_000_000 : 0,
        keyMicros: actor.type === "api_client" ? 1 : 0,
        ownerCapUsd: actor.type === "user" ? "1" : undefined,
        keyCapUsd: actor.type === "api_client" ? 0 : null,
      }),
    });
    await expect(runtime.submitTurn(outline(created.id, "cap-owner"))).rejects.toBeInstanceOf(
      TurnSpendError,
    );
    await expect(
      runtime.submitTurn({
        ...outline(created.id, "cap-key"),
        actor: { type: "api_client", userId: "user_1", keyId: "key_over" },
      }),
    ).rejects.toBeInstanceOf(TurnSpendError);
    expect(store.head(created.id)).toBeTruthy();
    await runtime.close();
  });
});
