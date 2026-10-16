const DEFAULT_HOST = "127.0.0.1";
const DEFAULT_PORT = 8787;

export interface ListenAddress {
  host: string;
  port: number;
  /** 绑定地址不是本机回环时给出。 */
  warning?: string;
}

export function isLoopbackHost(host: string): boolean {
  const normalized = host
    .trim()
    .toLowerCase()
    .replace(/^\[|\]$/g, "");
  if (normalized === "localhost" || normalized === "::1") return true;
  return /^127(?:\.\d{1,3}){3}$/.test(normalized);
}

/** 默认只绑 127.0.0.1。`LW_HOST` 改成外网地址时带上警告文案。 */
export function resolveListen(env: NodeJS.ProcessEnv = process.env): ListenAddress {
  const host = env.LW_HOST?.trim() || DEFAULT_HOST;
  const rawPort = env.LW_PORT?.trim() || String(DEFAULT_PORT);
  const port = Number(rawPort);
  if (!Number.isInteger(port) || port < 0 || port > 65535) {
    throw new Error(`LW_PORT 不合法：${rawPort}`);
  }
  if (isLoopbackHost(host)) return { host, port };
  return {
    host,
    port,
    warning: `警告：LW_HOST=${host} 不是本机回环地址。不论绑定在哪，所有 /api/v1 请求都必须带所有者令牌或 API Key。`,
  };
}

export function listenUrl(host: string, port: number): string {
  const bracket = host.includes(":") && !host.startsWith("[");
  return `http://${bracket ? `[${host}]` : host}:${port}`;
}
