import { getBuiltinModels, getBuiltinProviders } from "@earendil-works/pi-ai/providers/all";

export interface CatalogModel {
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
  cost: { input: number; output: number; cacheRead: number; cacheWrite: number };
}

const TEST_FAUX: CatalogModel = {
  ref: "test/faux",
  provider: "test",
  modelId: "faux-1",
  name: "测试模型",
  api: "faux",
  baseUrl: "http://127.0.0.1",
  contextWindow: 8192,
  maxTokens: 1024,
  input: ["text"],
  reasoning: false,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
};

let builtins: Map<string, CatalogModel> | undefined;

function builtinCatalog(): Map<string, CatalogModel> {
  if (builtins) return builtins;
  const models = new Map<string, CatalogModel>();
  for (const provider of getBuiltinProviders()) {
    for (const model of getBuiltinModels(provider)) {
      const input = model.input.filter(
        (item): item is "text" | "image" => item === "text" || item === "image",
      );
      models.set(`${provider}/${model.id}`, {
        ref: `${provider}/${model.id}`,
        provider,
        modelId: model.id,
        name: model.name,
        api: model.api,
        baseUrl: model.baseUrl,
        contextWindow: model.contextWindow,
        maxTokens: model.maxTokens,
        input,
        reasoning: model.reasoning,
        cost: {
          input: model.cost.input,
          output: model.cost.output,
          cacheRead: model.cost.cacheRead,
          cacheWrite: model.cost.cacheWrite,
        },
      });
    }
  }
  builtins = models;
  return models;
}

export function fauxAllowed(env: NodeJS.ProcessEnv = process.env): boolean {
  return env.NODE_ENV === "test" || env.LW_ALLOW_FAUX === "1";
}

/** pi-ai 从 models.dev 生成的目录。测试环境再加 `test/faux`，不访问网络。 */
export function catalogModels(env: NodeJS.ProcessEnv = process.env): Map<string, CatalogModel> {
  const models = new Map(builtinCatalog());
  if (fauxAllowed(env)) models.set(TEST_FAUX.ref, TEST_FAUX);
  return models;
}
