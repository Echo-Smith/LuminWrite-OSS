/** `provider/modelId`、`tier/...`、`custom/...`，商业版还可以是 `hosted/...`。 */
export type ModelRef = string;

/** 用途，不是模型。角色和系统任务只认 slot。 */
export type ModelSlot =
  | "writer.main"
  | "role.researcher"
  | "role.reviewer"
  | "role.fact_check"
  | "role.citation_audit"
  | "research.plan"
  | "kb.embed"
  | "prefs.summarize";

export type TokenRates = {
  input: number;
  output: number;
  cacheRead?: number;
  cacheWrite?: number;
};

export type RegistryEntrySpec =
  | {
      kind: "model";
      provider: string;
      modelId: string;
      api?: "openai-completions" | "openai-responses" | "anthropic-messages";
      baseUrl?: string;
      contextWindow?: number;
      maxTokens?: number;
      input?: Array<"text" | "image">;
      reasoning?: boolean;
      /** 每百万 token 的美元价。`priceSource` 区分所有者手填和注册表声明。 */
      cost?: TokenRates;
      priceSource?: "user" | "registry";
      fineTune?: {
        baseModel: ModelRef;
        adapterId: string;
        evalRunId?: string;
        dataConsent: "none" | "explicit";
      };
    }
  | {
      kind: "tier";
      label: string;
      members: ModelRef[];
      requires?: { tools?: boolean; minContext?: number };
      evalGate?: { suite: string; runId: string; passed: boolean };
    };

export interface ResolvedModel {
  ref: ModelRef;
  viaTier?: ModelRef;
  piProviderId: string;
  piModelId: string;
  credentialId: string;
  price: { known: boolean; source: "user" | "registry" | "catalog" | "none" };
}

export interface ModelListItem {
  ref: ModelRef;
  kind: "model" | "tier";
  usable: boolean;
  why?: string;
}
