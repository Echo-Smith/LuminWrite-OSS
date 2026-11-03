import type { PGlite } from "@electric-sql/pglite";

export type Residue = "cleared" | "wal_pending";

interface WasmFs {
  readdir(path: string): string[];
  stat(path: string): { mode: number; size: number };
  isFile(mode: number): boolean;
  open(path: string, flags: string): number;
  write(fd: number, buffer: Uint8Array, offset: number, length: number, position: number): number;
  close(fd: number): void;
}

const TABLE_NAME = /^[a-z][a-z0-9_]*$/;

/**
 * 删完行之后调用。VACUUM FULL 重写表文件，CHECKPOINT 把脏页写出，
 * 再切到新的 WAL 段，把不再使用的段清零。做不到的部分返回 wal_pending，不算删除失败。
 */
export async function scrubFreedText(client: PGlite): Promise<Residue> {
  let vacuumFailed = false;
  let tables: string[] = [];
  try {
    const listed = await client.query<{ tablename: string }>(
      `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`,
    );
    tables = listed.rows.map((row) => row.tablename).filter((name) => TABLE_NAME.test(name));
  } catch {
    return "wal_pending";
  }
  for (const name of tables) {
    try {
      await client.exec(`VACUUM FULL ${name}`);
    } catch {
      vacuumFailed = true;
    }
  }
  try {
    await client.exec("CHECKPOINT");
    const before = await walFileName(client);
    await client.exec("SELECT pg_switch_wal()");
    await client.exec("CHECKPOINT");
    const after = await walFileName(client);
    if (!before || !after || before === after) return "wal_pending";
    const dir = await dataDirectory(client);
    if (!dir) return "wal_pending";
    const zeroed = zeroInactiveWal(client, `${dir}/pg_wal`, after);
    await client.syncToFs();
    if (!zeroed || vacuumFailed) return "wal_pending";
    return "cleared";
  } catch {
    return "wal_pending";
  }
}

async function walFileName(client: PGlite): Promise<string | undefined> {
  const result = await client.query<{ name: string }>(
    `SELECT pg_walfile_name(pg_current_wal_lsn()) AS name`,
  );
  const name = result.rows[0]?.name;
  return typeof name === "string" && name.length > 0 ? name : undefined;
}

async function dataDirectory(client: PGlite): Promise<string | undefined> {
  const result = await client.query<{ dir: string }>(
    `SELECT current_setting('data_directory') AS dir`,
  );
  const dir = result.rows[0]?.dir;
  return typeof dir === "string" && dir.startsWith("/") ? dir : undefined;
}

function zeroInactiveWal(client: PGlite, walDir: string, current: string): boolean {
  const fs = wasmFs(client);
  if (!fs) return false;
  let names: string[];
  try {
    names = fs.readdir(walDir);
  } catch {
    return false;
  }
  for (const name of names) {
    if (name === "." || name === ".." || name === current) continue;
    const path = `${walDir}/${name}`;
    try {
      const stat = fs.stat(path);
      if (!fs.isFile(stat.mode) || stat.size <= 0) continue;
      const zeros = new Uint8Array(stat.size);
      const fd = fs.open(path, "w+");
      try {
        fs.write(fd, zeros, 0, zeros.length, 0);
      } finally {
        fs.close(fd);
      }
    } catch {
      return false;
    }
  }
  return true;
}

function wasmFs(client: PGlite): WasmFs | undefined {
  const mod = (client as { Module?: { FS?: WasmFs } }).Module;
  const fs = mod?.FS;
  if (!fs || typeof fs.readdir !== "function" || typeof fs.write !== "function") return undefined;
  return fs;
}
