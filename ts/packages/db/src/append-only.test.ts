import { PGlite } from "@electric-sql/pglite";
import { describe, expect, it } from "vitest";

import { migrationSql } from "./migrate.js";

describe("只追加的版本", () => {
  it("删除文档会级联到版本表，触发器拒绝这次删除", async () => {
    const db = new PGlite();
    try {
      await db.exec(migrationSql);
      await db.exec(`
        insert into documents (id, owner_id, title, status)
        values ('doc_discard', 'usr_owner', '未保存', 'draft')
      `);
      await db.exec(`
        insert into doc_versions (id, document_id, seq, origin, actor_type, actor_id, content)
        values ('ver_1', 'doc_discard', 1, 'human', 'user', 'usr_owner', '{}'::jsonb)
      `);
      await expect(db.exec(`delete from documents where id = 'doc_discard'`)).rejects.toThrow(
        /append-only/,
      );
    } finally {
      await db.close();
    }
  });
});
