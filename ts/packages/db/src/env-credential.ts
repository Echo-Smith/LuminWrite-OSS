import {
  createCredential,
  deleteCredential,
  revealCredential,
  updateCredential,
} from "./credentials.js";
import type { LwDatabase } from "./database.js";
import type { MasterKeyring } from "./vault.js";

/**
 * 只有所有者显式打开 `LW_LLM_ENABLE=1` 时，才把环境变量里的 key 加密导入成 label=env 的凭据。
 * 没打开时连 `OPENAI_API_KEY` 都不读。
 */
export async function importEnvCredential(
  db: LwDatabase,
  ring: MasterKeyring,
  env: NodeJS.ProcessEnv,
  userId: string,
): Promise<"imported" | "updated" | "skipped"> {
  if (env.LW_LLM_ENABLE !== "1") return "skipped";
  const apiKey = env.LW_LLM_API_KEY ?? env.OPENAI_API_KEY;
  if (!apiKey) return "skipped";
  const provider = (env.LW_LLM_PROVIDER ?? "openai").trim();
  const baseUrl = env.LW_LLM_BASE_URL?.trim() || null;
  const rows = await db.listCredentials(userId);
  const existing = rows.find((row) => row.label === "env");
  if (!existing) {
    await createCredential(db, ring, {
      userId,
      provider,
      label: "env",
      key: apiKey,
      ...(baseUrl ? { baseUrl } : {}),
      env,
    });
    return "imported";
  }
  const sameProvider = existing.provider === provider;
  const sameKey = revealCredential(existing, ring).reveal() === apiKey;
  const sameUrl = (existing.baseUrl ?? null) === baseUrl;
  if (sameProvider && sameKey && sameUrl) return "skipped";
  if (!sameProvider) {
    await deleteCredential(db, { id: existing.id, userId });
    await createCredential(db, ring, {
      userId,
      provider,
      label: "env",
      key: apiKey,
      ...(baseUrl ? { baseUrl } : {}),
      env,
    });
    return "updated";
  }
  await updateCredential(db, ring, {
    id: existing.id,
    userId,
    key: apiKey,
    baseUrl,
    env,
  });
  return "updated";
}
