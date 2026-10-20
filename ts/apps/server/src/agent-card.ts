/** A2A v1.0 风格的发现文档。只声明 HTTP+JSON，JSON-RPC 留给后面的里程碑。 */
export function agentCard(origin: string): Record<string, unknown> {
  const root = origin.replace(/\/$/, "");
  const url = `${root}/api/v1`;
  return {
    protocolVersion: "1.0",
    name: "LuminWrite",
    description: "本地写作代理。用 API Key 提交写作或修订任务，通过 SSE 看进度，再按版本回滚。",
    url,
    version: "0.0.0",
    capabilities: {
      streaming: true,
      pushNotifications: false,
      stateTransitionHistory: true,
    },
    securitySchemes: {
      apiKey: {
        type: "http",
        scheme: "bearer",
        bearerFormat: "API Key",
        description: "所有者签发的 API Key，形如 lw_<prefix>_<secret>",
      },
    },
    security: [{ apiKey: [] }],
    defaultInputModes: ["text/plain", "application/json"],
    defaultOutputModes: ["application/json", "text/event-stream"],
    skills: [
      {
        id: "write",
        name: "写作",
        description: "按 brief 新建一篇已保存的文档并写出初稿。",
        tags: ["writing"],
        inputModes: ["application/json"],
        outputModes: ["application/json"],
      },
      {
        id: "revise",
        name: "修订",
        description: "在同一把密钥的 api 会话上修订已有文档。人改过的稿默认变成提案。",
        tags: ["writing"],
        inputModes: ["application/json"],
        outputModes: ["application/json"],
      },
    ],
    supportedInterfaces: [
      {
        protocolBinding: "HTTP+JSON",
        protocolVersion: "1.0",
        url,
      },
    ],
  };
}
