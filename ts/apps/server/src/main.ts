import { startServer } from "./serve.js";

const running = await startServer();
if (running.warning) process.stderr.write(`${running.warning}\n`);
if (running.owner.created) {
  process.stdout.write(
    `已创建所有者 ${running.owner.userId}\n令牌文件 ${running.owner.tokenPath}\n`,
  );
}
process.stdout.write(`监听 ${running.url}\n`);

const shutdown = () => {
  void running.close().then(
    () => process.exit(0),
    (error: unknown) => {
      process.stderr.write(`${error instanceof Error ? error.message : "关闭失败"}\n`);
      process.exit(1);
    },
  );
};
process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
