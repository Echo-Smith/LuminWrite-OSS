import type { EditOp } from "@luminwrite/contracts";
import { analyzePlain, type PlainReport } from "@luminwrite/plainspeak";

export function textOfOps(ops: EditOp[]): string {
  const parts: string[] = [];
  for (const op of ops) {
    if (op.op === "replace_text") parts.push(op.newText);
    if (op.op === "replace_block" || op.op === "insert_block") parts.push(op.newMarkdown);
  }
  return parts.join("\n");
}

export function screenOps(ops: EditOp[]): PlainReport {
  return analyzePlain(textOfOps(ops));
}
