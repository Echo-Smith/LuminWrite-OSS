import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { crc32 } from "node:zlib";
import type { EditOp } from "@luminwrite/contracts";
import { ensureOwner, openMemoryDatabase } from "@luminwrite/db";
import { describe, expect, it } from "vitest";

import { chunkText } from "./chunk.js";
import { createFakeEmbedder, createOpenAiEmbedder, EMBED_DIM } from "./embed.js";
import { createKnowledgeBase, type KnowledgeBase } from "./kb.js";
import { parseUpload } from "./parse.js";
import { rrfMerge } from "./rrf.js";

const names = [
  "麒麟",
  "貔貅",
  "饕餮",
  "白泽",
  "夔牛",
  "毕方",
  "应龙",
  "夫诸",
  "驺吾",
  "天吴",
  "英招",
  "开明",
  "梼杌",
  "蛊雕",
  "朱厌",
  "化蛇",
  "鸣鸢",
  "赤鱬",
  "文鳐",
  "当康",
  "耳鼠",
  "狡犬",
];

function sentence(name: string): string {
  return `${name}在山南记下只属于这篇的句子。`;
}

function assertDisjoint(list: string[]) {
  const seen = new Map<string, string>();
  for (const name of list) {
    for (const char of name) {
      const previous = seen.get(char);
      if (previous) throw new Error(`${char} 同时出现在 ${previous} 和 ${name}`);
      seen.set(char, name);
    }
  }
}

async function seed(kb: KnowledgeBase, ownerId: string) {
  const ids: string[] = [];
  for (const name of names) {
    const uploaded = await kb.ingest({
      ownerId,
      filename: `${name}.md`,
      bytes: new TextEncoder().encode(`# ${name}\n\n${sentence(name)}`),
    });
    expect(uploaded.status).toBe("indexed");
    ids.push(uploaded.sourceId);
  }
  return ids;
}

async function expectTopFive(kb: KnowledgeBase, ownerId: string, ids: string[]) {
  expect(names.length).toBeGreaterThanOrEqual(20);
  for (let index = 0; index < names.length; index += 1) {
    const name = names[index];
    const sourceId = ids[index];
    if (!name || !sourceId) throw new Error("夹具缺行");
    const hits = await kb.search({ ownerId, text: sentence(name), limit: 5 });
    expect(hits.slice(0, 5).some((hit) => hit.sourceId === sourceId)).toBe(true);
  }
}

describe("混合检索", () => {
  it("没有嵌入时全文检索仍能命中中文夹具的前五", async () => {
    assertDisjoint(names);
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const ids = await seed(kb, "usr_owner");
      await expectTopFive(kb, "usr_owner", ids);
      expect(await kb.search({ ownerId: "usr_owner", text: "！！！", limit: 5 })).toEqual([]);
    } finally {
      await db.close();
    }
  });

  it("假嵌入走 RRF 时前五仍然命中", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db, embedder: createFakeEmbedder() });
      const ids = await seed(kb, "usr_owner");
      await expectTopFive(kb, "usr_owner", ids);
    } finally {
      await db.close();
    }
  });

  it("同一所有者重复上传返回 duplicate，另一个所有者检索不到", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const bytes = new TextEncoder().encode("沈青石只在城南卖春茶。");
      const first = await kb.ingest({ ownerId: "usr_a", filename: "tea.md", bytes });
      const again = await kb.ingest({ ownerId: "usr_a", filename: "tea-copy.md", bytes });
      expect(again).toEqual({ sourceId: first.sourceId, status: "duplicate" });
      const other = await kb.ingest({ ownerId: "usr_b", filename: "tea.md", bytes });
      expect(other.status).toBe("indexed");
      expect(other.sourceId).not.toBe(first.sourceId);
      const seenByA = await kb.search({ ownerId: "usr_a", text: "沈青石", limit: 5 });
      const seenByB = await kb.search({ ownerId: "usr_b", text: "沈青石", limit: 5 });
      expect(seenByA.map((hit) => hit.sourceId)).toEqual([first.sourceId]);
      expect(seenByB.map((hit) => hit.sourceId)).toEqual([other.sourceId]);
      await expect(kb.readSource("usr_a", other.sourceId)).rejects.toThrow("找不到来源");
    } finally {
      await db.close();
    }
  });

  it("未保存或已丢弃的文档不进检索", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-kb-doc-"));
    const db = await openMemoryDatabase();
    try {
      const owner = await ensureOwner(db, { dataDir: join(dir, "data") });
      const kb = createKnowledgeBase({ db });
      const created = await db.createDocument({ title: "手稿", ownerId: owner.userId });
      const op: EditOp = {
        op: "insert_block",
        afterBlockId: null,
        newMarkdown: "麒麟在山南记下只属于这篇的句子。",
        reason: "起稿",
      };
      const applied = await db.applyManual({
        documentId: created.id,
        ops: [op],
        actor: { type: "user", userId: owner.userId },
      });
      expect(applied.ok).toBe(true);
      expect(await kb.indexDocument(created.id)).toEqual({ status: "skipped" });
      expect(await kb.search({ ownerId: owner.userId, text: "麒麟在山南", limit: 5 })).toEqual([]);

      await db.saveDocument(created.id);
      const indexed = await kb.indexDocument(created.id);
      expect(indexed.status).toBe("indexed");
      const hits = await kb.search({
        ownerId: owner.userId,
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(hits.map((hit) => hit.sourceId)).toEqual([indexed.sourceId]);
      const hidden = await kb.search({
        ownerId: owner.userId,
        text: "麒麟在山南",
        limit: 5,
        excludeDocumentIds: [created.id],
      });
      expect(hidden).toEqual([]);

      await db.discardDocument(created.id);
      expect(await kb.search({ ownerId: owner.userId, text: "麒麟在山南", limit: 5 })).toEqual([]);
      await db.undoDiscard(created.id);
      await kb.indexDocument(created.id);
      const back = await kb.search({
        ownerId: owner.userId,
        text: "麒麟在山南",
        limit: 5,
        library: true,
      });
      expect(back.map((hit) => hit.sourceId)).toEqual([indexed.sourceId]);
    } finally {
      await db.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  it("嵌入失败时仍标记为已索引，全文还能搜到", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({
        db,
        embedder: {
          model: "fake/hash",
          dim: EMBED_DIM,
          async embed() {
            throw new Error("嵌入服务不可用");
          },
        },
      });
      const uploaded = await kb.ingest({
        ownerId: "usr_a",
        filename: "note.txt",
        bytes: new TextEncoder().encode("耳鼠只在夜里出没。"),
      });
      expect(uploaded.status).toBe("indexed");
      const rows = await db.query<{ last_error: string | null; status: string }>(
        `SELECT status, last_error FROM sources WHERE id = $1`,
        [uploaded.sourceId],
      );
      expect(rows[0]?.status).toBe("indexed");
      expect(rows[0]?.last_error).toContain("嵌入服务不可用");
      const hits = await kb.search({ ownerId: "usr_a", text: "耳鼠", limit: 5 });
      expect(hits.map((hit) => hit.sourceId)).toEqual([uploaded.sourceId]);
    } finally {
      await db.close();
    }
  });

  it("文件夹过滤只返回这一夹", async () => {
    const db = await openMemoryDatabase();
    try {
      const kb = createKnowledgeBase({ db });
      const folder = await kb.createFolder({ ownerId: "usr_a", name: "茶" });
      const inside = await kb.ingest({
        ownerId: "usr_a",
        filename: "inside.md",
        folderId: folder.id,
        bytes: new TextEncoder().encode("当康出现在东边的田里。"),
      });
      await kb.ingest({
        ownerId: "usr_a",
        filename: "outside.md",
        bytes: new TextEncoder().encode("当康出现在西边的田里。"),
      });
      const hits = await kb.search({
        ownerId: "usr_a",
        text: "当康",
        limit: 5,
        folderIds: [folder.id],
      });
      expect(hits.map((hit) => hit.sourceId)).toEqual([inside.sourceId]);
      await expect(
        kb.createFolder({ ownerId: "usr_b", name: "借", parentId: folder.id }),
      ).rejects.toThrow("找不到文件夹");
    } finally {
      await db.close();
    }
  });
});

describe("分块和融合", () => {
  it("段落重叠，超长且没有句读的段落也不会卡住", () => {
    const paragraph = (n: number) => `第${n}段${"春茶的香气留在石板路上。".repeat(12)}`;
    const body = [1, 2, 3, 4, 5, 6].map(paragraph).join("\n\n");
    const chunks = chunkText(body);
    expect(chunks.length).toBeGreaterThan(1);
    const firstLines = chunks[0]?.text.split("\n") ?? [];
    const overlap = firstLines[firstLines.length - 1];
    expect(overlap).toBeTruthy();
    expect(chunks[1]?.text.startsWith(overlap ?? "")).toBe(true);
    expect(body.slice(chunks[0]?.charStart, chunks[0]?.charEnd)).toContain(firstLines[0]);

    const headed = chunkText("# 总纲\n\n## 小节\n\n正文在这里。");
    expect(headed[0]?.headingPath).toEqual(["总纲", "小节"]);
    expect(chunkText("第一页正文。")[0]?.page).toBeNull();
    expect(chunkText("第一页正文。\f第二页正文。")[0]?.page).toBe(1);
    expect(chunkText("\f第二页正文。")[0]?.page).toBe(2);

    const huge = chunkText("啊".repeat(900));
    expect(huge).toHaveLength(1);
    expect(huge[0]?.text).toHaveLength(900);
  });

  it("只有一路结果时 RRF 保持原顺序", () => {
    expect(rrfMerge([["a", "b", "c"]]).map((item) => item.id)).toEqual(["a", "b", "c"]);
    const fused = rrfMerge([
      ["a", "b"],
      ["b", "c"],
    ]);
    expect(fused[0]?.id).toBe("b");
  });
});

describe("上传解析", () => {
  it("读 MD、TXT，拒绝别的类型", async () => {
    const md = await parseUpload({
      filename: "note.md",
      bytes: new TextEncoder().encode("# 标题\n\n正文"),
    });
    expect(md.title).toBe("标题");
    expect(md.body).toContain("正文");
    const txt = await parseUpload({
      filename: "plain.txt",
      bytes: new TextEncoder().encode("纯文本"),
    });
    expect(txt.title).toBe("plain");
    await expect(
      parseUpload({ filename: "sheet.csv", bytes: new TextEncoder().encode("a,b") }),
    ).rejects.toThrow("只接受 MD、TXT、PDF、DOCX");
  });

  it("抽出 PDF 和 DOCX 的正文", async () => {
    const pdf = await parseUpload({ filename: "note.pdf", bytes: pdfWithText("HelloKB") });
    expect(pdf.body).toContain("HelloKB");
    const docx = await parseUpload({ filename: "note.docx", bytes: docxWithText("青石正文") });
    expect(docx.body).toContain("青石正文");
  });
});

describe("OpenAI 兼容嵌入", () => {
  it("按返回的 index 排好向量，维度不对就失败", async () => {
    const seen: { url: string; authorization: string; model: string }[] = [];
    const embedder = createOpenAiEmbedder({
      model: "text-embedding-3-large",
      apiKey: "lwtest_embed_key",
      baseUrl: "http://127.0.0.1:9/v1/",
      fetchImpl: async (input, init) => {
        const body = JSON.parse(String(init?.body)) as { model: string; input: string[] };
        seen.push({
          url: String(input),
          authorization: new Headers(init?.headers).get("authorization") ?? "",
          model: body.model,
        });
        const vector = Array.from({ length: EMBED_DIM }, () => 0.01);
        return new Response(
          JSON.stringify({
            data: [
              { index: 1, embedding: vector },
              { index: 0, embedding: vector.map((value) => value + 0.01) },
            ],
          }),
        );
      },
    });
    const vectors = await embedder.embed(["甲", "乙"], { userId: "usr", requestId: "req" });
    expect(vectors).toHaveLength(2);
    expect(vectors[0]?.[0]).toBeCloseTo(0.02);
    expect(seen[0]?.url).toBe("http://127.0.0.1:9/v1/embeddings");
    expect(seen[0]?.authorization).toBe("Bearer lwtest_embed_key");
    expect(seen[0]?.model).toBe("text-embedding-3-large");

    const wrong = createOpenAiEmbedder({
      model: "m",
      apiKey: "lwtest_embed_key",
      baseUrl: "http://127.0.0.1:9/v1",
      fetchImpl: async () => new Response(JSON.stringify({ data: [{ index: 0, embedding: [1] }] })),
    });
    await expect(wrong.embed(["甲"], { userId: "usr", requestId: "req" })).rejects.toThrow(
      `嵌入维度不是 ${EMBED_DIM}`,
    );
  });
});

function pdfWithText(text: string): Uint8Array {
  const escaped = text.replace(/[()\\]/g, (char) => `\\${char}`);
  const stream = `BT /F1 24 Tf 72 120 Td (${escaped}) Tj ET`;
  const objects = [
    "1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n",
    "2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n",
    "3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj\n",
    `4 0 obj << /Length ${Buffer.byteLength(stream)} >> stream\n${stream}\nendstream endobj\n`,
    "5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n",
  ];
  let body = "%PDF-1.4\n";
  const offsets: number[] = [];
  for (const object of objects) {
    offsets.push(body.length);
    body += object;
  }
  const xref = body.length;
  let table = `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const offset of offsets) table += `${String(offset).padStart(10, "0")} 00000 n \n`;
  body += table;
  body += `trailer << /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF`;
  return new TextEncoder().encode(body);
}

function docxWithText(text: string): Uint8Array {
  const document = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>${text}</w:t></w:r></w:p></w:body>
</w:document>`;
  const types = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`;
  const rels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`;
  return zipStore([
    { name: "[Content_Types].xml", data: Buffer.from(types) },
    { name: "_rels/.rels", data: Buffer.from(rels) },
    { name: "word/document.xml", data: Buffer.from(document) },
  ]);
}

function zipStore(files: Array<{ name: string; data: Buffer }>): Uint8Array {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const file of files) {
    const name = Buffer.from(file.name);
    const checksum = crc32(file.data);
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4);
    local.writeUInt16LE(0, 8);
    local.writeUInt32LE(checksum, 14);
    local.writeUInt32LE(file.data.length, 18);
    local.writeUInt32LE(file.data.length, 22);
    local.writeUInt16LE(name.length, 26);
    const chunk = Buffer.concat([local, name, file.data]);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0, 10);
    central.writeUInt32LE(checksum, 16);
    central.writeUInt32LE(file.data.length, 20);
    central.writeUInt32LE(file.data.length, 24);
    central.writeUInt16LE(name.length, 28);
    central.writeUInt32LE(offset, 42);
    locals.push(chunk);
    centrals.push(Buffer.concat([central, name]));
    offset += chunk.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(files.length, 8);
  end.writeUInt16LE(files.length, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}
