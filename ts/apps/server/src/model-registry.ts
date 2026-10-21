import type { MutableModels } from "@earendil-works/pi-ai";
import type { Actor, ModelSlot, RegistryEntrySpec } from "@luminwrite/contracts";
import { type LwDatabase, type MasterKeyring, revealCredential } from "@luminwrite/db";
import {
  catalogModels,
  cheapestModel,
  conservativeRates,
  costMicros,
  createClosedModels,
  defaultRegistryRecords,
  defaultSlotBindings,
  isModelSlot,
  listModels,
  modelsForCredential,
  ownerSpendAllowed,
  probeResolved,
  type RegistryRecord,
  resolveModels,
  resolveTurnModels,
  syncByokProviders,
  validateRegistrySpec,
} from "@luminwrite/models";

import { utcDayStart } from "./api-access.js";

export interface RegistryDeps {
  db: LwDatabase;
  env?: NodeJS.ProcessEnv;
  keyring?: MasterKeyring;
  models?: MutableModels;
}

function runtimeEnv(env: NodeJS.ProcessEnv | undefined): NodeJS.ProcessEnv {
  return {
    ...(process.env.NODE_ENV ? { NODE_ENV: process.env.NODE_ENV } : {}),
    ...(process.env.LW_ALLOW_FAUX ? { LW_ALLOW_FAUX: process.env.LW_ALLOW_FAUX } : {}),
    ...env,
  };
}

function asSpec(spec: Record<string, unknown>): RegistryEntrySpec | undefined {
  if (spec.kind !== "model" && spec.kind !== "tier") return undefined;
  return spec as unknown as RegistryEntrySpec;
}

export async function ensureDefaultRegistry(
  db: LwDatabase,
  userId: string,
  env: NodeJS.ProcessEnv,
): Promise<void> {
  const existing = await db.listRegistryEntries(userId);
  const have = new Set(existing.map((entry) => entry.ref));
  for (const record of defaultRegistryRecords(env)) {
    if (have.has(record.ref)) continue;
    await db.upsertRegistryEntry({
      ownerId: userId,
      ref: record.ref,
      kind: record.kind,
      spec: { ...record.spec },
    });
  }
  const slots = await db.listSlotBindings(userId);
  const haveSlots = new Set(slots.map((slot) => slot.slot));
  for (const slot of defaultSlotBindings()) {
    if (haveSlots.has(slot.slot)) continue;
    await db.upsertSlotBinding(userId, slot.slot, slot.targetRef);
  }
}

async function loaded(deps: RegistryDeps, userId: string) {
  const env = runtimeEnv(deps.env);
  await ensureDefaultRegistry(deps.db, userId, env);
  const entries: RegistryRecord[] = [];
  for (const row of await deps.db.listRegistryEntries(userId)) {
    const spec = asSpec(row.spec);
    if (!spec || spec.kind !== row.kind) continue;
    entries.push({ ref: row.ref, kind: row.kind, spec });
  }
  const credentials = (await deps.db.listCredentials(userId)).map((row) => ({
    id: row.id,
    userId: row.userId,
    provider: row.provider,
    label: row.label,
    baseUrl: row.baseUrl,
    status: row.status,
    createdAt: row.createdAt.toISOString(),
  }));
  const slots = await deps.db.listSlotBindings(userId);
  return { env, entries, credentials, slots, catalog: catalogModels(env), userId };
}

export function modelsHandle(deps: RegistryDeps): MutableModels {
  if (!deps.models) deps.models = createClosedModels();
  return deps.models;
}

export async function listForUser(deps: RegistryDeps, userId: string) {
  const data = await loaded(deps, userId);
  return listModels(data);
}

export async function bindSlot(
  deps: RegistryDeps,
  userId: string,
  slot: string,
  targetRef: string,
): Promise<{ ok: true; slot: ModelSlot } | { ok: false; error: string }> {
  if (!isModelSlot(slot)) return { ok: false, error: "不认识这个槽位" };
  const data = await loaded(deps, userId);
  const known =
    data.entries.some((entry) => entry.ref === targetRef) || data.catalog.has(targetRef);
  if (!known) return { ok: false, error: "没有这个模型" };
  await deps.db.upsertSlotBinding(userId, slot, targetRef);
  return { ok: true, slot };
}

export async function saveRegistryEntry(
  deps: RegistryDeps,
  userId: string,
  ref: string,
  spec: RegistryEntrySpec,
): Promise<void> {
  const checked = validateRegistrySpec(spec);
  const stored =
    checked.kind === "model" && checked.cost && !checked.priceSource
      ? { ...checked, priceSource: "user" as const }
      : checked;
  await deps.db.upsertRegistryEntry({
    ownerId: userId,
    ref,
    kind: stored.kind,
    spec: { ...stored },
  });
}

export async function probeCredential(
  deps: RegistryDeps,
  input: { id: string; userId: string },
): Promise<{ ok: true } | { ok: false; status: 404 | 429 | 500; error: string }> {
  if (!deps.keyring) return { ok: false, status: 500, error: "还没有主密钥" };
  const row = await deps.db.getCredential(input.id);
  if (!row || row.userId !== input.userId) return { ok: false, status: 404, error: "找不到凭据" };
  const data = await loaded(deps, input.userId);
  const chain = modelsForCredential(input.id, data);
  const chosen = cheapestModel(chain);
  if (!chosen) {
    await deps.db.updateCredentialMeta(input.id, {
      status: "invalid",
      lastErrorCode: "no_model",
      lastVerifiedAt: new Date(),
    });
    return { ok: true };
  }
  const spent = await deps.db.usageCostMicrosForUser(input.userId, utcDayStart());
  if (!ownerSpendAllowed(spent, deps.env?.LW_DAILY_COST_CAP_USD)) {
    return { ok: false, status: 429, error: "已到今日费用上限" };
  }
  const models = modelsHandle(deps);
  const ring = deps.keyring;
  syncByokProviders({
    models,
    credentials: data.credentials,
    entries: data.entries,
    env: data.env,
    openSecret: async (id) => {
      const stored = await deps.db.getCredential(id);
      if (!stored || stored.userId !== input.userId) throw new Error("凭据解不开");
      return revealCredential(stored, ring);
    },
  });
  try {
    await probeResolved(models, chosen);
    await deps.db.updateCredentialMeta(input.id, {
      lastVerifiedAt: new Date(),
      lastErrorCode: null,
      ...(row.status === "invalid" ? { status: "active" } : {}),
    });
    const micros = chosen.price.known
      ? costMicros({ cost: chosen.rates }, { input: 1, output: 1 }, chosen.rates)
      : 0;
    await deps.db.recordUsage({
      requestId: `verify_${input.id}`,
      model: chosen.piModelId,
      inputTokens: 1,
      outputTokens: 1,
      costMicros: micros,
      userId: input.userId,
      credentialId: chosen.credentialId,
      modelRef: chosen.ref,
      ...(chosen.viaTier ? { tierRef: chosen.viaTier } : {}),
      priceKnown: chosen.price.known,
    });
    return { ok: true };
  } catch {
    await deps.db.updateCredentialMeta(input.id, {
      status: "invalid",
      lastErrorCode: "probe_failed",
      lastVerifiedAt: new Date(),
    });
    return { ok: true };
  }
}

export async function resolveForUser(
  deps: RegistryDeps,
  input: { userId: string; slot: ModelSlot; prefer?: string; sessionPin?: string },
) {
  const data = await loaded(deps, input.userId);
  return resolveModels({ ...data, ...input });
}

async function syncUserProviders(deps: RegistryDeps, userId: string): Promise<void> {
  if (!deps.keyring) return;
  const data = await loaded(deps, userId);
  const ring = deps.keyring;
  syncByokProviders({
    models: modelsHandle(deps),
    credentials: data.credentials,
    entries: data.entries,
    env: data.env,
    openSecret: async (id) => {
      const stored = await deps.db.getCredential(id);
      if (!stored || stored.userId !== userId) throw new Error("凭据解不开");
      return revealCredential(stored, ring);
    },
  });
}

/** 回合前解析。只返回这个用户的凭据，并先把 byok provider 注册到同一个 Models。 */
export async function resolveTurnChain(
  deps: RegistryDeps,
  input: {
    actor: Actor;
    slot: ModelSlot;
    prefer?: string;
    sessionPin: string | null;
    now: number;
    cooldownUntil: ReadonlyMap<string, number>;
  },
) {
  if (input.actor.type === "system") return { chain: [], conservative: conservativeRates([]) };
  const userId = input.actor.userId;
  await syncUserProviders(deps, userId);
  const data = await loaded(deps, userId);
  const resolveInput = {
    ...data,
    slot: input.slot,
    ...(input.prefer ? { prefer: input.prefer } : {}),
    ...(input.sessionPin ? { sessionPin: input.sessionPin } : {}),
    now: input.now,
    cooldownUntil: input.cooldownUntil,
  };
  const chain = resolveTurnModels(resolveInput);
  const open = resolveModels({ ...resolveInput, sessionPin: undefined });
  return { chain, conservative: conservativeRates(open.length > 0 ? open : chain) };
}

export async function turnSpendFor(deps: RegistryDeps, actor: Actor) {
  if (actor.type === "system") {
    return { userMicros: 0, keyMicros: 0, ownerCapUsd: deps.env?.LW_DAILY_COST_CAP_USD };
  }
  const since = utcDayStart();
  const userMicros = await deps.db.usageCostMicrosForUser(actor.userId, since);
  const apiKeyId = actor.type === "api_client" ? actor.keyId : undefined;
  const keyMicros = apiKeyId ? await deps.db.usageCostMicrosSince(apiKeyId, since) : 0;
  const key = apiKeyId ? await deps.db.getApiKey(apiKeyId) : undefined;
  return {
    userMicros,
    keyMicros,
    ...(deps.env?.LW_DAILY_COST_CAP_USD !== undefined
      ? { ownerCapUsd: deps.env.LW_DAILY_COST_CAP_USD }
      : {}),
    keyCapUsd: key?.dailyCostCapUsd ?? null,
  };
}
