import { DatabaseSync } from "node:sqlite";

/**
 * pi-durable 1.3.0 的 SQLite 布局（schema version 1，STRICT）。
 * Storage / Tx 只有追加和 fork，没有 deleteConversation。
 *
 * - durable_schema：schema 版本
 * - durable_metadata：next_id、next_seq。id 只增不减，清退不重置
 * - record_ids：id → conversation | entry | task | submission | document
 * - conversations：id、owner_conversation_id、owner_task_id、record JSON
 *   （record 里有 parent: { conversationId, at } 时，读历史会走父对话的 entries）
 * - entries：id、conversation_id、head、commit_seq、record JSON。用户消息和模型回复在这里
 * - tasks：id、conversation_id、kind、status、abort_requested、background、record JSON
 * - submissions：id、conversation_id、request_id、status、record JSON
 * - documents：id、kind、family、key_value、scope_kind（session | conversation | task）、
 *   owner_id、created_at、retired_at、record JSON
 * - document_revisions：document_id、seq、kind（base | delta）、version、content JSON
 *
 * fork 会把当时的 conversation 文档复制到子对话，但 entries 仍留在父对话。
 * 子对话的 record.parent 继续指向父对话。删掉父对话的行之后，若子对话还指着它，
 * pi 读历史会落到不存在的对话上。
 *
 * 清退（进程不再使用这些对话之后调用；测试里先 Harness.close）：
 * 1. 目标对话，加上 owner_conversation_id 落在目标里的任务子对话，整段删除。
 * 2. 仍要保留、但 parent 指向被删对话的 fork：去掉 record.parent，留下它自己的
 *    entries 和已复制的 documents。这是 fork 之后再删旧对话。
 * 3. 删除这些对话的 entries、tasks、submissions、conversation/task 范围的 documents
 *    和 document_revisions，以及对应的 record_ids。
 * 4. 不动 durable_metadata、session 范围的 documents，也不动其他对话。
 * 5. wal_checkpoint(TRUNCATE) 之后 VACUUM，避免原文留在空闲页或 WAL。
 *    文件仍被打开的 Harness 占用时 VACUUM 会失败，行已经删掉，vacuumed 为 false。
 */
export interface PiPurgeReport {
  deletedConversationIds: string[];
  detachedForkIds: string[];
  vacuumed: boolean;
}

interface ConversationRow {
  id: number;
  owner_conversation_id: number | null;
  record: string;
}

interface IdRow {
  id: number;
}

interface DocumentRow {
  id: number;
  scope_kind: string;
  owner_id: number;
}

export function purgePiConversations(
  sqlitePath: string,
  conversationIds: readonly string[],
): PiPurgeReport {
  const requested = new Set<number>();
  for (const id of conversationIds) {
    if (!/^\d+$/.test(id)) continue;
    requested.add(Number(id));
  }
  const database = new DatabaseSync(sqlitePath);
  try {
    database.exec("PRAGMA busy_timeout = 5000");
    if (requested.size === 0)
      return { deletedConversationIds: [], detachedForkIds: [], vacuumed: false };
    const conversations = database
      .prepare("SELECT id, owner_conversation_id, record FROM conversations")
      .all() as unknown as ConversationRow[];
    const drop = new Set<number>();
    for (const id of requested) {
      if (conversations.some((row) => row.id === id)) drop.add(id);
    }
    let grew = true;
    while (grew) {
      grew = false;
      for (const row of conversations) {
        if (drop.has(row.id) || row.owner_conversation_id == null) continue;
        if (drop.has(row.owner_conversation_id)) {
          drop.add(row.id);
          grew = true;
        }
      }
    }
    const detached: number[] = [];
    const rewrite = database.prepare("UPDATE conversations SET record = ? WHERE id = ?");
    for (const row of conversations) {
      if (drop.has(row.id)) continue;
      const parent = parentId(row.record);
      if (parent == null || !drop.has(parent)) continue;
      const parsed = JSON.parse(row.record) as Record<string, unknown>;
      delete parsed.parent;
      rewrite.run(JSON.stringify(parsed), row.id);
      detached.push(row.id);
    }
    if (drop.size === 0) {
      return {
        deletedConversationIds: [],
        detachedForkIds: detached.map(String),
        vacuumed: false,
      };
    }
    const dropList = [...drop];
    const tasks = selectIds(database, "SELECT id FROM tasks WHERE conversation_id = ?", dropList);
    const entries = selectIds(
      database,
      "SELECT id FROM entries WHERE conversation_id = ?",
      dropList,
    );
    const submissions = selectIds(
      database,
      "SELECT id FROM submissions WHERE conversation_id = ?",
      dropList,
    );
    const documents = database
      .prepare("SELECT id, scope_kind, owner_id FROM documents")
      .all() as unknown as DocumentRow[];
    const taskSet = new Set(tasks);
    const documentIds = documents
      .filter((row) => {
        if (row.scope_kind === "conversation") return drop.has(row.owner_id);
        if (row.scope_kind === "task") return taskSet.has(row.owner_id);
        return false;
      })
      .map((row) => row.id);
    database.exec("BEGIN");
    try {
      deleteIds(database, "DELETE FROM document_revisions WHERE document_id = ?", documentIds);
      deleteIds(database, "DELETE FROM documents WHERE id = ?", documentIds);
      deleteIds(database, "DELETE FROM entries WHERE conversation_id = ?", dropList);
      deleteIds(database, "DELETE FROM tasks WHERE conversation_id = ?", dropList);
      deleteIds(database, "DELETE FROM submissions WHERE conversation_id = ?", dropList);
      deleteIds(database, "DELETE FROM conversations WHERE id = ?", dropList);
      deleteRecordIds(database, "conversation", dropList);
      deleteRecordIds(database, "entry", entries);
      deleteRecordIds(database, "task", tasks);
      deleteRecordIds(database, "submission", submissions);
      deleteRecordIds(database, "document", documentIds);
      database.exec("COMMIT");
    } catch (error) {
      database.exec("ROLLBACK");
      throw error;
    }
    let vacuumed = false;
    try {
      database.exec("PRAGMA wal_checkpoint(TRUNCATE)");
      database.exec("VACUUM");
      vacuumed = true;
    } catch {
      vacuumed = false;
    }
    return {
      deletedConversationIds: dropList.map(String),
      detachedForkIds: detached.map(String),
      vacuumed,
    };
  } finally {
    database.close();
  }
}

function parentId(record: string): number | undefined {
  try {
    const parsed = JSON.parse(record) as { parent?: { conversationId?: number } };
    const id = parsed.parent?.conversationId;
    return typeof id === "number" ? id : undefined;
  } catch {
    return undefined;
  }
}

function selectIds(database: DatabaseSync, sql: string, ids: readonly number[]): number[] {
  const statement = database.prepare(sql);
  const found: number[] = [];
  for (const id of ids) {
    for (const row of statement.all(id) as unknown as IdRow[]) found.push(row.id);
  }
  return found;
}

function deleteIds(database: DatabaseSync, sql: string, ids: readonly number[]): void {
  const statement = database.prepare(sql);
  for (const id of ids) statement.run(id);
}

function deleteRecordIds(database: DatabaseSync, recordType: string, ids: readonly number[]): void {
  const statement = database.prepare("DELETE FROM record_ids WHERE id = ? AND record_type = ?");
  for (const id of ids) statement.run(id, recordType);
}
