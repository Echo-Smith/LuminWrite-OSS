import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { Type } from "@earendil-works/pi-ai";
import { createModels } from "@earendil-works/pi-ai/models";
import {
  fauxAssistantMessage,
  fauxProvider,
  fauxToolCall,
} from "@earendil-works/pi-ai/providers/faux";
import { createRegistry, defineExtension, defineTool, Harness } from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";

const [, , sqlitePath, phase, counterPath, releasePath, submissionPath] = process.argv;
const ctx = BACKGROUND_CONTEXT;

const touch = defineTool({
  name: "touch",
  description: "写一个计数器，用来观察崩溃后续跑。",
  parameters: Type.Object({}),
  replay: "safe",
  async execute(_args, api, context) {
    const phaseMemo = await api.memo("phase", context);
    if (phaseMemo === undefined) {
      await api.memo("phase", "waiting", context);
      writeFileSync(counterPath, "1");
      const started = Date.now();
      while (!existsSync(releasePath)) {
        if (Date.now() - started > 20_000) throw new Error("等待释放超时");
        await new Promise((resolve) => setTimeout(resolve, 20));
      }
    }
    writeFileSync(counterPath, "2");
    return { output: "ok" };
  },
});

const faux = fauxProvider();
const models = createModels();
models.setProvider(faux.provider);
const respond = (context) => {
  faux.appendResponses([respond]);
  const messages = context.messages ?? [];
  for (let index = messages.length - 1; index >= 0; index -= 1) {
    const message = messages[index];
    if (!message) continue;
    if (message.role === "user") break;
    if (message.role === "toolResult") return fauxAssistantMessage("ok");
  }
  return fauxAssistantMessage([fauxToolCall("touch", {})], { stopReason: "toolUse" });
};
faux.appendResponses([respond]);

const registry = createRegistry();
registry.install(defineExtension({ name: "crash", tools: [touch] }));
const storage = await openNodeSqliteStorage(sqlitePath);
const harness = await Harness.open(storage, { models, registry }, ctx);
harness.resume();

if (phase === "start") {
  const conversation = await harness.createConversation(
    {
      ownership: { kind: "ownerless" },
      agent: { model: { provider: "faux", modelId: "faux-1" } },
    },
    ctx,
  );
  const submission = await conversation.submit(
    { type: "input", content: "touch", requestId: "crash-1" },
    ctx,
  );
  writeFileSync(submissionPath, String(submission.id));
  await submission.wait(ctx);
} else {
  const id = Number(readFileSync(submissionPath, "utf8"));
  const submission = await harness.submission(id, ctx);
  if (!submission) throw new Error("找不到上次的提交");
  await submission.wait(ctx);
  writeFileSync(submissionPath, String(submission.id));
}

await harness.close(ctx);
process.stdout.write("CHILD_OK\n");
