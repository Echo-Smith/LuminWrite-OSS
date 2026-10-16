import type { BlockId, ContentHash, EditOp, TurnKind } from "@luminwrite/contracts";

export interface PlanBlock {
  blockId: BlockId;
  contentHash: ContentHash;
  text: string;
}

export const OUTLINE_PARAGRAPHS = [
  "推理成本先看三件事：显卡时租、显存占用、批大小。",
  "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。",
  "建议先量出当前批大小和每小时费用，再决定加机器还是调批大小。",
] as const;

export const SECTION_PARAGRAPHS = [
  "推理成本先看三件事：显卡时租、显存占用、批大小。一台 A10 按小时计费。",
  "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。再往上提，排队时间会先变长。",
  "第三段先记下当前每小时费用。费用下降不明显就先调批大小。",
  "第四段只记录显存占用，不改机器数量。这一段在分叉之后仍然逐字相同。",
  "第五段建议先量出当前批大小。量完再决定加机器还是调批大小。",
] as const;

export const DRAFT_PARAGRAPHS = [
  "推理成本先看三件事：显卡时租、显存占用、批大小。一台 A10 按小时计费，显存决定能放多大的批。",
  "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。再往上提，排队时间会先变长。",
  "建议先量出当前批大小和每小时费用，再决定加机器还是调批大小。费用下降不明显就先调批大小。",
] as const;

export type Plan = { ok: true; ops: EditOp[]; summary: string } | { ok: false; reason: string };

function insertParagraphs(
  paragraphs: readonly string[],
  afterBlockId: BlockId | null,
  reason: string,
): EditOp {
  return {
    op: "insert_block",
    afterBlockId,
    newMarkdown: paragraphs.join("\n\n"),
    reason,
  };
}

function shorten(text: string): string {
  const sentence = text.split("。")[0];
  const next = sentence ? `${sentence}。` : text;
  if (next !== text) return next;
  return `${text.slice(0, Math.max(1, text.length - 1))}。`;
}

export function targetIndex(
  instruction: string,
  blocks: PlanBlock[],
  selection?: Array<{ blockId: string }>,
): number {
  const selected = selection?.[0]?.blockId;
  if (selected) {
    const found = blocks.findIndex((block) => block.blockId === selected);
    if (found >= 0) return found;
  }
  const match = /第\s*([0-9]+)\s*段/.exec(instruction);
  if (match?.[1]) return Number(match[1]) - 1;
  return 0;
}

export function planOps(input: {
  kind: TurnKind;
  instruction: string;
  blocks: PlanBlock[];
  selection?: Array<{ blockId: string }>;
}): Plan {
  if (input.kind === "research" || input.kind === "review") {
    return { ok: false, reason: "尚未实现" };
  }
  if (input.kind === "outline") {
    const after = input.blocks.at(-1)?.blockId ?? null;
    return {
      ok: true,
      summary: "大纲",
      ops: [insertParagraphs(OUTLINE_PARAGRAPHS, after, "写大纲")],
    };
  }
  if (input.kind === "draft") {
    if (input.blocks.length === 0) {
      return {
        ok: true,
        summary: "初稿",
        ops: [insertParagraphs(DRAFT_PARAGRAPHS, null, "写初稿")],
      };
    }
    const ops: EditOp[] = [];
    input.blocks.forEach((block, index) => {
      const next = DRAFT_PARAGRAPHS[index] ?? `${block.text}这一段补上当前能核对的数字。`;
      if (next === block.text) return;
      ops.push({
        op: "replace_text",
        blockId: block.blockId,
        contentHash: block.contentHash,
        expectedOldText: block.text,
        newText: next,
        reason: "写成初稿",
      });
    });
    return { ok: true, summary: "初稿", ops };
  }
  const index = targetIndex(input.instruction, input.blocks, input.selection);
  const block = input.blocks[index];
  if (!block) return { ok: false, reason: "没有可修订的段落" };
  const next = shorten(block.text);
  if (next === block.text) return { ok: false, reason: "这段没有可收紧的文本" };
  return {
    ok: true,
    summary: "收紧指定段落",
    ops: [
      {
        op: "replace_text",
        blockId: block.blockId,
        contentHash: block.contentHash,
        expectedOldText: block.text,
        newText: next,
        reason: "只改这一段",
      },
    ],
  };
}
