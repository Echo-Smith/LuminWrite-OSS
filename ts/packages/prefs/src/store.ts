import { compilePersonalLayer } from "./compile.js";
import { FACT_HINT, looksLikeFact, plainConflictOf } from "./inspect.js";
import {
  PREFERENCE_KINDS,
  type Preference,
  type PreferenceAuditEntry,
  type PreferenceKind,
  type PreferenceScope,
  type PreferenceStore,
  type ReviewAction,
  SCOPE_LEVELS,
} from "./types.js";

export class PreferenceInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PreferenceInputError";
  }
}

export class PreferenceNotFound extends Error {
  constructor(id: string) {
    super(`找不到写作笔记 ${id}`);
    this.name = "PreferenceNotFound";
  }
}

export class PreferenceConflict extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PreferenceConflict";
  }
}

export class FactLikeNote extends Error {
  constructor() {
    super("这像事实，不记成写作笔记");
    this.name = "FactLikeNote";
  }
}

export interface NoteSql {
  query<T>(sqlText: string, params?: unknown[]): Promise<T[]>;
  transaction<T>(fn: (query: NoteSql["query"]) => Promise<T>): Promise<T>;
  now(): Date;
  preferenceId(): string;
  auditId(): string;
}

interface PreferenceRow {
  id: string;
  user_id: string;
  kind: PreferenceKind;
  statement: string;
  scope_level: PreferenceScope["level"];
  scope_ref: string | null;
  strength: "must" | "prefer";
  status: Preference["status"];
  source: Preference["source"];
  support: number;
  contradict: number;
  plain_conflict: boolean;
  version: number;
  superseded_by: string | null;
  proposed_in_session_id: string | null;
  updated_at: Date | string;
}

interface AuditRow {
  id: string;
  preference_id: string;
  action: string;
  actor_type: string;
  actor_id: string;
  before_text: string;
  after_text: string;
  at: Date | string;
  user_id: string;
}

function stamp(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString();
}

function toPreference(row: PreferenceRow): Preference {
  return {
    id: row.id,
    userId: row.user_id,
    kind: row.kind,
    statement: row.statement,
    rule: null,
    scope: row.scope_ref
      ? { level: row.scope_level, ref: row.scope_ref }
      : { level: row.scope_level },
    strength: row.strength,
    status: row.status,
    source: row.source,
    support: row.support,
    contradict: row.contradict,
    plainConflict: row.plain_conflict,
    version: row.version,
    supersededBy: row.superseded_by,
    proposedInSessionId: row.proposed_in_session_id,
    updatedAt: stamp(row.updated_at),
  };
}

function toAudit(row: AuditRow): PreferenceAuditEntry {
  return {
    id: row.id,
    preferenceId: row.preference_id,
    action: row.action,
    actorType: row.actor_type,
    actorId: row.actor_id,
    before: row.before_text,
    after: row.after_text,
    at: stamp(row.at),
  };
}

function cleanText(text: string): string {
  const statement = text.trim();
  if (!statement || statement.length > 400) {
    throw new PreferenceInputError("笔记要写成一句不超过 400 字的话");
  }
  return statement;
}

function cleanKind(kind: string): PreferenceKind {
  if (!(PREFERENCE_KINDS as readonly string[]).includes(kind)) {
    throw new PreferenceInputError("笔记类型不合法");
  }
  return kind as PreferenceKind;
}

function cleanScope(scope: PreferenceScope): {
  level: PreferenceScope["level"];
  ref: string | null;
} {
  if (!(SCOPE_LEVELS as readonly string[]).includes(scope.level)) {
    throw new PreferenceInputError("笔记范围不合法");
  }
  if (scope.level === "global") return { level: "global", ref: null };
  const ref = scope.ref?.trim() ?? "";
  if (!ref || ref.length > 200) throw new PreferenceInputError("这个范围需要一个引用");
  return { level: scope.level, ref };
}

const SELECT_NOTE = `SELECT id, user_id, kind, statement, scope_level, scope_ref, strength, status,
  source, support, contradict, plain_conflict, version, superseded_by, proposed_in_session_id, updated_at
  FROM preferences`;

export function createPreferenceStore(sql: NoteSql): PreferenceStore {
  async function one(userId: string, id: string): Promise<PreferenceRow | undefined> {
    const rows = await sql.query<PreferenceRow>(`${SELECT_NOTE} WHERE user_id = $1 AND id = $2`, [
      userId,
      id,
    ]);
    return rows[0];
  }

  async function audit(
    query: NoteSql["query"],
    input: {
      userId: string;
      preferenceId: string;
      action: string;
      actorType: string;
      actorId: string;
      before: string;
      after: string;
    },
  ): Promise<string> {
    const id = sql.auditId();
    await query(
      `INSERT INTO preference_audit
        (id, preference_id, user_id, action, actor_type, actor_id, before_text, after_text, at)
       VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
      [
        id,
        input.preferenceId,
        input.userId,
        input.action,
        input.actorType,
        input.actorId,
        input.before,
        input.after,
        sql.now().toISOString(),
      ],
    );
    return id;
  }

  async function insert(
    query: NoteSql["query"],
    row: {
      id: string;
      userId: string;
      kind: PreferenceKind;
      statement: string;
      scopeLevel: PreferenceScope["level"];
      scopeRef: string | null;
      strength: "must" | "prefer";
      status: Preference["status"];
      source: Preference["source"];
      plainConflict: boolean;
      version: number;
      proposedInSessionId: string | null;
    },
  ): Promise<void> {
    await query(
      `INSERT INTO preferences (
        id, user_id, kind, statement, rule, scope_level, scope_ref, strength, status, source,
        support, contradict, plain_conflict, version, superseded_by, proposed_in_session_id, updated_at
      ) VALUES (
        $1, $2, $3, $4, NULL, $5, $6, $7, $8, $9,
        0, 0, $10, $11, NULL, $12, $13
      )`,
      [
        row.id,
        row.userId,
        row.kind,
        row.statement,
        row.scopeLevel,
        row.scopeRef,
        row.strength,
        row.status,
        row.source,
        row.plainConflict,
        row.version,
        row.proposedInSessionId,
        sql.now().toISOString(),
      ],
    );
  }

  return {
    async list(userId, filter) {
      const rows = await sql.query<PreferenceRow>(
        `${SELECT_NOTE} WHERE user_id = $1 ORDER BY updated_at, id`,
        [userId],
      );
      const notes = rows.map(toPreference);
      const statuses = filter?.status;
      if (!statuses) return notes.filter((note) => note.status !== "superseded");
      const allowed = new Set(statuses);
      return notes.filter((note) => allowed.has(note.status));
    },

    async write(userId, input, by) {
      const statement = cleanText(input.text);
      const kind = cleanKind(input.kind);
      const scope = cleanScope(input.scope);
      const strength = input.strength ?? "prefer";
      if (strength !== "must" && strength !== "prefer") {
        throw new PreferenceInputError("笔记强度不合法");
      }
      const factHint = looksLikeFact(statement);
      const id = sql.preferenceId();
      await sql.transaction(async (query) => {
        await insert(query, {
          id,
          userId,
          kind,
          statement,
          scopeLevel: scope.level,
          scopeRef: scope.ref,
          strength,
          status: "active",
          source: "user_written",
          plainConflict: plainConflictOf(statement),
          version: 1,
          proposedInSessionId: null,
        });
        await audit(query, {
          userId,
          preferenceId: id,
          action: "write",
          actorType: by.type,
          actorId: by.id,
          before: "",
          after: statement,
        });
      });
      const saved = await one(userId, id);
      if (!saved) throw new PreferenceNotFound(id);
      return {
        preference: { ...toPreference(saved), ...(factHint ? { factHint } : {}) },
        factHint,
      };
    },

    async propose(userId, input) {
      const statement = cleanText(input.text);
      if (looksLikeFact(statement)) throw new FactLikeNote();
      const kind = cleanKind(input.kind);
      const scope = cleanScope(input.scope);
      const sessionId = input.sessionId.trim();
      if (!sessionId) throw new PreferenceInputError("候选笔记需要会话");
      const id = sql.preferenceId();
      await sql.transaction(async (query) => {
        await insert(query, {
          id,
          userId,
          kind,
          statement,
          scopeLevel: scope.level,
          scopeRef: scope.ref,
          strength: "prefer",
          status: "candidate",
          source: "agent_proposed",
          plainConflict: plainConflictOf(statement),
          version: 1,
          proposedInSessionId: sessionId,
        });
        await audit(query, {
          userId,
          preferenceId: id,
          action: "propose",
          actorType: "agent",
          actorId: sessionId,
          before: "",
          after: statement,
        });
      });
      const saved = await one(userId, id);
      if (!saved) throw new PreferenceNotFound(id);
      return toPreference(saved);
    },

    async review(userId, id, action, by) {
      const row = await one(userId, id);
      if (!row || row.status === "superseded") throw new PreferenceNotFound(id);
      const next = nextStatus(row.status, action);
      await sql.transaction(async (query) => {
        await query(
          `UPDATE preferences SET status = $1, updated_at = $2 WHERE id = $3 AND user_id = $4`,
          [next, sql.now().toISOString(), id, userId],
        );
        await audit(query, {
          userId,
          preferenceId: id,
          action,
          actorType: by.type,
          actorId: by.id,
          before: "",
          after: "",
        });
      });
      const saved = await one(userId, id);
      if (!saved) throw new PreferenceNotFound(id);
      return toPreference(saved);
    },

    async correct(userId, id, expectedVersion, newText, by) {
      const row = await one(userId, id);
      if (!row || row.status === "superseded" || row.status === "rejected") {
        throw new PreferenceNotFound(id);
      }
      if (row.version !== expectedVersion) throw new PreferenceConflict("笔记版本已经变了");
      const statement = cleanText(newText);
      const factHint = looksLikeFact(statement);
      const nextId = sql.preferenceId();
      await sql.transaction(async (query) => {
        await insert(query, {
          id: nextId,
          userId,
          kind: row.kind,
          statement,
          scopeLevel: row.scope_level,
          scopeRef: row.scope_ref,
          strength: row.strength,
          status: "active",
          source: "user_written",
          plainConflict: plainConflictOf(statement),
          version: row.version + 1,
          proposedInSessionId: null,
        });
        await query(
          `UPDATE preferences
           SET status = 'superseded', superseded_by = $1, updated_at = $2
           WHERE id = $3 AND user_id = $4`,
          [nextId, sql.now().toISOString(), id, userId],
        );
        await audit(query, {
          userId,
          preferenceId: id,
          action: "correct",
          actorType: by.type,
          actorId: by.id,
          before: row.statement,
          after: statement,
        });
        await audit(query, {
          userId,
          preferenceId: nextId,
          action: "write",
          actorType: by.type,
          actorId: by.id,
          before: "",
          after: statement,
        });
      });
      const saved = await one(userId, nextId);
      if (!saved) throw new PreferenceNotFound(nextId);
      return { ...toPreference(saved), ...(factHint ? { factHint } : {}) };
    },

    async forget(userId, id, by) {
      const row = await one(userId, id);
      if (!row) throw new PreferenceNotFound(id);
      const receiptId = await sql.transaction(async (query) => {
        await query(
          `UPDATE preference_audit SET before_text = ''
           WHERE preference_id = $1 OR before_text = $2`,
          [id, row.statement],
        );
        await query(
          `UPDATE preference_audit SET after_text = ''
           WHERE preference_id = $1 OR after_text = $2`,
          [id, row.statement],
        );
        const receipt = await audit(query, {
          userId,
          preferenceId: id,
          action: "forget",
          actorType: by.type,
          actorId: by.id,
          before: "",
          after: "",
        });
        await query(`DELETE FROM preferences WHERE id = $1 AND user_id = $2`, [id, userId]);
        return receipt;
      });
      return { status: "verified" as const, receiptId };
    },

    async explain(userId, id) {
      const audits = await sql.query<AuditRow>(
        `SELECT id, preference_id, user_id, action, actor_type, actor_id, before_text, after_text, at
         FROM preference_audit WHERE preference_id = $1 AND user_id = $2 ORDER BY at, id`,
        [id, userId],
      );
      if (audits.length === 0) throw new PreferenceNotFound(id);
      return audits.map(toAudit);
    },

    async compileOverlay(userId, ctx) {
      const rows = await sql.query<PreferenceRow>(`${SELECT_NOTE} WHERE user_id = $1`, [userId]);
      return compilePersonalLayer(userId, rows.map(toPreference), ctx);
    },
  };
}

function nextStatus(status: Preference["status"], action: ReviewAction): Preference["status"] {
  if (action === "accept" && status === "candidate") return "active";
  if (action === "reject" && status === "candidate") return "rejected";
  if (action === "mute" && status === "active") return "muted";
  if (action === "unmute" && status === "muted") return "active";
  throw new PreferenceConflict("这个笔记现在不能这样处理");
}

export const factHintText = FACT_HINT;
