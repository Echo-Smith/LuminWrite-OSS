import { homedir } from "node:os";
import { join } from "node:path";

export interface DatabaseLocation {
  kind: "pglite" | "postgres";
  /** 令牌、Pi 的 SQLite、以及 PGlite 数据目录的父目录。 */
  dataDir: string;
  /** 只有 kind 为 postgres 时有值。 */
  databaseUrl?: string;
}

/**
 * 默认是本机 PGlite。只有显式设置 `LW_DATABASE_URL` 才连正式 Postgres。
 * 不读 `DATABASE_URL`：那常是别的项目留在环境里的连接串。
 */
export function resolveDatabaseLocation(env: NodeJS.ProcessEnv = process.env): DatabaseLocation {
  const dataDir = env.LW_DATA_DIR?.trim() || join(homedir(), ".luminwrite");
  const databaseUrl = env.LW_DATABASE_URL?.trim() || "";
  if (databaseUrl) return { kind: "postgres", dataDir, databaseUrl };
  return { kind: "pglite", dataDir };
}

export function pgliteDataDir(dataDir: string): string {
  return join(dataDir, "pglite");
}
