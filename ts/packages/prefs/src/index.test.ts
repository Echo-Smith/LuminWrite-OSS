import { describe, expect, it } from "vitest";

import { compilePersonalLayer } from "./compile.js";
import { looksLikeFact, plainConflictOf } from "./inspect.js";
import { createPreferenceStore, FactLikeNote, type NoteSql, PreferenceConflict } from "./store.js";
import type { Preference } from "./types.js";

const user = { type: "user", id: "user_1" };

function memorySql(): NoteSql & { notes: Map<string, Record<string, unknown>> } {
  const notes = new Map<string, Record<string, unknown>>();
  const audits: Array<Record<string, unknown>> = [];
  let seq = 0;
  const now = () => new Date("2026-04-01T00:00:00.000Z");
  const query: NoteSql["query"] = async (sqlText, params = []) => {
    if (sqlText.startsWith("SELECT id, user_id, kind")) {
      const userId = String(params[0]);
      const id = params[1] ? String(params[1]) : undefined;
      return [...notes.values()].filter(
        (row) => row.user_id === userId && (id === undefined || row.id === id),
      ) as never;
    }
    if (sqlText.startsWith("SELECT id, preference_id")) {
      return audits.filter(
        (row) => row.preference_id === params[0] && row.user_id === params[1],
      ) as never;
    }
    return [];
  };
  return {
    notes,
    query,
    async transaction(fn) {
      return fn(async (sqlText, params = []) => {
        if (sqlText.startsWith("INSERT INTO preferences")) {
          const id = String(params[0]);
          notes.set(id, {
            id,
            user_id: params[1],
            kind: params[2],
            statement: params[3],
            scope_level: params[4],
            scope_ref: params[5],
            strength: params[6],
            status: params[7],
            source: params[8],
            support: 0,
            contradict: 0,
            plain_conflict: params[9],
            version: params[10],
            superseded_by: null,
            proposed_in_session_id: params[11],
            updated_at: params[12],
          });
          return [];
        }
        if (sqlText.startsWith("INSERT INTO preference_audit")) {
          audits.push({
            id: params[0],
            preference_id: params[1],
            user_id: params[2],
            action: params[3],
            actor_type: params[4],
            actor_id: params[5],
            before_text: params[6],
            after_text: params[7],
            at: params[8],
          });
          return [];
        }
        if (sqlText.startsWith("UPDATE preferences SET status = $1")) {
          const row = notes.get(String(params[2]));
          if (row && row.user_id === params[3]) {
            row.status = params[0];
            row.updated_at = params[1];
          }
          return [];
        }
        if (sqlText.startsWith("UPDATE preferences")) {
          const row = notes.get(String(params[2]));
          if (row && row.user_id === params[3]) {
            row.status = "superseded";
            row.superseded_by = params[0];
            row.updated_at = params[1];
          }
          return [];
        }
        if (sqlText.includes("SET before_text")) {
          for (const row of audits) {
            if (row.preference_id === params[0] || row.before_text === params[1])
              row.before_text = "";
          }
          return [];
        }
        if (sqlText.includes("SET after_text")) {
          for (const row of audits) {
            if (row.preference_id === params[0] || row.after_text === params[1])
              row.after_text = "";
          }
          return [];
        }
        if (sqlText.startsWith("DELETE FROM preferences")) {
          const row = notes.get(String(params[0]));
          if (row && row.user_id === params[1]) notes.delete(String(params[0]));
          return [];
        }
        return query(sqlText, params);
      });
    },
    now,
    preferenceId: () => `prf_${++seq}`,
    auditId: () => `aud_${++seq}`,
  };
}

describe("写作笔记", () => {
  it("候选和说人话冲突不进个人层，范围内的 active 才进去", () => {
    const notes: Preference[] = [
      note({ id: "a", status: "candidate", statement: "候选先别用。" }),
      note({ id: "b", status: "active", statement: "赋能不要出现。", plainConflict: true }),
      note({ id: "c", status: "active", statement: "先写结论，再写依据。", version: 3 }),
      note({
        id: "d",
        status: "active",
        statement: "评论体裁用短句。",
        scope: { level: "genre", ref: "评论" },
      }),
      note({ id: "e", status: "superseded", statement: "旧措辞。" }),
    ];
    const layer = compilePersonalLayer("user_1", notes, { genre: "评论" });
    expect(layer.origin).toBe("personal");
    expect(layer.ref).toBe("prefs:user_1@v3");
    expect(layer.rules).toEqual([]);
    expect(layer.prose).toBe("先写结论，再写依据。\n评论体裁用短句。");
    expect(layer.prose).not.toContain("候选");
    expect(layer.prose).not.toContain("赋能");
    expect(plainConflictOf("赋能不要出现。")).toBe(true);
    expect(plainConflictOf("先写结论，再写依据。")).toBe(false);
    expect(looksLikeFact("控制在 30% 以内")).toBe(true);
    expect(looksLikeFact("先写结论，再写依据。")).toBe(false);
  });

  it("改措辞新建一条并关闭旧条；事实候选不生成；删除后原文只剩空记录", async () => {
    const sql = memorySql();
    const store = createPreferenceStore(sql);
    const written = await store.write(
      "user_1",
      { text: "先写结论，再写依据。", kind: "habit", scope: { level: "global" } },
      user,
    );
    expect(written.factHint).toBe(false);
    expect(written.preference.status).toBe("active");
    const hinted = await store.write(
      "user_1",
      { text: "开篇不要写 2020 年。", kind: "taboo", scope: { level: "global" } },
      user,
    );
    expect(hinted.factHint).toBe(true);
    expect(hinted.preference.status).toBe("active");
    await expect(
      store.propose("user_1", {
        text: "某大学的稿子要短。",
        kind: "tone",
        scope: { level: "global" },
        sessionId: "ses_1",
      }),
    ).rejects.toBeInstanceOf(FactLikeNote);
    const candidate = await store.propose("user_1", {
      text: "段尾不要再总结一遍。",
      kind: "structure",
      scope: { level: "global" },
      sessionId: "ses_1",
    });
    expect(candidate.status).toBe("candidate");
    const overlay = await store.compileOverlay("user_1", {});
    expect(overlay.prose).toContain("先写结论");
    expect(overlay.prose).not.toContain("段尾不要");
    await expect(
      store.correct("user_1", written.preference.id, 2, "先写结论。", user),
    ).rejects.toBeInstanceOf(PreferenceConflict);
    const corrected = await store.correct("user_1", written.preference.id, 1, "先写结论。", user);
    expect(corrected.id).not.toBe(written.preference.id);
    expect(corrected.statement).toBe("先写结论。");
    expect(corrected.version).toBe(2);
    const listed = await store.list("user_1");
    expect(listed.map((item) => item.id)).not.toContain(written.preference.id);
    const closed = sql.notes.get(written.preference.id);
    expect(closed?.status).toBe("superseded");
    expect(closed?.superseded_by).toBe(corrected.id);
    const forgotten = await store.forget("user_1", corrected.id, user);
    expect(forgotten.status).toBe("verified");
    expect(sql.notes.has(corrected.id)).toBe(false);
    const trail = await store.explain("user_1", corrected.id);
    expect(trail.some((item) => item.action === "forget")).toBe(true);
    expect(JSON.stringify(trail)).not.toContain("先写结论。");
    const oldTrail = await store.explain("user_1", written.preference.id);
    expect(oldTrail.every((item) => item.after !== "先写结论。")).toBe(true);
  });
});

function note(
  patch: Partial<Preference> & Pick<Preference, "id" | "status" | "statement">,
): Preference {
  return {
    userId: "user_1",
    kind: "habit",
    rule: null,
    scope: { level: "global" },
    strength: "prefer",
    source: "user_written",
    support: 0,
    contradict: 0,
    plainConflict: false,
    version: 1,
    supersededBy: null,
    proposedInSessionId: null,
    updatedAt: "2026-04-01T00:00:00.000Z",
    ...patch,
  };
}
