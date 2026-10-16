import type { JsonValue } from "@earendil-works/chord";
import { BACKGROUND_CONTEXT, type Context } from "@earendil-works/chord/context";
import {
  type Message,
  type MutableModels,
  type TranscriptContext,
  Type,
} from "@earendil-works/pi-ai";
import { fauxAssistantMessage, fauxToolCall } from "@earendil-works/pi-ai/providers/faux";
import {
  type Conversation,
  type ConversationId,
  createRegistry,
  defineExtension,
  defineTool,
  Harness,
} from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";
import {
  ActorSchema,
  asBlockId,
  type DossierContent,
  type EditOp,
  EditOpSchema,
  type ModelSlot,
  type RunEvent,
  type TurnRequest,
  type TurnStatus,
} from "@luminwrite/contracts";
import type { UsageSink } from "@luminwrite/edition";
import {
  assertTurnSpend,
  chargeMicros,
  createClosedModels,
  type FullRates,
  fauxAllowed,
  fauxFor,
  type PricedModel,
  runWithFallback,
  secondsUntilUtcMidnight,
  UpstreamModelError,
} from "@luminwrite/models";

import { type PlanBlock, planOps, targetIndex } from "./plan.js";
import type { HarnessChange, HarnessPort } from "./port.js";
import { screenOps } from "./screen.js";
import type { DossierSnapshot, SessionSnapshot, WritingStore } from "./store.js";

const ctx: Context = BACKGROUND_CONTEXT;

const terminal = new Set<TurnStatus>(["applied", "proposed", "failed", "conflicted", "cancelled"]);

function parseTag(text: string, tag: string): string {
  const match = new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`).exec(text);
  return match?.[1] ?? "";
}

function latestUserText(messages: readonly Message[]): string {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message || message.role !== "user") continue;
    if (typeof message.content === "string") return message.content;
    return message.content.map((part) => (part.type === "text" ? part.text : "")).join("");
  }
  return "";
}

function toolRanAfterLatestUser(messages: readonly Message[]): boolean {
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message) continue;
    if (message.role === "user") return false;
    if (message.role === "toolResult") return true;
  }
  return false;
}

function promptFor(
  turn: TurnRequest,
  turnId: string,
  session: SessionSnapshot,
  dossier: DossierContent,
  blocks: PlanBlock[],
  scopeIds: string[],
): string {
  const payload = {
    documentId: turn.documentId,
    sessionId: session.id,
    turnId,
    kind: turn.kind,
    instruction: turn.instruction,
    applyMode: turn.applyMode,
    actor: turn.actor,
    selection: turn.selection ?? [],
    scopeIds,
    baseVersionId: session.baseVersionId,
  };
  return [
    "只通过 propose_edits 提交修订。锚点来自会话的基准版本，服务端按文档当前版本校验。",
    "先读文档档案，不要依赖更早的聊天记录。",
    `<dossier>${JSON.stringify(dossier)}</dossier>`,
    `<turn>${JSON.stringify(payload)}</turn>`,
    `<blocks>${JSON.stringify(blocks)}</blocks>`,
  ].join("\n");
}

export interface TurnResult {
  turnId: string;
  status: TurnStatus;
  submissionId: string;
  versionId?: string;
}

export interface WritingRuntime extends HarnessPort {
  submitTurn(req: TurnRequest): Promise<TurnResult>;
  events(documentId: string, afterSeq?: number): Promise<RunEvent[]>;
  readRelatedDossier(sessionId: string, documentId: string): Promise<DossierSnapshot>;
}

export interface TurnModelChoice {
  chain: PricedModel[];
  conservative: FullRates;
}

const LOCAL_FAUX: PricedModel = {
  ref: "test/faux",
  piProviderId: "test",
  piModelId: "faux-1",
  credentialId: "local",
  price: { known: false, source: "none" },
  rates: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
};

function slotFor(kind: TurnRequest["kind"]): ModelSlot {
  if (kind === "research") return "research.plan";
  if (kind === "review") return "role.reviewer";
  return "writer.main";
}

function usesFaux(model: PricedModel): boolean {
  return model.ref === "test/faux" || model.piModelId === "faux-1" || model.piProviderId === "test";
}

function assistantStarted(message: Message): boolean {
  if (message.role !== "assistant") return false;
  return message.content.some((part) => {
    if (part.type === "text") return part.text.length > 0;
    if (part.type === "thinking") return part.thinking.length > 0;
    return part.type === "toolCall";
  });
}

function assistantsOf(entries: readonly { model?: readonly Message[] }[]): Message[] {
  const messages: Message[] = [];
  for (const entry of entries) {
    for (const message of entry.model ?? []) {
      if (message.role === "assistant") messages.push(message);
    }
  }
  return messages;
}

function detailText(detail: unknown): string {
  if (typeof detail === "string") return detail;
  if (detail && typeof detail === "object" && "message" in detail) {
    const message = (detail as { message?: unknown }).message;
    if (typeof message === "string") return message;
  }
  return "";
}

function statusOf(text: string): number | undefined {
  if (/\b429\b/.test(text) || /rate limit/i.test(text)) return 429;
  if (/\b401\b|\b403\b|invalid api key|incorrect api key/i.test(text)) return 401;
  const five = /\b5\d\d\b/.exec(text);
  return five ? Number(five[0]) : undefined;
}

function retryableFailure(text: string): boolean {
  const status = statusOf(text);
  if (status === 429 || status === 401 || (status !== undefined && status >= 500)) return true;
  return /network|fetch failed|ECONN|ETIMEDOUT|timeout|invalid/i.test(text);
}

export async function createWritingRuntime(options: {
  sqlitePath: string;
  store: WritingStore;
  planner?: typeof planOps;
  usage?: UsageSink;
  models?: MutableModels;
  env?: NodeJS.ProcessEnv;
  /** 缺省时回合走进程内 test/faux，不访问网络。服务进程传入注册表解析。 */
  resolveTurn?(input: {
    actor: TurnRequest["actor"];
    slot: ModelSlot;
    prefer?: string;
    sessionPin: string | null;
    now: number;
    cooldownUntil: ReadonlyMap<string, number>;
  }): Promise<TurnModelChoice>;
  turnSpend?(actor: TurnRequest["actor"]): Promise<{
    userMicros: number;
    keyMicros: number;
    ownerCapUsd?: string;
    keyCapUsd?: number | null;
  }>;
}): Promise<WritingRuntime> {
  const planner = options.planner ?? planOps;
  const storage = await openNodeSqliteStorage(options.sqlitePath);
  const models = options.models ?? createClosedModels();
  const faux = fauxFor(models);
  models.setProvider(faux.provider);
  const cooldownUntil = new Map<string, number>();
  const respond = (context: TranscriptContext) => {
    faux.appendResponses([respond]);
    if (toolRanAfterLatestUser(context.messages)) return fauxAssistantMessage("已处理这一回合。");
    const text = latestUserText(context.messages);
    const turn = JSON.parse(parseTag(text, "turn") || "{}") as {
      documentId?: string;
      sessionId?: string;
      turnId?: string;
      kind?: TurnRequest["kind"];
      instruction?: string;
      applyMode?: "auto" | "proposal";
      actor?: TurnRequest["actor"];
      selection?: Array<{ blockId: string }>;
      scopeIds?: string[];
    };
    const blocks = JSON.parse(parseTag(text, "blocks") || "[]") as PlanBlock[];
    if (
      !turn.kind ||
      !turn.documentId ||
      !turn.sessionId ||
      !turn.turnId ||
      !turn.applyMode ||
      !turn.actor
    ) {
      return fauxAssistantMessage("回合信息不完整。");
    }
    const related = /^读取关联档案\s+(\S+)/.exec(turn.instruction ?? "");
    if (related?.[1]) {
      return fauxAssistantMessage(
        [
          fauxToolCall("read_related_dossier", {
            sessionId: turn.sessionId,
            documentId: related[1],
          }),
        ],
        { stopReason: "toolUse" },
      );
    }
    const planned = planner({
      kind: turn.kind,
      instruction: turn.instruction ?? "",
      blocks,
      selection: turn.selection,
    });
    if (!planned.ok) return fauxAssistantMessage(planned.reason);
    return fauxAssistantMessage(
      [
        fauxToolCall("propose_edits", {
          documentId: turn.documentId,
          sessionId: turn.sessionId,
          turnId: turn.turnId,
          applyMode: turn.applyMode,
          summary: planned.summary,
          actorJson: JSON.stringify(turn.actor),
          scopeJson: JSON.stringify(turn.scopeIds ?? []),
          opsJson: JSON.stringify(planned.ops),
        }),
      ],
      { stopReason: "toolUse" },
    );
  };
  faux.appendResponses([respond]);

  const readBlocks = defineTool({
    name: "read_blocks",
    description: "读取文档当前块的 id、哈希和文本。",
    parameters: Type.Object({ sessionId: Type.String(), documentId: Type.String() }),
    replay: "safe",
    async execute(args) {
      const allowed = await options.store.canRead(args.sessionId, args.documentId);
      if (!allowed) {
        return { isError: true, output: [{ type: "text", text: "这个会话不能读这篇文档" }] };
      }
      const blocks = await options.store.readBlocks(args.documentId);
      return { output: [{ type: "text", text: JSON.stringify(blocks) }] };
    },
  });

  const readRelatedDossier = defineTool({
    name: "read_related_dossier",
    description: "只读有血缘或已授权文档的当前档案。不复制、不同步到本篇。",
    parameters: Type.Object({ sessionId: Type.String(), documentId: Type.String() }),
    replay: "safe",
    async execute(args) {
      try {
        const dossier = await options.store.readRelatedDossier(args.sessionId, args.documentId);
        return {
          output: [
            {
              type: "text",
              text: JSON.stringify({ version: dossier.version, content: dossier.content }),
            },
          ],
        };
      } catch (error) {
        const message = error instanceof Error ? error.message : "不能读取档案";
        return { isError: true, output: [{ type: "text", text: message }] };
      }
    },
  });

  const proposeEdits = defineTool({
    name: "propose_edits",
    description: "提交块锚点修订。哈希和说人话由服务端校验。",
    parameters: Type.Object({
      documentId: Type.String(),
      sessionId: Type.String(),
      turnId: Type.String(),
      applyMode: Type.Union([Type.Literal("auto"), Type.Literal("proposal")]),
      summary: Type.String(),
      actorJson: Type.String(),
      scopeJson: Type.String(),
      opsJson: Type.String(),
    }),
    replay: "safe",
    async execute(args, api, context) {
      const parsedOps = EditOpSchema.array().safeParse(JSON.parse(args.opsJson));
      const parsedActor = ActorSchema.safeParse(JSON.parse(args.actorJson));
      if (!parsedOps.success || !parsedActor.success) {
        await options.store.markTurn(args.turnId, { status: "failed", error: "修订格式不合法" });
        return { isError: true, output: [{ type: "text", text: "修订格式不合法" }] };
      }
      const ops = parsedOps.data as EditOp[];
      const report = screenOps(ops);
      if (!report.pass) {
        await options.store.markTurn(args.turnId, {
          status: "failed",
          error: "没有通过说人话检查",
        });
        return { isError: true, output: [{ type: "text", text: "没有通过说人话检查" }] };
      }
      const cached = await api.memo("result", context);
      if (cached !== undefined) return { output: [{ type: "text", text: JSON.stringify(cached) }] };
      const scope = (JSON.parse(args.scopeJson) as string[]).map((id) => asBlockId(id));
      const outcome = await options.store.propose({
        documentId: args.documentId,
        sessionId: args.sessionId,
        turnId: args.turnId,
        ops,
        piTaskId: String(api.taskId),
        applyMode: args.applyMode,
        actor: parsedActor.data,
        summary: args.summary,
        ...(scope.length > 0 ? { scope } : {}),
      });
      const saved = await api.memo(
        "result",
        JSON.parse(JSON.stringify(outcome)) as JsonValue,
        context,
      );
      if (!outcome.ok)
        return { isError: true, output: [{ type: "text", text: JSON.stringify(outcome.errors) }] };
      return { output: [{ type: "text", text: JSON.stringify(saved) }] };
    },
  });

  const registry = createRegistry();
  registry.install(
    defineExtension({ name: "lw-writing", tools: [readBlocks, readRelatedDossier, proposeEdits] }),
  );
  const harness = await Harness.open(
    storage,
    {
      models,
      registry,
      settings: { retry: { enabled: false, maxRetries: 0, baseDelayMs: 0 } },
    },
    ctx,
  );
  harness.resume();
  const seenEntries = new Set<string>();

  async function conversationFor(session: SessionSnapshot): Promise<Conversation> {
    if (session.piConversationId) {
      const found = await harness.conversation(
        Number(session.piConversationId) as ConversationId,
        ctx,
      );
      if (found) return found;
    }
    const created = await harness.createConversation(
      {
        ownership: { kind: "ownerless" },
        agent: { model: { provider: "test", modelId: "faux-1" } },
      },
      ctx,
    );
    await options.store.setSessionConversation(session.id, String(created.id));
    session.piConversationId = String(created.id);
    return created;
  }

  async function translate(
    conversation: Conversation,
    documentId: string,
    turnId: string,
  ): Promise<void> {
    const handle = await conversation.watch(ctx);
    try {
      for (const entry of handle.value.entries) {
        const key = `${documentId}:${entry.id}`;
        if (seenEntries.has(key)) continue;
        seenEntries.add(key);
        await options.store.appendEvent(documentId, {
          turnId,
          type: entry.kind,
          payload: { entryId: entry.id },
        });
      }
    } finally {
      await handle.stop();
    }
  }

  const runtime: WritingRuntime = {
    async submit(sessionId, input) {
      const session = await options.store.getSession(sessionId);
      if (!session) throw new Error(`找不到会话 ${sessionId}`);
      const conversation = await conversationFor(session);
      const submission = await conversation.submit(
        {
          type: "input",
          content: input.content,
          requestId: input.requestId,
          whenBusy: input.whenBusy,
        },
        ctx,
      );
      return { submissionId: String(submission.id) };
    },
    async abort(sessionId) {
      const session = await options.store.getSession(sessionId);
      if (!session) throw new Error(`找不到会话 ${sessionId}`);
      const conversation = await conversationFor(session);
      await conversation.abort(ctx);
    },
    async resume() {
      harness.resume();
    },
    watch(sessionId) {
      return watchConversation(sessionId);
    },
    async close() {
      await harness.close(ctx);
    },
    events(documentId, afterSeq) {
      return options.store.eventsAfter(documentId, afterSeq);
    },
    readRelatedDossier(sessionId, documentId) {
      return options.store.readRelatedDossier(sessionId, documentId);
    },
    async submitTurn(req) {
      const begun = await options.store.beginTurn(req);
      const existing = await options.store.getTurn(begun.turnId);
      if (!begun.created && existing && terminal.has(existing.status)) {
        return {
          turnId: begun.turnId,
          status: existing.status,
          submissionId: existing.piSubmissionId ?? "",
          ...(existing.resultVersionId ? { versionId: existing.resultVersionId } : {}),
        };
      }
      if (options.turnSpend) {
        const spend = await options.turnSpend(req.actor);
        assertTurnSpend({ ...spend, retryAfterSeconds: secondsUntilUtcMidnight() });
      }
      const session = await options.store.sessionFor(req);
      const now = Date.now();
      const choice = options.resolveTurn
        ? await options.resolveTurn({
            actor: req.actor,
            slot: slotFor(req.kind),
            ...(req.model ? { prefer: req.model } : {}),
            sessionPin: session.modelPin,
            now,
            cooldownUntil,
          })
        : { chain: [], conservative: LOCAL_FAUX.rates };
      let chain = choice.chain;
      if (chain.length === 0) {
        if (!options.resolveTurn || fauxAllowed(options.env)) chain = [LOCAL_FAUX];
        else {
          await options.store.markTurn(begun.turnId, { status: "failed", error: "没有可用模型" });
          return { turnId: begun.turnId, status: "failed", submissionId: "" };
        }
      }
      await options.store.markTurn(begun.turnId, { status: "running" });
      const dossier = await options.store.readDossier(req.documentId);
      const blocks = await options.store.readBlocksAt(req.documentId, session.baseVersionId);
      const index = targetIndex(req.instruction, blocks, req.selection);
      const scoped = req.kind === "revise" ? blocks[index]?.blockId : undefined;
      const conversation = await conversationFor(session);
      const prompt = promptFor(
        req,
        begun.turnId,
        session,
        dossier.content,
        blocks,
        scoped ? [scoped] : [],
      );
      await options.store.appendEvent(req.documentId, {
        turnId: begun.turnId,
        type: "turn.started",
        payload: {
          kind: req.kind,
          sessionId: session.id,
          baseVersionId: session.baseVersionId,
          dossierVersion: dossier.version,
          dossier: dossier.content,
        },
      });
      const fallbacks: Array<{
        type: "model.fallback";
        from: string;
        to: string;
        credentialId: string;
        reason: string;
      }> = [];
      let outcome: {
        value: {
          submissionId: string;
          usage: { input: number; output: number; cacheRead: number; cacheWrite: number };
        };
        used: PricedModel;
      };
      try {
        outcome = await runWithFallback({
          chain,
          now,
          cooldownUntil,
          onEvent: (event) => {
            fallbacks.push(event);
          },
          run: async (model, markToken) => {
            await conversation.configure(
              { model: { provider: model.piProviderId, modelId: model.piModelId } },
              ctx,
            );
            if (usesFaux(model)) faux.appendResponses([respond]);
            const before = (await conversation.context(ctx)).entries.length;
            const watch = await conversation.watch(ctx);
            const noteTokens = () => {
              const fresh = assistantsOf(watch.value.entries.slice(before));
              if (fresh.some(assistantStarted)) markToken();
            };
            watch.start(async () => {
              noteTokens();
            });
            try {
              const submission = await conversation.submit(
                {
                  type: "input",
                  content: prompt,
                  requestId: `${req.idempotencyKey}:${model.piProviderId}:${model.ref}`,
                  whenBusy: req.delivery === "steer" ? "steer" : "followUp",
                },
                ctx,
              );
              const settled = await submission.wait(ctx);
              noteTokens();
              const assistants = assistantsOf(
                (await conversation.context(ctx)).entries.slice(before),
              );
              const started = assistants.some(assistantStarted);
              if (started) markToken();
              if (settled.status === "unanswered") {
                const text = `${settled.reason} ${detailText(settled.detail)}`.trim();
                throw new UpstreamModelError(text || "模型调用失败", {
                  status: statusOf(text),
                  retryable: !started && retryableFailure(text),
                });
              }
              const usage = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
              for (const message of assistants) {
                if (message.role !== "assistant") continue;
                usage.input += message.usage.input;
                usage.output += message.usage.output;
                usage.cacheRead += message.usage.cacheRead;
                usage.cacheWrite += message.usage.cacheWrite;
              }
              return { submissionId: String(submission.id), usage };
            } catch (error) {
              if (error instanceof UpstreamModelError) throw error;
              const text = error instanceof Error ? error.message : "模型调用失败";
              throw new UpstreamModelError(text, {
                status: statusOf(text),
                retryable: retryableFailure(text),
              });
            } finally {
              await watch.stop();
            }
          },
        });
      } catch (error) {
        for (const event of fallbacks) {
          await options.store.appendEvent(req.documentId, {
            turnId: begun.turnId,
            type: event.type,
            payload: {
              from: event.from,
              to: event.to,
              credentialId: event.credentialId,
              reason: event.reason,
            },
          });
        }
        const message = error instanceof Error ? error.message : "没有可用模型";
        await options.store.markTurn(begun.turnId, { status: "failed", error: message });
        return { turnId: begun.turnId, status: "failed", submissionId: "" };
      }
      for (const event of fallbacks) {
        await options.store.appendEvent(req.documentId, {
          turnId: begun.turnId,
          type: event.type,
          payload: {
            from: event.from,
            to: event.to,
            credentialId: event.credentialId,
            reason: event.reason,
          },
        });
      }
      await options.store.markTurn(begun.turnId, {
        piConversationId: String(conversation.id),
        piSubmissionId: outcome.value.submissionId,
        modelRef: outcome.used.ref,
        tierRef: outcome.used.viaTier ?? null,
      });
      await options.store.setSessionModelPin(session.id, outcome.used.ref);
      await translate(conversation, req.documentId, begun.turnId);
      let turn = await options.store.getTurn(begun.turnId);
      if (turn && (turn.status === "running" || turn.status === "queued")) {
        await options.store.markTurn(begun.turnId, { status: "failed", error: "尚未实现" });
        turn = await options.store.getTurn(begun.turnId);
      }
      const tokens = outcome.value.usage;
      await options.usage?.record({
        requestId: req.idempotencyKey,
        model: outcome.used.piModelId,
        inputTokens: tokens.input,
        outputTokens: tokens.output,
        cacheReadTokens: tokens.cacheRead,
        cacheWriteTokens: tokens.cacheWrite,
        costMicros: chargeMicros(
          outcome.used.price.known,
          outcome.used.rates,
          choice.conservative,
          tokens,
        ),
        priceKnown: outcome.used.price.known,
        credentialId: outcome.used.credentialId,
        modelRef: outcome.used.ref,
        ...(outcome.used.viaTier ? { tierRef: outcome.used.viaTier } : {}),
        sessionId: session.id,
        turnId: begun.turnId,
        ...(req.actor.type === "system" ? {} : { userId: req.actor.userId }),
        ...(req.actor.type === "api_client" ? { apiKeyId: req.actor.keyId } : {}),
      });
      if (turn?.status === "applied" && turn.resultVersionId) {
        await options.store.advanceSessionBase(session.id, turn.resultVersionId);
      }
      return {
        turnId: begun.turnId,
        status: turn?.status ?? "failed",
        submissionId: outcome.value.submissionId,
        ...(turn?.resultVersionId ? { versionId: turn.resultVersionId } : {}),
      };
    },
  };

  async function* watchConversation(sessionId: string): AsyncGenerator<HarnessChange> {
    const session = await options.store.getSession(sessionId);
    if (!session) throw new Error(`找不到会话 ${sessionId}`);
    const conversation = await conversationFor(session);
    const handle = await conversation.watch(ctx);
    const queue: HarnessChange[] = [toChange(sessionId, handle.value.entries)];
    let pending: (() => void) | undefined;
    let stopped = false;
    handle.start(async (value) => {
      queue.push(toChange(sessionId, value.entries));
      pending?.();
      pending = undefined;
    });
    try {
      while (!stopped) {
        if (queue.length === 0) {
          await new Promise<void>((resolve) => {
            pending = resolve;
          });
        }
        if (stopped) return;
        const next = queue.shift();
        if (next) yield next;
      }
    } finally {
      stopped = true;
      pending?.();
      await handle.stop();
    }
  }

  return runtime;
}

function toChange(sessionId: string, entries: readonly { kind: string }[]): HarnessChange {
  const last = entries.at(-1);
  return {
    sessionId,
    entryCount: entries.length,
    ...(last ? { lastKind: last.kind } : {}),
  };
}
