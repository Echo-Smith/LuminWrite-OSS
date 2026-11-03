import {
  type Api,
  type AuthContext,
  createModels,
  createProvider,
  type FauxProviderHandle,
  fauxAssistantMessage,
  fauxProvider,
  type Model,
  type MutableModels,
  type Provider,
} from "@earendil-works/pi-ai";
import { anthropicMessagesApi } from "@earendil-works/pi-ai/api/anthropic-messages.lazy";
import { googleGenerativeAIApi } from "@earendil-works/pi-ai/api/google-generative-ai.lazy";
import { openAICompletionsApi } from "@earendil-works/pi-ai/api/openai-completions.lazy";
import { builtinProviders } from "@earendil-works/pi-ai/providers/all";

import type { CatalogModel } from "./catalog.js";
import { fauxAllowed } from "./catalog.js";
import type { RegistryRecord } from "./defaults.js";
import type { CredentialRef, PricedModel } from "./registry.js";

export interface Revealable {
  reveal(): string;
}

/** 给 pi-ai 的环境读取一律空，避免用到机器上别的项目的 key。 */
export function closedAuthContext(): AuthContext {
  return {
    env() {
      return Promise.resolve(undefined);
    },
    fileExists() {
      return Promise.resolve(false);
    },
  };
}

export function createClosedModels(): MutableModels {
  return createModels({ authContext: closedAuthContext() });
}

const fauxes = new WeakMap<MutableModels, FauxProviderHandle>();

export function fauxFor(models: MutableModels): FauxProviderHandle {
  let handle = fauxes.get(models);
  if (!handle) {
    handle = fauxProvider({
      provider: "test",
      models: [
        {
          id: "faux-1",
          name: "测试",
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
          contextWindow: 8192,
          maxTokens: 256,
        },
      ],
    });
    fauxes.set(models, handle);
  }
  return handle;
}

let builtins: Map<string, Provider> | undefined;

function builtinById(id: string): Provider | undefined {
  if (!builtins) builtins = new Map(builtinProviders().map((provider) => [provider.id, provider]));
  return builtins.get(id);
}

export function byokFromBase(
  base: Provider,
  credential: Pick<CredentialRef, "id" | "label" | "baseUrl">,
  openSecret: () => Promise<Revealable>,
): Provider {
  const id = `byok_${credential.id}`;
  const baseUrl = credential.baseUrl ?? base.baseUrl;
  return {
    id,
    name: `${base.name}（${credential.label}）`,
    ...(baseUrl ? { baseUrl } : {}),
    auth: {
      apiKey: {
        name: credential.label,
        resolve: async () => {
          const apiKey = (await openSecret()).reveal();
          return {
            auth: { apiKey, ...(credential.baseUrl ? { baseUrl: credential.baseUrl } : {}) },
            source: "vault",
          };
        },
      },
    },
    getModels() {
      return base.getModels().map((model) => ({
        ...model,
        provider: id,
        ...(credential.baseUrl ? { baseUrl: credential.baseUrl } : {}),
      }));
    },
    stream(model, context, options) {
      return base.stream({ ...model, provider: base.id }, context, options);
    },
    streamSimple(model, context, options) {
      return base.streamSimple({ ...model, provider: base.id }, context, options);
    },
  };
}

/**
 * Gemini 的 OpenAI 兼容端点不认识 `store`。pi-ai 对「普通」兼容端点默认带
 * `store: false`，Google 会回 400。官方 base 以斜杠结尾；openai 7 在路径以
 * `/` 开头时不加斜杠也能拼对 `/chat/completions`，这里仍收成文档里的形式。
 */
export const GEMINI_NATIVE_BASE = "https://generativelanguage.googleapis.com/v1beta";

/** `models/gemini-4-argon` 和 `gemini-4-argon` 都落到路径里的那一节。 */
export function geminiModelId(modelId: string): string {
  return modelId.trim().replace(/^models\//, "");
}

export function geminiOpenAiEndpoint(baseUrl: string): {
  baseUrl: string;
  compat?: { supportsStore: false };
} {
  let host = "";
  let path = "";
  try {
    const url = new URL(baseUrl);
    host = url.hostname;
    path = url.pathname;
  } catch {
    return { baseUrl };
  }
  if (host !== "generativelanguage.googleapis.com" || !path.includes("/openai")) {
    return { baseUrl };
  }
  return {
    baseUrl: baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`,
    compat: { supportsStore: false },
  };
}

function geminiNativeModel(
  record: RegistryRecord,
  providerId: string,
  baseUrl: string,
): Model<"google-generative-ai"> | undefined {
  if (record.spec.kind !== "model") return undefined;
  const spec = record.spec;
  return {
    id: geminiModelId(spec.modelId),
    name: geminiModelId(spec.modelId),
    api: "google-generative-ai",
    provider: providerId,
    baseUrl,
    input: spec.input ?? ["text"],
    cost: {
      input: spec.cost?.input ?? 0,
      output: spec.cost?.output ?? 0,
      cacheRead: spec.cost?.cacheRead ?? 0,
      cacheWrite: spec.cost?.cacheWrite ?? 0,
    },
    reasoning: spec.reasoning ?? false,
    contextWindow: spec.contextWindow ?? 128000,
    maxTokens: spec.maxTokens ?? 4096,
  };
}

function compatibleModel(
  record: RegistryRecord,
  providerId: string,
  baseUrl: string,
): Model<Api> | undefined {
  if (record.spec.kind !== "model") return undefined;
  const spec = record.spec;
  const api = spec.api ?? "openai-completions";
  const resolved = spec.baseUrl ?? baseUrl;
  const gemini =
    api === "openai-completions" ? geminiOpenAiEndpoint(resolved) : { baseUrl: resolved };
  return {
    id: spec.modelId,
    name: spec.modelId,
    api,
    provider: providerId,
    baseUrl: gemini.baseUrl,
    input: spec.input ?? ["text"],
    cost: {
      input: spec.cost?.input ?? 0,
      output: spec.cost?.output ?? 0,
      cacheRead: spec.cost?.cacheRead ?? 0,
      cacheWrite: spec.cost?.cacheWrite ?? 0,
    },
    reasoning: spec.reasoning ?? false,
    contextWindow: spec.contextWindow ?? 8192,
    maxTokens: spec.maxTokens ?? 1024,
    ...(gemini.compat ? { compat: gemini.compat } : {}),
  };
}

function providerForCredential(
  credential: CredentialRef,
  options: {
    models: MutableModels;
    entries: RegistryRecord[];
    openSecret: (id: string) => Promise<Revealable>;
    env: NodeJS.ProcessEnv;
  },
): Provider | undefined {
  const open = () => options.openSecret(credential.id);
  if (credential.provider === "test") {
    if (!fauxAllowed(options.env)) return undefined;
    return byokFromBase(fauxFor(options.models).provider, credential, open);
  }
  if (credential.provider === "gemini") {
    const id = `byok_${credential.id}`;
    const baseUrl = (credential.baseUrl ?? GEMINI_NATIVE_BASE).replace(/\/$/, "");
    const models = options.entries
      .filter((entry) => entry.spec.kind === "model" && entry.spec.provider === "gemini")
      .map((entry) => geminiNativeModel(entry, id, baseUrl))
      .filter((model): model is Model<"google-generative-ai"> => model !== undefined);
    if (models.length === 0) return undefined;
    return createProvider({
      id,
      name: credential.label,
      baseUrl,
      auth: {
        apiKey: {
          name: credential.label,
          resolve: async () => ({
            auth: { apiKey: (await open()).reveal() },
            source: "vault",
          }),
        },
      },
      models,
      api: googleGenerativeAIApi(),
    });
  }
  if (
    credential.provider === "openai-compatible" ||
    credential.provider === "anthropic-compatible"
  ) {
    const id = `byok_${credential.id}`;
    const baseUrl = credential.baseUrl ?? "http://127.0.0.1:11434/v1";
    const models = options.entries
      .filter((entry) => entry.spec.kind === "model" && entry.spec.provider === credential.provider)
      .map((entry) => compatibleModel(entry, id, baseUrl))
      .filter((model): model is Model<Api> => model !== undefined);
    if (models.length === 0) return undefined;
    const anthropic = credential.provider === "anthropic-compatible";
    return createProvider({
      id,
      name: credential.label,
      baseUrl,
      auth: {
        apiKey: {
          name: credential.label,
          resolve: async () => ({
            auth: { apiKey: (await open()).reveal(), baseUrl },
            source: "vault",
          }),
        },
      },
      models,
      api: anthropic ? anthropicMessagesApi() : openAICompletionsApi(),
    });
  }
  const base = builtinById(credential.provider);
  if (!base) return undefined;
  return byokFromBase(base, credential, open);
}

/** 每把 active 凭据注册成 `byok_<id>`。停用的从集合里拿掉。 */
export function syncByokProviders(options: {
  models: MutableModels;
  credentials: CredentialRef[];
  entries: RegistryRecord[];
  openSecret: (id: string) => Promise<Revealable>;
  env?: NodeJS.ProcessEnv;
}): void {
  const env = options.env ?? process.env;
  const active = new Set<string>();
  for (const credential of options.credentials) {
    if (credential.status !== "active") continue;
    const provider = providerForCredential(credential, { ...options, env });
    if (!provider) continue;
    active.add(provider.id);
    options.models.setProvider(provider);
  }
  for (const existing of options.models.getProviders()) {
    if (existing.id.startsWith("byok_") && !active.has(existing.id)) {
      options.models.deleteProvider(existing.id);
    }
  }
}

export async function probeResolved(models: MutableModels, resolved: PricedModel): Promise<void> {
  if (resolved.ref === "test/faux" || resolved.piModelId === "faux-1") {
    fauxFor(models).appendResponses([fauxAssistantMessage("ok")]);
  }
  const model = models.getModel(resolved.piProviderId, resolved.piModelId);
  if (!model) throw new Error("模型没有注册");
  const message = await models.completeSimple(
    model,
    { messages: [{ role: "user", content: "ping", timestamp: Date.now() }] },
    { maxTokens: 1 },
  );
  if (message.stopReason === "error") throw new Error("探测失败");
}

export type { CatalogModel };
