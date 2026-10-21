export { createInlineJobs, type InlineJobs } from "./inline.js";
export { openJobPort } from "./pgboss.js";
export type { JobPort, JobSendOptions } from "./port.js";

export const phase = "queue" as const;
export const note =
  "运行时用 pg-boss（PGlite 走 fromPglite，Postgres 走连接串）。测试用进程内队列，可手动推进时间。";
