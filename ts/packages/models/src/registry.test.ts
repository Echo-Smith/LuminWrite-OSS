import { describe, expect, it } from "vitest";

import { catalogModels } from "./catalog.js";
import { defaultRegistryRecords } from "./defaults.js";
import { runWithFallback, UpstreamModelError } from "./fallback.js";
import { chargeMicros, choosePrice, costMicros, ownerSpendAllowed } from "./price.js";
import { listModels, resolveModels, resolveTurnModels } from "./registry.js";

const env = { NODE_ENV: "test" };

function input(
  credentials: Array<{ id: string; provider: string; status?: "active" | "disabled" | "invalid" }>,
) {
  return {
    userId: "usr_owner",
    entries: defaultRegistryRecords(env),
    credentials: credentials.map((credential) => ({
      id: credential.id,
      userId: "usr_owner",
      provider: credential.provider,
      label: credential.id,
      baseUrl: null,
      status: credential.status ?? "active",
      createdAt: credential.id,
    })),
    slots: [],
    catalog: catalogModels(env),
    now: 1_000,
  };
}

describe("模型注册表", () => {
  it("环境里有供应商 key 但没有凭据时，解析结果不可用", () => {
    const listed = listModels(input([]));
    const mini = listed.find((item) => item.ref === "openai/gpt-4.1-mini");
    expect(mini).toMatchObject({ usable: false, why: "没有凭据" });
    expect(resolveModels({ ...input([]), slot: "writer.main" })).toEqual([]);
    expect(JSON.stringify(listed)).not.toContain("OPENAI_API_KEY");
  });

  it("有凭据才解析出 byok provider，会话钉住后不再回到档位前面的模型", () => {
    const chain = resolveModels({
      ...input([{ id: "cred_oa", provider: "openai" }]),
      slot: "writer.main",
    });
    expect(chain[0]).toMatchObject({
      ref: "openai/gpt-4.1-mini",
      piProviderId: "byok_cred_oa",
      credentialId: "cred_oa",
      viaTier: "tier/balanced",
    });
    expect(chain.some((model) => model.ref === "deepseek/deepseek-v4-pro")).toBe(false);
    const pinned = resolveModels({
      ...input([
        { id: "cred_oa", provider: "openai" },
        { id: "cred_ds", provider: "deepseek" },
      ]),
      slot: "writer.main",
      sessionPin: "deepseek/deepseek-v4-pro",
    });
    expect(pinned.map((model) => model.ref)).toEqual(["deepseek/deepseek-v4-pro"]);
  });

  it("换档后不再沿用钉，未换档时只走钉住的模型", () => {
    const creds = input([
      { id: "cred_oa", provider: "openai" },
      { id: "cred_ds", provider: "deepseek" },
    ]);
    const stuck = resolveTurnModels({
      ...creds,
      slot: "writer.main",
      sessionPin: "deepseek/deepseek-v4-pro",
    });
    expect(stuck.map((model) => model.ref)).toEqual(["deepseek/deepseek-v4-pro"]);
    const changed = resolveTurnModels({
      ...creds,
      slot: "writer.main",
      prefer: "tier/fast",
      sessionPin: "deepseek/deepseek-v4-pro",
    });
    expect(changed[0]?.ref).toBe("openai/gpt-4.1-nano");
    expect(changed.some((model) => model.ref === "deepseek/deepseek-v4-pro")).toBe(false);
    const unknown = chargeMicros(
      false,
      { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      { input: 2, output: 8, cacheRead: 0, cacheWrite: 0 },
      { input: 1_000_000, output: 0 },
    );
    expect(unknown).toBe(2_000_000);
  });

  it("价格是用户价优先于注册表，再优先于目录", () => {
    expect(
      choosePrice({
        user: { input: 9, output: 9 },
        registry: { input: 4, output: 4 },
        catalog: { input: 1, output: 1 },
      }),
    ).toMatchObject({ source: "user", known: true, rates: { input: 9 } });
    expect(
      choosePrice({ registry: { input: 4, output: 4 }, catalog: { input: 1, output: 1 } }).source,
    ).toBe("registry");
    expect(choosePrice({ catalog: { input: 0.4, output: 1.6 } })).toMatchObject({
      source: "catalog",
      known: true,
    });
    expect(choosePrice({ catalog: { input: 0, output: 0 } })).toMatchObject({
      source: "none",
      known: false,
    });
    const micros = costMicros(
      { cost: { input: 0.1, output: 0.4, cacheRead: 0, cacheWrite: 0 } },
      { input: 1_000_000, output: 0 },
      { input: 3, output: 9 },
    );
    expect(micros).toBe(3_000_000);
    expect(ownerSpendAllowed(2_000_000, "1")).toBe(false);
    expect(ownerSpendAllowed(0, undefined)).toBe(true);
  });

  it("第一个 token 之前才回退，429 会冷却凭据", async () => {
    const chain = resolveModels({
      ...input([
        { id: "cred_a", provider: "openai" },
        { id: "cred_b", provider: "deepseek" },
      ]),
      slot: "writer.main",
      prefer: "tier/balanced",
    });
    expect(chain.map((model) => model.credentialId)).toEqual(["cred_a", "cred_b"]);
    const cooldown = new Map<string, number>();
    let second = 0;
    const failed = await runWithFallback({
      chain,
      now: 1_000,
      cooldownUntil: cooldown,
      run: async (model, markToken) => {
        if (model.credentialId === "cred_a") {
          markToken();
          throw new UpstreamModelError("中断", { status: 429, retryable: true });
        }
        second += 1;
        return model.ref;
      },
    }).then(
      () => "继续了",
      (error: unknown) => (error instanceof Error ? error.message : ""),
    );
    expect(failed).toBe("中断");
    expect(second).toBe(0);

    const events: string[] = [];
    const ok = await runWithFallback({
      chain,
      now: 5_000,
      cooldownUntil: cooldown,
      run: async (model) => {
        if (model.credentialId === "cred_a") {
          throw new UpstreamModelError("限流", { status: 429, retryable: true });
        }
        return model.ref;
      },
    });
    events.push(...ok.events.map((event) => event.type));
    expect(ok.used.ref).toBe("deepseek/deepseek-v4-pro");
    expect(events).toEqual(["model.fallback"]);
    expect(cooldown.get("cred_a")).toBe(5_000 + 60_000);
    const cooled = resolveModels({
      ...input([
        { id: "cred_a", provider: "openai" },
        { id: "cred_b", provider: "deepseek" },
      ]),
      slot: "writer.main",
      now: 6_000,
      cooldownUntil: cooldown,
    });
    expect(cooled.some((model) => model.credentialId === "cred_a")).toBe(false);
  });
});
