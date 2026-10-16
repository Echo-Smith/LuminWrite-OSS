import { normalizeBody } from "./parse.js";

export interface TextChunk {
  text: string;
  charStart: number;
  charEnd: number;
  page: number | null;
  headingPath: string[];
}

export interface BlockChunk {
  text: string;
  charStart: number;
  charEnd: number;
  blockIds: string[];
}

const TARGET_MIN = 600;
const TARGET_MAX = 800;

interface Paragraph {
  text: string;
  start: number;
  end: number;
  page: number | null;
  headingPath: string[];
}

/**
 * 按标题和段落切开，凑到大约 600–800 字，下一块重叠上一段。
 * 正文里的换页符 `\f` 记成页码。
 */
export function chunkText(body: string): TextChunk[] {
  const paragraphs = splitParagraphs(body);
  if (paragraphs.length === 0) return [];
  const pieces = paragraphs.flatMap(splitOversized);
  const chunks: TextChunk[] = [];
  let index = 0;
  while (index < pieces.length) {
    let end = index;
    let length = 0;
    while (end < pieces.length) {
      const next = pieces[end];
      if (!next) break;
      const extra = next.text.length + (length > 0 ? 1 : 0);
      if (length >= TARGET_MIN && length + extra > TARGET_MAX) break;
      length += extra;
      end += 1;
      if (length >= 700) break;
    }
    if (end === index) end = index + 1;
    const slice = pieces.slice(index, end);
    const first = slice[0];
    const last = slice[slice.length - 1];
    if (!first || !last) break;
    chunks.push({
      text: slice.map((item) => item.text).join("\n"),
      charStart: first.start,
      charEnd: last.end,
      page: first.page,
      headingPath: last.headingPath,
    });
    if (end >= pieces.length) break;
    // 一块里只有一段时不能再回退，否则超长且无法按句切开的段落会停在原地。
    index = end - index > 1 ? end - 1 : end;
  }
  return chunks;
}

/**
 * 文档按相邻块打包。偏移是拼接正文里的位置，块本身不再切开，相邻块之间不重叠。
 */
export function chunkBlocks(blocks: { blockId: string; text: string }[]): {
  body: string;
  chunks: BlockChunk[];
} {
  const cleaned = blocks
    .map((block) => ({ blockId: block.blockId, text: block.text.replace(/\r\n/g, "\n").trim() }))
    .filter((block) => block.text.length > 0);
  const body = normalizeBody(cleaned.map((block) => block.text).join("\n\n"));
  if (!body) return { body: "", chunks: [] };
  const located: { blockId: string; start: number; end: number }[] = [];
  let cursor = 0;
  for (const block of cleaned) {
    const start = body.indexOf(block.text, cursor);
    if (start < 0) continue;
    located.push({ blockId: block.blockId, start, end: start + block.text.length });
    cursor = start + block.text.length;
  }
  const chunks: BlockChunk[] = [];
  let index = 0;
  while (index < located.length) {
    const origin = located[index];
    if (!origin) break;
    let end = index;
    let length = 0;
    while (end < located.length) {
      const next = located[end];
      if (!next) break;
      const span = next.end - origin.start;
      if (length >= TARGET_MIN && span > TARGET_MAX) break;
      length = span;
      end += 1;
      if (length >= 700) break;
    }
    if (end === index) end = index + 1;
    const slice = located.slice(index, end);
    const first = slice[0];
    const last = slice[slice.length - 1];
    if (!first || !last) break;
    chunks.push({
      text: body.slice(first.start, last.end),
      charStart: first.start,
      charEnd: last.end,
      blockIds: slice.map((item) => item.blockId),
    });
    index = end;
  }
  return { body, chunks };
}

function splitOversized(paragraph: Paragraph): Paragraph[] {
  if (paragraph.text.length <= TARGET_MAX) return [paragraph];
  const parts = paragraph.text.split(/(?<=[。！？!?])/u).filter((part) => part.length > 0);
  if (parts.length <= 1) return [paragraph];
  const pieces: Paragraph[] = [];
  let cursor = paragraph.start;
  for (const part of parts) {
    const at = paragraph.text.indexOf(part, cursor - paragraph.start);
    const start = at >= 0 ? paragraph.start + at : cursor;
    pieces.push({
      text: part,
      start,
      end: start + part.length,
      page: paragraph.page,
      headingPath: paragraph.headingPath,
    });
    cursor = start + part.length;
  }
  return pieces;
}

function splitParagraphs(body: string): Paragraph[] {
  const paragraphs: Paragraph[] = [];
  const headings: string[] = [];
  let page: number | null = body.includes("\f") ? 1 : null;
  let buffer = "";
  let bufferStart = 0;
  let index = 0;

  const flush = (end: number) => {
    const text = buffer.trim();
    if (text) {
      const leading = buffer.length - buffer.trimStart().length;
      paragraphs.push({
        text,
        start: bufferStart + leading,
        end: bufferStart + leading + text.length,
        page,
        headingPath: [...headings],
      });
    }
    buffer = "";
    bufferStart = end;
  };

  while (index < body.length) {
    if (body[index] === "\f") {
      flush(index);
      page = (page ?? 1) + 1;
      index += 1;
      bufferStart = index;
      continue;
    }
    const lineEnd = body.indexOf("\n", index);
    const end = lineEnd === -1 ? body.length : lineEnd;
    const line = body.slice(index, end);
    const heading = /^(#{1,6})\s+(\S.*)$/.exec(line);
    if (heading?.[1] && heading[2]) {
      flush(index);
      const level = heading[1].length;
      headings.splice(level - 1);
      headings[level - 1] = heading[2].trim();
      index = end + 1;
      bufferStart = index;
      continue;
    }
    if (line.trim() === "") {
      flush(end + 1);
      index = end + 1;
      continue;
    }
    if (!buffer) bufferStart = index;
    buffer += (buffer ? "\n" : "") + line;
    index = end + 1;
  }
  flush(body.length);
  return paragraphs;
}
