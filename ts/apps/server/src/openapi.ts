import { extendZodWithOpenApi, OpenAPIHono } from "@hono/zod-openapi";
import { ApiTaskSchema, CreateTaskBodySchema, TaskMessageBodySchema } from "@luminwrite/contracts";
import { z } from "zod";

const ErrorSchema = z.object({ error: z.string() });
const IdParams = z.object({ id: z.string() });

const bearer = [{ apiKey: [] }];

function json(schema: z.ZodType, description: string) {
  return {
    description,
    content: { "application/json": { schema } },
  };
}

let cached: ReturnType<OpenAPIHono["getOpenAPIDocument"]> | undefined;

/** 由 `@hono/zod-openapi` 从路由声明生成。描述外部 Agent 用的 `/api/v1` 表面。 */
export function openApiDocument(): ReturnType<OpenAPIHono["getOpenAPIDocument"]> {
  if (cached) return cached;
  extendZodWithOpenApi(z);
  const spec = new OpenAPIHono();
  spec.openAPIRegistry.registerComponent("securitySchemes", "apiKey", {
    type: "http",
    scheme: "bearer",
    bearerFormat: "API Key",
    description: "所有者签发的 API Key",
  });
  spec.openAPIRegistry.registerPath({
    method: "post",
    path: "/tasks",
    tags: ["tasks"],
    summary: "提交写作或修订任务",
    security: bearer,
    request: {
      headers: z.object({ "Idempotency-Key": z.string().min(1).max(200) }),
      body: {
        required: true,
        content: { "application/json": { schema: CreateTaskBodySchema } },
      },
    },
    responses: {
      202: json(ApiTaskSchema, "任务已接受，回合在后台跑"),
      200: json(ApiTaskSchema, "同一个 Idempotency-Key 已经有任务"),
      400: json(ErrorSchema, "参数不合法"),
      403: json(ErrorSchema, "密钥不能用这个档位，或这不是 API Key"),
      429: json(ErrorSchema, "超过并发上限"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "get",
    path: "/tasks/{id}",
    tags: ["tasks"],
    summary: "读取任务状态",
    security: bearer,
    request: { params: IdParams },
    responses: {
      200: json(ApiTaskSchema, "任务"),
      404: json(ErrorSchema, "找不到任务"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "get",
    path: "/tasks/{id}/events",
    tags: ["tasks"],
    summary: "SSE。先补发 Last-Event-ID 或 after 之后的事件，再等到任务停下来。",
    security: bearer,
    request: {
      params: IdParams,
      query: z.object({ after: z.string().optional() }),
      headers: z.object({ "Last-Event-ID": z.string().optional() }),
    },
    responses: {
      200: {
        description: "text/event-stream",
        content: { "text/event-stream": { schema: z.string() } },
      },
      404: json(ErrorSchema, "找不到任务"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "post",
    path: "/tasks/{id}/messages",
    tags: ["tasks"],
    summary: "input_required 时回复，例如批准大纲",
    security: bearer,
    request: {
      params: IdParams,
      body: {
        required: true,
        content: { "application/json": { schema: TaskMessageBodySchema } },
      },
    },
    responses: {
      202: json(ApiTaskSchema, "已收下回复"),
      409: json(ErrorSchema, "任务不在等待输入"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "post",
    path: "/tasks/{id}/cancel",
    tags: ["tasks"],
    summary: "取消还没结束的任务",
    security: bearer,
    request: { params: IdParams },
    responses: {
      200: json(ApiTaskSchema, "已取消"),
      409: json(ErrorSchema, "任务已经结束"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "get",
    path: "/documents/{id}",
    tags: ["documents"],
    summary: "读取文档和块",
    security: bearer,
    request: { params: IdParams },
    responses: {
      200: json(z.object({ id: z.string(), title: z.string() }).passthrough(), "文档"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "get",
    path: "/documents/{id}/versions",
    tags: ["documents"],
    summary: "版本列表",
    security: bearer,
    request: { params: IdParams },
    responses: {
      200: json(z.array(z.object({ id: z.string(), seq: z.number() }).passthrough()), "版本"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "post",
    path: "/documents/{id}/restore",
    tags: ["documents"],
    summary: "回滚到一个旧版本，写成新版本",
    security: bearer,
    request: {
      params: IdParams,
      body: {
        required: true,
        content: {
          "application/json": { schema: z.object({ versionId: z.string().min(1) }) },
        },
      },
    },
    responses: {
      200: json(z.object({ ok: z.boolean(), versionId: z.string().optional() }), "回滚结果"),
    },
  });
  spec.openAPIRegistry.registerPath({
    method: "get",
    path: "/library/articles",
    tags: ["library"],
    summary: "已保存文档",
    security: bearer,
    request: {
      query: z.object({
        q: z.string().optional(),
        tag: z.string().optional(),
        limit: z.string().optional(),
      }),
    },
    responses: {
      200: json(z.array(z.object({ id: z.string(), title: z.string() }).passthrough()), "文章"),
    },
  });
  cached = spec.getOpenAPIDocument({
    openapi: "3.0.3",
    info: {
      title: "LuminWrite",
      version: "0.0.0",
      description: "外部 Agent 用的任务 API。服务器默认只绑 127.0.0.1。",
    },
    servers: [{ url: "/api/v1" }],
  });
  return cached;
}
