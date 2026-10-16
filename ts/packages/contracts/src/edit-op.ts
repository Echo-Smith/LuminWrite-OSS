import { z } from "zod";

/** 块诞生时分配，之后不随内容改变。 */
export type BlockId = `blk_${string}`;
/** sha256(normalize(text + 关键 attrs))，浏览器与 Node 同一算法。 */
export type ContentHash = `sha256:${string}`;

export const BlockIdSchema = z.string().regex(/^blk_[A-Za-z0-9_-]{8,64}$/);
export const ContentHashSchema = z.string().regex(/^sha256:[0-9a-f]{64}$/);

export const AnchorSchema = z.object({
  blockId: BlockIdSchema,
  contentHash: ContentHashSchema,
  quote: z.string().optional(),
});

export interface Anchor {
  blockId: BlockId;
  contentHash: ContentHash;
  quote?: string;
}

const reason = z.string().trim().min(1).max(500);

export const EditOpSchema = z.discriminatedUnion("op", [
  z.object({
    op: z.literal("replace_text"),
    blockId: BlockIdSchema,
    contentHash: ContentHashSchema,
    expectedOldText: z.string().min(1),
    newText: z.string().min(1),
    reason,
  }),
  z.object({
    op: z.literal("replace_block"),
    blockId: BlockIdSchema,
    contentHash: ContentHashSchema,
    expectedOldText: z.string().min(1),
    newMarkdown: z.string().min(1),
    reason,
  }),
  z.object({
    op: z.literal("insert_block"),
    afterBlockId: BlockIdSchema.nullable(),
    parentBlockId: BlockIdSchema.optional(),
    newMarkdown: z.string().min(1),
    reason,
  }),
  z.object({
    op: z.literal("move_block"),
    blockId: BlockIdSchema,
    contentHash: ContentHashSchema,
    afterBlockId: BlockIdSchema.nullable(),
    reason,
  }),
  z.object({
    op: z.literal("delete_block"),
    blockId: BlockIdSchema,
    contentHash: ContentHashSchema,
    expectedOldText: z.string().min(1),
    reason,
  }),
  z.object({
    op: z.literal("set_attr"),
    blockId: BlockIdSchema,
    contentHash: ContentHashSchema,
    attrs: z.record(z.string(), z.unknown()),
    reason,
  }),
]);

export type EditOp =
  | {
      op: "replace_text";
      blockId: BlockId;
      contentHash: ContentHash;
      expectedOldText: string;
      newText: string;
      reason: string;
    }
  | {
      op: "replace_block";
      blockId: BlockId;
      contentHash: ContentHash;
      expectedOldText: string;
      newMarkdown: string;
      reason: string;
    }
  | {
      op: "insert_block";
      afterBlockId: BlockId | null;
      parentBlockId?: BlockId;
      newMarkdown: string;
      reason: string;
    }
  | {
      op: "delete_block";
      blockId: BlockId;
      contentHash: ContentHash;
      expectedOldText: string;
      reason: string;
    }
  | {
      op: "move_block";
      blockId: BlockId;
      contentHash: ContentHash;
      afterBlockId: BlockId | null;
      reason: string;
    }
  | {
      op: "set_attr";
      blockId: BlockId;
      contentHash: ContentHash;
      attrs: Record<string, unknown>;
      reason: string;
    };

export const OpErrorCodeSchema = z.enum([
  "block_not_found",
  "hash_mismatch",
  "old_text_not_found",
  "old_text_ambiguous",
  "noop",
  "overlap",
  "schema_invalid",
  "out_of_scope",
]);

export type OpErrorCode = z.infer<typeof OpErrorCodeSchema>;

export interface OpError {
  index: number;
  code: OpErrorCode;
  message: string;
}

export function asBlockId(value: string): BlockId {
  const parsed = BlockIdSchema.safeParse(value);
  if (!parsed.success) {
    throw new Error(`非法 block id: ${value}`);
  }
  return value as BlockId;
}

export function asContentHash(value: string): ContentHash {
  const parsed = ContentHashSchema.safeParse(value);
  if (!parsed.success) {
    throw new Error(`非法 content hash: ${value}`);
  }
  return value as ContentHash;
}
