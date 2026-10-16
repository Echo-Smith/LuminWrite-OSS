import { lookup as dnsLookup } from "node:dns/promises";
import type { IncomingHttpHeaders } from "node:http";
import { createRequire } from "node:module";
import { Agent, type Dispatcher, request } from "undici";

const require = createRequire(import.meta.url);
const ipaddr = require("ipaddr.js") as {
  isValid(value: string): boolean;
  parse(value: string): {
    range(): string;
    isIPv4MappedAddress?: () => boolean;
    toIPv4Address?: () => { range(): string };
  };
};

const MAX_BYTES = 8 * 1024 * 1024;
const TIMEOUT_MS = 20_000;
const MAX_REDIRECTS = 5;

const BLOCKED = new Set([
  "unspecified",
  "broadcast",
  "multicast",
  "linkLocal",
  "loopback",
  "private",
  "reserved",
  "carrierGradeNat",
  "uniqueLocal",
  "benchmarking",
  "as112",
  "as112v6",
  "amt",
  "ipv4Mapped",
]);

export class SafeFetchError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SafeFetchError";
  }
}

export interface LookupAddress {
  address: string;
  family: 4 | 6;
}

export type LookupFn = (host: string) => Promise<LookupAddress[]>;

export interface SafeFetchResult {
  status: number;
  body: string | null;
  etag?: string;
  lastModified?: string;
  finalUrl: string;
  contentType?: string;
}

export interface SafeFetchOptions {
  headers?: Record<string, string>;
  allowPrivate?: boolean;
  lookup?: LookupFn;
  dispatcher?: Dispatcher;
  timeoutMs?: number;
  maxBytes?: number;
  maxRedirects?: number;
}

export async function defaultLookup(host: string): Promise<LookupAddress[]> {
  const records = await dnsLookup(host, { all: true, verbatim: true });
  return records.map((record) => ({
    address: record.address,
    family: record.family === 6 ? 6 : 4,
  }));
}

function addressBlocked(address: string): boolean {
  if (!ipaddr.isValid(address)) return true;
  const parsed = ipaddr.parse(address);
  if (parsed.isIPv4MappedAddress?.()) {
    const mapped = parsed.toIPv4Address?.();
    if (!mapped) return true;
    return BLOCKED.has(mapped.range());
  }
  return BLOCKED.has(parsed.range());
}

function pinnedLookup(addresses: LookupAddress[]) {
  return (
    _hostname: string,
    options: { all?: boolean },
    callback: (
      error: NodeJS.ErrnoException | null,
      address: string | LookupAddress[],
      family?: number,
    ) => void,
  ) => {
    if (options.all) {
      callback(null, addresses);
      return;
    }
    const first = addresses[0];
    callback(null, first?.address ?? "", first?.family ?? 4);
  };
}

async function resolveHost(host: string, lookup: LookupFn): Promise<LookupAddress[]> {
  const bare = host.replace(/^\[|\]$/g, "");
  if (ipaddr.isValid(bare)) {
    return [{ address: bare, family: bare.includes(":") ? 6 : 4 }];
  }
  try {
    return await lookup(host);
  } catch {
    throw new SafeFetchError("无法解析地址");
  }
}

function header(headers: IncomingHttpHeaders, name: string): string | undefined {
  const raw = headers[name] ?? headers[name.toLowerCase()];
  const value = Array.isArray(raw) ? raw[0] : raw;
  return value?.trim() || undefined;
}

async function readBody(
  body: AsyncIterable<Uint8Array | string>,
  maxBytes: number,
): Promise<string> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of body) {
    const buffer = typeof chunk === "string" ? Buffer.from(chunk) : Buffer.from(chunk);
    total += buffer.length;
    if (total > maxBytes) {
      if ("destroy" in body && typeof body.destroy === "function") body.destroy();
      throw new SafeFetchError("响应超过 8MB");
    }
    chunks.push(buffer);
  }
  return Buffer.concat(chunks).toString("utf8");
}

function mapTransport(error: unknown): never {
  if (error instanceof SafeFetchError) throw error;
  const name = error instanceof Error ? error.name : "";
  const message = error instanceof Error ? error.message : "";
  if (name === "TimeoutError" || name === "AbortError" || /timeout|aborted/i.test(message)) {
    throw new SafeFetchError("请求超时");
  }
  if (name === "ResponseExceededMaxSizeError") throw new SafeFetchError("响应超过 8MB");
  throw new SafeFetchError("抓取失败");
}

/**
 * 只抓 http/https。先解析 DNS，任一地址落在私网、环回或链路本地就拒绝。
 * 每一跳重定向都重新检查。连接时的 lookup 钉在已经检查过的地址上。
 */
export async function safeFetch(
  rawUrl: string,
  options: SafeFetchOptions = {},
): Promise<SafeFetchResult> {
  const lookup = options.lookup ?? defaultLookup;
  const allowPrivate = options.allowPrivate === true;
  const timeoutMs = options.timeoutMs ?? TIMEOUT_MS;
  const maxBytes = options.maxBytes ?? MAX_BYTES;
  const maxRedirects = options.maxRedirects ?? MAX_REDIRECTS;
  let current: URL;
  try {
    current = new URL(rawUrl);
  } catch {
    throw new SafeFetchError("只接受 http 和 https");
  }

  for (let hop = 0; hop <= maxRedirects; hop += 1) {
    if (current.protocol !== "http:" && current.protocol !== "https:") {
      throw new SafeFetchError("只接受 http 和 https");
    }
    const addresses = await resolveHost(current.hostname, lookup);
    if (
      addresses.length === 0 ||
      (!allowPrivate && addresses.some((item) => addressBlocked(item.address)))
    ) {
      throw new SafeFetchError("地址被拒绝");
    }
    const agent = options.dispatcher
      ? undefined
      : new Agent({
          connect: { lookup: pinnedLookup(addresses) },
          headersTimeout: timeoutMs,
          bodyTimeout: timeoutMs,
          connectTimeout: timeoutMs,
          maxResponseSize: maxBytes,
        });
    const dispatcher = options.dispatcher ?? agent;
    try {
      let response: Awaited<ReturnType<typeof request>>;
      try {
        response = await request(current.href, {
          method: "GET",
          signal: AbortSignal.timeout(timeoutMs),
          ...(dispatcher ? { dispatcher } : {}),
          ...(options.headers ? { headers: options.headers } : {}),
        });
      } catch (error) {
        mapTransport(error);
      }
      const status = response.statusCode;
      if (status >= 300 && status < 400 && status !== 304) {
        const location = header(response.headers, "location");
        response.body.destroy();
        if (!location) throw new SafeFetchError("重定向没有目标");
        if (hop === maxRedirects) throw new SafeFetchError("重定向次数过多");
        current = new URL(location, current);
        continue;
      }
      const etag = header(response.headers, "etag");
      const lastModified = header(response.headers, "last-modified");
      const contentType = header(response.headers, "content-type");
      if (status === 304) {
        response.body.destroy();
        return {
          status,
          body: null,
          finalUrl: current.href,
          ...(etag ? { etag } : {}),
          ...(lastModified ? { lastModified } : {}),
          ...(contentType ? { contentType } : {}),
        };
      }
      const declared = Number(header(response.headers, "content-length"));
      if (Number.isFinite(declared) && declared > maxBytes) {
        response.body.destroy();
        throw new SafeFetchError("响应超过 8MB");
      }
      try {
        const body = await readBody(response.body, maxBytes);
        return {
          status,
          body,
          finalUrl: current.href,
          ...(etag ? { etag } : {}),
          ...(lastModified ? { lastModified } : {}),
          ...(contentType ? { contentType } : {}),
        };
      } catch (error) {
        response.body.destroy();
        mapTransport(error);
      }
    } finally {
      await agent?.close().catch(() => undefined);
    }
  }
  throw new SafeFetchError("重定向次数过多");
}
