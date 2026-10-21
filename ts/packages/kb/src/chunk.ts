export interface TextChunk {
  text: string;
  charStart: number;
  charEnd: number;
  page: number | null;
  headingPath: string[];
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
