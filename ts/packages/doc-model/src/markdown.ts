import type { BlockId } from "@luminwrite/contracts";

import { newBlockId, type PMNode } from "./tree.js";

export function markdownToBlocks(markdown: string, ids: () => BlockId = newBlockId): PMNode[] {
  const chunks = markdown
    .replace(/\r\n/g, "\n")
    .split(/\n{2,}/)
    .map((chunk) => chunk.trim())
    .filter((chunk) => chunk.length > 0);
  return chunks.map((chunk) => {
    const heading = /^(#{1,6})\s+([\s\S]+)$/.exec(chunk);
    if (heading?.[1] && heading[2]) {
      return {
        type: "heading",
        attrs: { id: ids(), level: heading[1].length },
        content: [{ type: "text", text: heading[2].trim() }],
      };
    }
    return {
      type: "paragraph",
      attrs: { id: ids() },
      content: [{ type: "text", text: chunk }],
    };
  });
}

export function blocksToMarkdown(nodes: PMNode[]): string {
  return nodes
    .map((node) => {
      const text = (node.content ?? []).map((child) => child.text ?? "").join("");
      if (node.type === "heading") {
        const level = typeof node.attrs?.level === "number" ? node.attrs.level : 1;
        return `${"#".repeat(level)} ${text}`;
      }
      return text;
    })
    .join("\n\n");
}
