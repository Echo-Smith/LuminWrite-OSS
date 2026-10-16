import type {
  ModelListItem,
  ModelSlot,
  RegistryEntrySpec,
  ResolvedModel,
  TokenRates,
} from "@luminwrite/contracts";

import type { CatalogModel } from "./catalog.js";
import { defaultSlotBindings, type RegistryRecord } from "./defaults.js";
import { choosePrice, type FullRates, rateSum } from "./price.js";

export interface CredentialRef {
  id: string;
  userId: string;
  provider: string;
  label: string;
  baseUrl: string | null;
  status: "active" | "disabled" | "invalid";
  createdAt?: string;
}

export interface PricedModel extends ResolvedModel {
  rates: FullRates;
}

export interface ResolveInput {
  userId: string;
  slot: ModelSlot;
  prefer?: string;
  sessionPin?: string;
  entries: RegistryRecord[];
  credentials: CredentialRef[];
  slots: Array<{ slot: string; targetRef: string }>;
  catalog: Map<string, CatalogModel>;
  now?: number;
  cooldownUntil?: ReadonlyMap<string, number>;
}

interface Concrete {
  ref: string;
  provider: string;
  modelId: string;
  name: string;
  api: string;
  baseUrl: string;
  contextWindow: number;
  maxTokens: number;
  input: Array<"text" | "image">;
  reasoning: boolean;
  price: ResolvedModel["price"];
  rates: FullRates;
}

function modelEntry(entries: RegistryRecord[], ref: string): RegistryRecord | undefined {
  return entries.find(
    (entry) => entry.ref === ref && entry.kind === "model" && entry.spec.kind === "model",
  );
}

function concreteOf(
  ref: string,
  entries: RegistryRecord[],
  catalog: Map<string, CatalogModel>,
): Concrete | undefined {
  const entry = modelEntry(entries, ref);
  const spec = entry?.spec.kind === "model" ? entry.spec : undefined;
  const catalogModel =
    catalog.get(ref) ?? (spec ? catalog.get(`${spec.provider}/${spec.modelId}`) : undefined);
  const provider = spec?.provider ?? catalogModel?.provider;
  const modelId = spec?.modelId ?? catalogModel?.modelId;
  if (!provider || !modelId) return undefined;
  const user = spec?.cost && spec.priceSource !== "registry" ? spec.cost : undefined;
  const registry = spec?.priceSource === "registry" ? spec.cost : undefined;
  const price = choosePrice({
    ...(user ? { user } : {}),
    ...(registry ? { registry } : {}),
    ...(catalogModel ? { catalog: catalogModel.cost } : {}),
  });
  return {
    ref: catalogModel?.ref ?? ref,
    provider,
    modelId,
    name: catalogModel?.name ?? modelId,
    api: spec?.api ?? catalogModel?.api ?? "openai-completions",
    baseUrl: spec?.baseUrl ?? catalogModel?.baseUrl ?? "",
    contextWindow: spec?.contextWindow ?? catalogModel?.contextWindow ?? 8192,
    maxTokens: spec?.maxTokens ?? catalogModel?.maxTokens ?? 1024,
    input: spec?.input ?? catalogModel?.input ?? ["text"],
    reasoning: spec?.reasoning ?? catalogModel?.reasoning ?? false,
    price: { known: price.known, source: price.source },
    rates: price.rates,
  };
}

function matchingCredentials(model: Concrete, input: ResolveInput): CredentialRef[] {
  const now = input.now ?? Date.now();
  return input.credentials
    .filter((credential) => credential.userId === input.userId)
    .filter((credential) => credential.provider === model.provider)
    .filter((credential) => credential.status === "active")
    .filter((credential) => (input.cooldownUntil?.get(credential.id) ?? 0) <= now)
    .sort(
      (a, b) => (a.createdAt ?? "").localeCompare(b.createdAt ?? "") || a.id.localeCompare(b.id),
    );
}

function toPriced(model: Concrete, credential: CredentialRef, viaTier?: string): PricedModel {
  return {
    ref: model.ref,
    ...(viaTier ? { viaTier } : {}),
    piProviderId: `byok_${credential.id}`,
    piModelId: model.modelId,
    credentialId: credential.id,
    price: model.price,
    rates: model.rates,
  };
}

function expand(ref: string, input: ResolveInput, viaTier?: string): PricedModel[] {
  const entry = input.entries.find((item) => item.ref === ref);
  if (
    (entry?.kind === "tier" && entry.spec.kind === "tier") ||
    (ref.startsWith("tier/") && !viaTier)
  ) {
    if (viaTier) return [];
    if (!entry || entry.spec.kind !== "tier") return [];
    if (entry.spec.evalGate && entry.spec.evalGate.passed === false) return [];
    const chain: PricedModel[] = [];
    for (const member of entry.spec.members) {
      if (member.startsWith("tier/")) continue;
      chain.push(...expand(member, input, ref));
    }
    return chain;
  }
  const model = concreteOf(ref, input.entries, input.catalog);
  if (!model) return [];
  return matchingCredentials(model, input).map((credential) =>
    toPriced(model, credential, viaTier),
  );
}

/**
 * 两层解析：所有者条目盖过目录，然后只留这个用户的凭据。
 * 会话钉住具体模型时不再展开档位。
 */
export function resolveModels(input: ResolveInput): PricedModel[] {
  if (input.sessionPin) {
    if (input.sessionPin.startsWith("tier/")) return [];
    return expand(input.sessionPin, input);
  }
  const bound = input.slots.find((slot) => slot.slot === input.slot)?.targetRef;
  const fallback = defaultSlotBindings().find((slot) => slot.slot === input.slot)?.targetRef;
  const target = input.prefer ?? bound ?? fallback;
  if (!target) return [];
  return expand(target, input);
}

/**
 * 回合用的链。钉住的具体模型还在当前档位里时只试它；
 * 换了档位、钉住的模型不在新链里时，改走新链。显式 prefer 覆盖钉。
 */
export function resolveTurnModels(input: ResolveInput): PricedModel[] {
  if (input.prefer) return resolveModels({ ...input, sessionPin: undefined });
  const open = resolveModels({ ...input, sessionPin: undefined });
  if (!input.sessionPin) return open;
  const stuck = open.filter((model) => model.ref === input.sessionPin);
  return stuck.length > 0 ? stuck : open;
}

function whyUnusable(ref: string, input: ResolveInput): string {
  const model = concreteOf(ref, input.entries, input.catalog);
  if (!model) return "目录里没有这个模型";
  const creds = input.credentials.filter(
    (credential) => credential.userId === input.userId && credential.provider === model.provider,
  );
  if (creds.length === 0) return "没有凭据";
  const now = input.now ?? Date.now();
  const active = creds.filter((credential) => credential.status === "active");
  if (active.length === 0) return "凭据不可用";
  if (active.every((credential) => (input.cooldownUntil?.get(credential.id) ?? 0) > now)) {
    return "冷却中";
  }
  return "没有凭据";
}

export function listModels(
  input: Omit<ResolveInput, "slot" | "prefer" | "sessionPin">,
): ModelListItem[] {
  const items: ModelListItem[] = [];
  const seen = new Set<string>();
  const resolveInput = { ...input, slot: "writer.main" as const };
  const consider = (ref: string, kind: "model" | "tier") => {
    if (seen.has(ref)) return;
    seen.add(ref);
    const chain = expand(ref, resolveInput);
    if (chain.length > 0) {
      items.push({ ref, kind, usable: true });
      return;
    }
    const entry = input.entries.find((item) => item.ref === ref);
    if (
      entry?.spec.kind === "tier" &&
      entry.spec.evalGate &&
      entry.spec.evalGate.passed === false
    ) {
      items.push({ ref, kind, usable: false, why: "评测未通过" });
      return;
    }
    if (kind === "tier") {
      const members = entry?.spec.kind === "tier" ? entry.spec.members : [];
      const why =
        members.length === 0
          ? "档位没有成员"
          : whyUnusable(members.find((member) => !member.startsWith("tier/")) ?? ref, resolveInput);
      items.push({ ref, kind, usable: false, why });
      return;
    }
    items.push({ ref, kind, usable: false, why: whyUnusable(ref, resolveInput) });
  };
  for (const entry of input.entries) consider(entry.ref, entry.kind);
  for (const entry of input.entries) {
    if (entry.spec.kind !== "tier") continue;
    for (const member of entry.spec.members) {
      if (!member.startsWith("tier/")) consider(member, "model");
    }
  }
  const providers = new Set(
    input.credentials
      .filter((credential) => credential.userId === input.userId)
      .map((credential) => credential.provider),
  );
  for (const model of input.catalog.values()) {
    if (providers.has(model.provider)) consider(model.ref, "model");
  }
  return items;
}

export function cheapestModel(chain: readonly PricedModel[]): PricedModel | undefined {
  return [...chain].sort((a, b) => {
    const left = a.price.known ? a.rates.input + a.rates.output : Number.POSITIVE_INFINITY;
    const right = b.price.known ? b.rates.input + b.rates.output : Number.POSITIVE_INFINITY;
    return left - right;
  })[0];
}

export function modelsForCredential(
  credentialId: string,
  input: Omit<ResolveInput, "slot" | "prefer" | "sessionPin">,
): PricedModel[] {
  const credential = input.credentials.find(
    (item) => item.id === credentialId && item.userId === input.userId,
  );
  if (!credential) return [];
  const refs = new Set<string>();
  for (const model of input.catalog.values()) {
    if (model.provider === credential.provider) refs.add(model.ref);
  }
  for (const entry of input.entries) {
    if (entry.spec.kind === "model" && entry.spec.provider === credential.provider)
      refs.add(entry.ref);
  }
  const chain: PricedModel[] = [];
  const resolveInput = { ...input, slot: "writer.main" as const };
  for (const ref of refs) {
    for (const model of expand(ref, resolveInput)) {
      if (model.credentialId === credentialId) chain.push(model);
    }
  }
  return chain;
}

export function conservativeRates(chain: readonly PricedModel[]): FullRates {
  const known = chain.filter((model) => model.price.known);
  const first = known[0];
  if (!first) return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
  return known.reduce(
    (max, model) => (rateSum(model.rates) > rateSum(max) ? model.rates : max),
    first.rates,
  );
}

export function validateRegistrySpec(spec: RegistryEntrySpec): RegistryEntrySpec {
  if (spec.kind === "tier") {
    if (!spec.label.trim()) throw new Error("档位需要名称");
    if (spec.members.some((member) => member.startsWith("tier/"))) {
      throw new Error("档位不能再套档位");
    }
    return spec;
  }
  if (!spec.provider.trim() || !spec.modelId.trim()) throw new Error("模型条目缺少供应商或模型 id");
  if (
    spec.fineTune &&
    spec.fineTune.dataConsent !== "explicit" &&
    spec.fineTune.dataConsent !== "none"
  ) {
    throw new Error("微调必须写明数据同意");
  }
  return spec;
}

export type { TokenRates };
