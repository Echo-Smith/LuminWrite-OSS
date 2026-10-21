import type { EditOp } from "@luminwrite/contracts";

/** 插入看锚点，其余看被改的块。两边都不在范围内就算越界。 */
export function targetedBlockId(op: EditOp): string | null {
  if (op.op === "insert_block") return op.afterBlockId ?? op.parentBlockId ?? null;
  return op.blockId;
}

export function outsideScope(op: EditOp, allowedIds: readonly string[]): boolean {
  const id = targetedBlockId(op);
  if (!id) return true;
  return !allowedIds.includes(id);
}
