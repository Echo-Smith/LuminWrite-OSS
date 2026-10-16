import type { BlockId, EditOp } from "@luminwrite/contracts";
import fc from "fast-check";
import { describe, expect, it } from "vitest";
import type { PMNode } from "./index.js";
import {
  applyOps,
  blockHash,
  blockHashSync,
  blockText,
  diffBlocks,
  docFromParagraphs,
  emptyDoc,
  listBlocks,
  manualOpsFromSteps,
  mergeBlocks,
  rebaseOp,
  remapBlockIds,
  sha256Text,
  splitBlock,
} from "./index.js";

const sample = [
  "推理成本主要由显卡时租决定。",
  "批大小提高后，单次请求更便宜。",
  "先量出当前费用再加机器。",
];

function paragraph(doc: ReturnType<typeof docFromParagraphs>, index: number): PMNode {
  const node = listBlocks(doc)[index];
  if (!node) throw new Error(`missing block ${index}`);
  return node;
}

function replaceParagraph(
  doc: ReturnType<typeof docFromParagraphs>,
  index: number,
  newText: string,
): EditOp {
  const node = paragraph(doc, index);
  const blockId = node.attrs?.id;
  if (typeof blockId !== "string") throw new Error("missing id");
  return {
    op: "replace_text",
    blockId: blockId as BlockId,
    contentHash: blockHashSync(node),
    expectedOldText: blockText(node),
    newText,
    reason: "测试",
  };
}

describe("sha256 与块哈希", () => {
  it("对齐公开测试向量", () => {
    expect(sha256Text("")).toBe("e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
    expect(sha256Text("abc")).toBe(
      "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    );
  });

  it("与 WebCrypto 同一摘要", async () => {
    const payload = "你好\n{}";
    const subtle = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(payload));
    const hex = [...new Uint8Array(subtle)]
      .map((byte) => byte.toString(16).padStart(2, "0"))
      .join("");
    expect(sha256Text(payload)).toBe(hex);
    expect(sha256Text(payload)).toBe(
      "b83bde06b0bf15a594867a6cd61eaa0bcec4b2afe4390208dc35f27072b46ca1",
    );
  });

  it("换行归一，id 不进哈希", async () => {
    const doc = docFromParagraphs(["甲\r\n乙"]);
    const node = paragraph(doc, 0);
    const hash = await blockHash(node);
    expect(hash).toBe(
      blockHashSync({ ...node, attrs: { ...node.attrs, id: "blk_othervalue0001" } }),
    );
    expect(blockHashSync(node)).toBe(
      blockHashSync({
        type: "paragraph",
        attrs: { level: undefined },
        content: [{ type: "text", text: "甲\n乙" }],
      }),
    );
  });

  it("标题 level 改变哈希", () => {
    const heading = {
      type: "heading",
      attrs: { id: "blk_headinglevel0001", level: 1 },
      content: [{ type: "text", text: "标题" }],
    };
    const deeper = { ...heading, attrs: { ...heading.attrs, level: 2 } };
    expect(blockHashSync(heading)).not.toBe(blockHashSync(deeper));
  });
});

describe("applyOps", () => {
  it("只改目标段，其他段逐字节相同", () => {
    const doc = docFromParagraphs(sample);
    const before = listBlocks(doc).map(blockText);
    const applied = applyOps(doc, [replaceParagraph(doc, 1, "批大小提高后更便宜。")]);
    expect(applied.ok).toBe(true);
    if (!applied.ok) return;
    const after = listBlocks(applied.doc).map(blockText);
    expect(after[1]).toBe("批大小提高后更便宜。");
    expect(after[0]).toBe(before[0]);
    expect(after[2]).toBe(before[2]);
    expect(listBlocks(applied.doc).map((node) => node.attrs?.id)).toEqual(
      listBlocks(doc).map((node) => node.attrs?.id),
    );
    expect(JSON.stringify(doc)).toBe(
      JSON.stringify(
        docFromParagraphs(
          sample,
          listBlocks(doc).map((node) => node.attrs?.id as BlockId),
        ),
      ),
    );
  });

  it("不同块的并发修改互不冲突，过期修改被拒绝", () => {
    const doc = docFromParagraphs(sample);
    const op0 = replaceParagraph(doc, 0, "成本看时租。");
    const op1 = replaceParagraph(doc, 1, "批大小决定单价。");
    const both = applyOps(doc, [op0, op1]);
    expect(both.ok).toBe(true);
    const first = applyOps(doc, [op0]);
    expect(first.ok).toBe(true);
    if (!first.ok) return;
    const second = applyOps(first.doc, [op1]);
    expect(second.ok).toBe(true);
    if (!second.ok) return;
    expect(blockText(listBlocks(second.doc)[2]!)).toBe(sample[2]);
    const stale = applyOps(first.doc, [op0]);
    expect(stale.ok).toBe(false);
    if (stale.ok) return;
    expect(stale.errors[0]?.code).toBe("hash_mismatch");
  });

  it("原文缺失、歧义、空操作、重叠都有明确错误码", () => {
    const doc = docFromParagraphs(["啊啊成本啊啊", "另一段。"]);
    const node = paragraph(doc, 0);
    const blockId = node.attrs?.id as BlockId;
    const contentHash = blockHashSync(node);
    const missing = applyOps(doc, [
      {
        op: "replace_text",
        blockId,
        contentHash,
        expectedOldText: "不存在",
        newText: "新的",
        reason: "测试",
      },
    ]);
    expect(missing.ok).toBe(false);
    if (!missing.ok) expect(missing.errors[0]?.code).toBe("old_text_not_found");

    const ambiguous = applyOps(doc, [
      {
        op: "replace_text",
        blockId,
        contentHash,
        expectedOldText: "啊",
        newText: "呀",
        reason: "测试",
      },
    ]);
    expect(ambiguous.ok).toBe(false);
    if (!ambiguous.ok) expect(ambiguous.errors[0]?.code).toBe("old_text_ambiguous");

    const noop = applyOps(doc, [
      {
        op: "replace_text",
        blockId,
        contentHash,
        expectedOldText: "成本",
        newText: "成本",
        reason: "测试",
      },
    ]);
    expect(noop.ok).toBe(false);
    if (!noop.ok) expect(noop.errors[0]?.code).toBe("noop");

    const overlap = applyOps(doc, [
      replaceParagraph(doc, 0, "一。"),
      replaceParagraph(doc, 0, "二。"),
    ]);
    expect(overlap.ok).toBe(false);
    if (!overlap.ok) expect(overlap.errors[0]?.code).toBe("overlap");
    expect(blockText(paragraph(doc, 0))).toBe("啊啊成本啊啊");
  });

  it("范围外的块不能改", () => {
    const doc = docFromParagraphs(sample);
    const op = replaceParagraph(doc, 2, "不该改。");
    const scoped = applyOps(doc, [op], {
      scope: [paragraph(doc, 0).attrs?.id as BlockId],
    });
    expect(scoped.ok).toBe(false);
    if (!scoped.ok) expect(scoped.errors[0]?.code).toBe("out_of_scope");
  });

  it("原文仍唯一出现时可以 rebase，否则返回 null", () => {
    const doc = docFromParagraphs(["推理成本下降。"]);
    const op = replaceParagraph(doc, 0, "推理开销下降。");
    if (op.op !== "replace_text") throw new Error("op");
    const edited = applyOps(doc, [
      {
        ...op,
        newText: "推理成本明显下降。",
      },
    ]);
    expect(edited.ok).toBe(true);
    if (!edited.ok) return;
    const current = paragraph(edited.doc, 0);
    const rebased = rebaseOp({ ...op, expectedOldText: "成本" }, current);
    expect(rebased?.op).toBe("replace_text");
    if (rebased?.op === "replace_text") expect(rebased.contentHash).toBe(blockHashSync(current));
    expect(rebaseOp({ ...op, expectedOldText: "时租" }, current)).toBeNull();
  });

  it("删除、插入、移动和回放手工 op", () => {
    const doc = docFromParagraphs(sample);
    const removed = applyOps(doc, [
      {
        op: "delete_block",
        blockId: paragraph(doc, 1).attrs?.id as BlockId,
        contentHash: blockHashSync(paragraph(doc, 1)),
        expectedOldText: sample[1] ?? "",
        reason: "删掉",
      },
    ]);
    expect(removed.ok).toBe(true);
    if (!removed.ok) return;
    expect(listBlocks(removed.doc)).toHaveLength(2);

    const inserted = applyOps(removed.doc, [
      {
        op: "insert_block",
        afterBlockId: paragraph(removed.doc, 0).attrs?.id as BlockId,
        newMarkdown: "补上的一段。",
        reason: "插入",
      },
    ]);
    expect(inserted.ok).toBe(true);
    if (!inserted.ok) return;
    expect(blockText(listBlocks(inserted.doc)[1]!)).toBe("补上的一段。");

    const moved = applyOps(doc, [
      {
        op: "move_block",
        blockId: paragraph(doc, 2).attrs?.id as BlockId,
        contentHash: blockHashSync(paragraph(doc, 2)),
        afterBlockId: null,
        reason: "挪到开头",
      },
    ]);
    expect(moved.ok).toBe(true);
    if (!moved.ok) return;
    expect(blockText(listBlocks(moved.doc)[0]!)).toBe(sample[2]);

    const changed = applyOps(doc, [replaceParagraph(doc, 0, "改过的第一段。")]);
    expect(changed.ok).toBe(true);
    if (!changed.ok) return;
    const ops = manualOpsFromSteps(doc, changed.doc);
    expect(ops).toHaveLength(1);
    const replay = applyOps(doc, ops);
    expect(replay.ok).toBe(true);
    if (!replay.ok) return;
    expect(listBlocks(replay.doc).map(blockText)).toEqual(listBlocks(changed.doc).map(blockText));
    const diffs = diffBlocks(doc, changed.doc);
    expect(diffs.filter((diff) => diff.kind === "modified")).toHaveLength(1);
    expect(diffs.filter((diff) => diff.kind === "unchanged")).toHaveLength(2);
  });

  it("拆段保留前半 id，合段保留前一块 id", () => {
    const doc = docFromParagraphs(["前半后半"]);
    const id = paragraph(doc, 0).attrs?.id as BlockId;
    const split = splitBlock(doc, id, 2);
    expect(split.ok).toBe(true);
    if (!split.ok) return;
    const blocks = listBlocks(split.doc);
    expect(blocks[0]?.attrs?.id).toBe(id);
    expect(blockText(blocks[0]!)).toBe("前半");
    expect(blockText(blocks[1]!)).toBe("后半");
    const second = blocks[1]?.attrs?.id as BlockId;
    const merged = mergeBlocks(split.doc, id, second);
    expect(merged.ok).toBe(true);
    if (!merged.ok) return;
    expect(listBlocks(merged.doc)).toHaveLength(1);
    expect(listBlocks(merged.doc)[0]?.attrs?.id).toBe(id);
    expect(blockText(listBlocks(merged.doc)[0]!)).toBe("前半后半");
  });

  it("空文档可以插入", () => {
    const applied = applyOps(emptyDoc(), [
      {
        op: "insert_block",
        afterBlockId: null,
        newMarkdown: "第一段。\n\n第二段。",
        reason: "起稿",
      },
    ]);
    expect(applied.ok).toBe(true);
    if (!applied.ok) return;
    expect(listBlocks(applied.doc).map(blockText)).toEqual(["第一段。", "第二段。"]);
  });
});

describe("属性：锚点与冲突", () => {
  const chars = "推理成本下降了批大小0123456789。".split("");

  it("改一块不影响其他块的文本和哈希，旧锚点会被拒绝", () => {
    fc.assert(
      fc.property(
        fc.array(fc.string({ unit: fc.constantFrom(...chars), minLength: 1, maxLength: 12 }), {
          minLength: 2,
          maxLength: 6,
        }),
        fc.integer({ min: 0, max: 5 }),
        fc.integer({ min: 0, max: 5 }),
        (texts, ia, ib) => {
          const doc = docFromParagraphs(texts);
          const blocks = listBlocks(doc);
          const i = ia % blocks.length;
          const j = ib % blocks.length;
          const before = blocks.map((node) => ({
            id: node.attrs?.id,
            text: blockText(node),
            hash: blockHashSync(node),
          }));
          const op = replaceParagraph(doc, i, `${before[i]?.text ?? ""}补。`);
          const applied = applyOps(doc, [op]);
          expect(applied.ok).toBe(true);
          if (!applied.ok) return;
          const after = listBlocks(applied.doc);
          for (let n = 0; n < before.length; n += 1) {
            if (n === i) {
              expect(blockHashSync(after[n]!)).not.toBe(before[n]?.hash);
              continue;
            }
            expect(blockText(after[n]!)).toBe(before[n]?.text);
            expect(blockHashSync(after[n]!)).toBe(before[n]?.hash);
            expect(after[n]?.attrs?.id).toBe(before[n]?.id);
          }
          if (i !== j) {
            const other = replaceParagraph(doc, j, `${before[j]?.text ?? ""}另。`);
            const concurrent = applyOps(applied.doc, [other]);
            expect(concurrent.ok).toBe(true);
          }
          const stale = applyOps(applied.doc, [op]);
          expect(stale.ok).toBe(false);
          if (!stale.ok) expect(stale.errors[0]?.code).toBe("hash_mismatch");
        },
      ),
      { numRuns: 40 },
    );
  });
});

describe("派生文档的块 id", () => {
  it("复制正文时换成新 id，相同文本的哈希不变", () => {
    const doc = docFromParagraphs(sample);
    const copied = remapBlockIds(doc);
    const before = listBlocks(doc);
    const after = listBlocks(copied);
    expect(after.map(blockText)).toEqual(before.map(blockText));
    expect(after.map(blockHashSync)).toEqual(before.map(blockHashSync));
    const ids = new Set(before.map((node) => node.attrs?.id));
    for (const node of after) expect(ids.has(node.attrs?.id)).toBe(false);
    expect(new Set(after.map((node) => node.attrs?.id)).size).toBe(after.length);
  });
});
