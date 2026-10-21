import type { Actor } from "@luminwrite/contracts";
import { authenticateOwnerToken, type LwDatabase } from "@luminwrite/db";
import type { AuthPort } from "@luminwrite/edition";

export interface OssAuthOptions {
  /**
   * M1-02 接入 API Key。未提供时，对不上所有者令牌的 Bearer 一律拒绝。
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

/** OSS 的 AuthPort：所有者令牌，以及可选的 API Key 钩子。请求体里的身份不参与。 */
export function createOssAuth(db: LwDatabase, options: OssAuthOptions = {}): AuthPort {
  return {
    async authenticate(req) {
      const token = bearerToken(req);
      if (!token) return null;
      const owner = await authenticateOwnerToken(db, token);
      if (owner) return { type: "user", userId: owner.id };
      if (!options.authenticateApiKey) return null;
      return options.authenticateApiKey(token);
    },
  };
}
