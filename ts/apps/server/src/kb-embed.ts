import type { RegistryEntrySpec } from "@luminwrite/contracts";
import { type LwDatabase, type MasterKeyring, revealCredential } from "@luminwrite/db";
import { createOpenAiEmbedder, type EmbeddingPort } from "@luminwrite/kb";
import { catalogModels, type RegistryRecord, resolveModels } from "@luminwrite/models";

/**
 * pi-ai 没有嵌入接口。`kb.embed` 槽解析到模型条目和一把有效 BYOK 凭据时，
 * 才走 OpenAI 兼容的 `/embeddings`。槽或凭据缺了就返回 null，检索退回全文。
 */
export function kbEmbedderFor(deps: {
  db: LwDatabase;
  keyring?: MasterKeyring;
  env?: NodeJS.ProcessEnv;
  fetchImpl?: typeof fetch;
}): (ownerId: string) => Promise<EmbeddingPort | null> {
  return async (ownerId) => {
    const ring = deps.keyring;
    if (!ring) return null;
    const entries: RegistryRecord[] = [];
    for (const row of await deps.db.listRegistryEntries(ownerId)) {
      const spec = asSpec(row.spec);
      if (!spec || spec.kind !== row.kind) continue;
      entries.push({ ref: row.ref, kind: row.kind, spec });
    }
    const credentials = (await deps.db.listCredentials(ownerId)).map((row) => ({
      id: row.id,
      userId: row.userId,
      provider: row.provider,
      label: row.label,
      baseUrl: row.baseUrl,
      status: row.status,
      createdAt: row.createdAt.toISOString(),
    }));
    const slots = await deps.db.listSlotBindings(ownerId);
    const chosen = resolveModels({
      userId: ownerId,
      slot: "kb.embed",
      entries,
      credentials,
      slots,
      catalog: catalogModels(deps.env ?? {}),
    })[0];
    if (!chosen) return null;
    const row = await deps.db.getCredential(chosen.credentialId);
    if (!row || row.userId !== ownerId || row.status !== "active") return null;
    const entry = entries.find((item) => item.ref === chosen.ref);
    const specBase = entry?.spec.kind === "model" ? entry.spec.baseUrl : undefined;
    const baseUrl = row.baseUrl || specBase;
    if (!baseUrl) return null;
    return createOpenAiEmbedder({
      model: chosen.piModelId,
      apiKey: revealCredential(row, ring).reveal(),
      baseUrl,
      ...(deps.fetchImpl ? { fetchImpl: deps.fetchImpl } : {}),
    });
  };
}

function asSpec(spec: Record<string, unknown>): RegistryEntrySpec | undefined {
  if (spec.kind !== "model" && spec.kind !== "tier") return undefined;
  return spec as unknown as RegistryEntrySpec;
}
