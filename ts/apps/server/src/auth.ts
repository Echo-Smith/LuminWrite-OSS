import type { Actor } from "@luminwrite/contracts";
import { authenticateApiKey, authenticateOwnerToken, type LwDatabase } from "@luminwrite/db";
import type { AuthPort } from "@luminwrite/edition";

export interface OssAuthOptions {
  /**
   * 默认查库里的 API Key（只认哈希）。测试可以换掉。
   * 命中后应返回 `{ type: "api_client", userId, keyId }`。
   */
  authenticateApiKey?: (token: string) => Promise<Actor | null>;
}

export function bearerToken(req: {
  headers: { get(name: string): string | null };
}): string | undefined {
  const header = req.headers.get("authorization");
  if (!header) return undefined;
  const match = /^Bearer\s+(\S+)\s*$/i.exec(header);
  return match?.[1];
}

/** OSS 的 AuthPort：先认所有者令牌，再认 API Key。请求体里的身份不参与。 */
export function createOssAuth(db: LwDatabase, options: OssAuthOptions = {}): AuthPort {
  const authenticateKey = options.authenticateApiKey ?? ((token) => authenticateApiKey(db, token));
  return {
    async authenticate(req) {
      const token = bearerToken(req);
      if (!token) return null;
      const owner = await authenticateOwnerToken(db, token);
      if (owner) return { type: "user", userId: owner.id };
      return authenticateKey(token);
    },
  };
}
