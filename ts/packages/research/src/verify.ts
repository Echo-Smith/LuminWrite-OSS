export type CitationStatus = "verified" | "fuzzy" | "not_found" | "mismatch";

export interface QuoteCheck {
  status: CitationStatus;
  charStart: number;
  charEnd: number;
  /** 命中时的编辑距离。对不上时为 -1。 */
  distance: number;
  note?: string;
}

/** 全角 ASCII 折成半角，空白收成一个空格。校验只在这之后的文本上找。 */
export function normalizeCitationText(text: string): string {
  let folded = "";
  for (const char of text) {
    const code = char.codePointAt(0);
    if (code === undefined) continue;
    if (code >= 0xff01 && code <= 0xff5e) folded += String.fromCodePoint(code - 0xfee0);
    else if (code === 0x3000) folded += " ";
    else folded += char;
  }
  return folded.replace(/\s+/gu, " ").trim();
}

function chars(text: string): string[] {
  return [...text];
}

function levenshtein(left: string[], right: string[], max: number): number {
  if (Math.abs(left.length - right.length) > max) return max + 1;
  let prev = Array.from({ length: right.length + 1 }, (_, index) => index);
  let curr = new Array<number>(right.length + 1).fill(0);
  for (let i = 1; i <= left.length; i += 1) {
    curr[0] = i;
    let rowMin = curr[0] ?? max + 1;
    for (let j = 1; j <= right.length; j += 1) {
      const cost = left[i - 1] === right[j - 1] ? 0 : 1;
      const value = Math.min(
        (prev[j] ?? max) + 1,
        (curr[j - 1] ?? max) + 1,
        (prev[j - 1] ?? max) + cost,
      );
      curr[j] = value;
      if (value < rowMin) rowMin = value;
    }
    if (rowMin > max) return max + 1;
    [prev, curr] = [curr, prev];
  }
  return prev[right.length] ?? max + 1;
}

function findFuzzy(
  quote: string[],
  corpus: string,
  max: number,
): { start: number; end: number; distance: number } | null {
  const pieceCount = max + 1;
  const pieceLen = Math.max(2, Math.floor(quote.length / pieceCount));
  const quoteText = quote.join("");
  let best: { start: number; end: number; distance: number } | null = null;
  const seen = new Set<number>();
  for (
    let offset = 0;
    offset + pieceLen <= quoteText.length && offset / pieceLen < pieceCount;
    offset += pieceLen
  ) {
    const piece = quoteText.slice(offset, offset + pieceLen);
    let from = 0;
    let hits = 0;
    while (from <= corpus.length && hits < 64) {
      const at = corpus.indexOf(piece, from);
      if (at < 0) break;
      from = at + 1;
      hits += 1;
      const guess = at - offset;
      for (let start = guess - max; start <= guess + max; start += 1) {
        if (start < 0 || start > corpus.length || seen.has(start)) continue;
        seen.add(start);
        for (let len = quote.length - max; len <= quote.length + max; len += 1) {
          if (len <= 0) continue;
          const end = start + len;
          if (end > corpus.length) continue;
          const window = chars(corpus.slice(start, end));
          const distance = levenshtein(quote, window, max);
          if (distance <= max && (!best || distance < best.distance)) {
            best = { start, end, distance };
            if (distance === 0) return best;
          }
        }
      }
    }
  }
  return best;
}

export function verifyQuote(input: {
  quote: string;
  /** null 表示钉住的来源或版本已经读不到。 */
  corpus: string | null;
  sourceMissing?: boolean;
  blockMissing?: boolean;
  /** 来源修订号已经变了，正文是新的。文档钉的是旧版本，不走这条。 */
  revised?: boolean;
}): QuoteCheck {
  if (input.sourceMissing || input.corpus == null) {
    return { status: "mismatch", charStart: 0, charEnd: 0, distance: -1, note: "来源已不存在" };
  }
  if (input.blockMissing) {
    return { status: "mismatch", charStart: 0, charEnd: 0, distance: -1, note: "块已不存在" };
  }
  const quote = normalizeCitationText(input.quote);
  const corpus = normalizeCitationText(input.corpus);
  if (!quote) {
    return { status: "not_found", charStart: 0, charEnd: 0, distance: -1 };
  }
  const exact = corpus.indexOf(quote);
  if (exact >= 0) {
    return { status: "verified", charStart: exact, charEnd: exact + quote.length, distance: 0 };
  }
  const quoteChars = chars(quote);
  const max = Math.floor(quoteChars.length * 0.1);
  const fuzzy = max > 0 ? findFuzzy(quoteChars, corpus, max) : null;
  if (fuzzy) {
    return {
      status: "fuzzy",
      charStart: fuzzy.start,
      charEnd: fuzzy.end,
      distance: fuzzy.distance,
    };
  }
  if (input.revised) {
    return { status: "mismatch", charStart: 0, charEnd: 0, distance: -1, note: "来源已修改" };
  }
  return { status: "not_found", charStart: 0, charEnd: 0, distance: -1 };
}
