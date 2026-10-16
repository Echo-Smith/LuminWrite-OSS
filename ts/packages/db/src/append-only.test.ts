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
      await expect(
        db.exec(`update doc_versions set summary = 'x' where id = 'ver_1'`),
      ).rejects.toThrow(/append-only/);
    } finally {
      await db.close();
    }
  });

  it("清退标记才允许删版本，合并标记不能改掉来源", async () => {
    const db = new PGlite();
    try {
      await db.exec(migrationSql);
      await db.exec(`
        insert into documents (id, owner_id, title, status, saved_at, head_version_id)
        values ('doc_keep', 'usr_owner', '已保存', 'active', now(), 'ver_manual')
      `);
      await db.exec(`
        insert into doc_versions (id, document_id, seq, origin, actor_type, actor_id, content, summary)
        values ('ver_manual', 'doc_keep', 1, 'manual', 'user', 'usr_owner', '{}'::jsonb, '一版')
      `);
      await expect(
        db.exec(`
          begin;
          select set_config('lw.version_write', 'coalesce', true);
          update doc_versions set origin = 'restore' where id = 'ver_manual';
          commit;
        `),
      ).rejects.toThrow(/append-only/);
      await db.exec("rollback");
      await db.exec(`
        begin;
        select set_config('lw.version_write', 'coalesce', true);
        update doc_versions set summary = '合并' where id = 'ver_manual';
        commit;
      `);
      await db.exec(`
        begin;
        select set_config('lw.version_write', 'purge', true);
        delete from doc_versions where document_id = 'doc_keep';
        delete from documents where id = 'doc_keep';
        commit;
      `);
      const left = await db.query<{ id: string }>(`select id from documents where id = 'doc_keep'`);
      expect(left.rows).toHaveLength(0);
    } finally {
      await db.close();
    }
  });
});
