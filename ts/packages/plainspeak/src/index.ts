import lexicon from "./aitone_zh.json" with { type: "json" };

export interface PlainHit {
  term: string;
  role: "hollow" | "rhetoric" | "format";
  offset: number;
}

export interface PlainReport {
  hollowScore: number;
  hollowSentenceRatio: number;
  hits: PlainHit[];
  pass: boolean;
}

export interface Tokenizer {
  sentences(text: string): string[];
}

export interface AnalyzeOptions {
  rhetoricAllowed?: boolean;
  tokenizer?: Tokenizer;
}

const hollowTerms = lexicon.hollow;
const rhetoricTerms = lexicon.rhetoric;
const patterns = lexicon.patterns.map((source) => new RegExp(source, "g"));

export function defaultTokenizer(): Tokenizer {
  return {
    sentences(text: string): string[] {
      return text
        .split(/(?<=[。！？!?；;])/)
        .map((sentence) => sentence.trim())
        .filter((sentence) => sentence.length > 0);
    },
  };
}

export function isHollowPhrase(phrase: string): boolean {
  if (hollowTerms.some((term) => phrase.includes(term))) return true;
  return patterns.some((pattern) => {
    pattern.lastIndex = 0;
    return pattern.test(phrase);
  });
}

export function analyzePlain(text: string, opts?: AnalyzeOptions): PlainReport {
  const tokenizer = opts?.tokenizer ?? defaultTokenizer();
  const rhetoricAllowed = opts?.rhetoricAllowed ?? true;
  const sentences = tokenizer.sentences(text);
  const hits: PlainHit[] = [];
  let hollowSentences = 0;

  const scan = (sentence: string, base: number): boolean => {
    let hollow = false;
    for (const term of hollowTerms) {
      const offset = sentence.indexOf(term);
      if (offset >= 0) {
        hits.push({ term, role: "hollow", offset: base + offset });
        hollow = true;
      }
    }
    for (const pattern of patterns) {
      pattern.lastIndex = 0;
      const match = pattern.exec(sentence);
      if (match?.[0]) {
        hits.push({ term: match[0], role: "hollow", offset: base + match.index });
        hollow = true;
      }
    }
    if (!rhetoricAllowed) {
      for (const term of rhetoricTerms) {
        const offset = sentence.indexOf(term);
        if (offset >= 0) hits.push({ term, role: "rhetoric", offset: base + offset });
      }
    }
    return hollow;
  };

  let cursor = 0;
  for (const sentence of sentences) {
    const at = text.indexOf(sentence, cursor);
    const base = at >= 0 ? at : cursor;
    if (scan(sentence, base)) hollowSentences += 1;
    cursor = base + sentence.length;
  }
  if (sentences.length === 0 && text.trim().length > 0) {
    if (scan(text, 0)) hollowSentences += 1;
  }

  const total = Math.max(sentences.length, text.trim().length > 0 ? 1 : 0);
  const hollowSentenceRatio = total === 0 ? 0 : hollowSentences / total;
  const hollowScore = Math.min(100, Math.round(hollowSentenceRatio * 100 + hits.length * 4));
  const pass = hollowSentenceRatio <= 0.2 && hollowScore < 40;
  return { hollowScore, hollowSentenceRatio, hits, pass };
}

export function plainspeakFloorTerms(): readonly string[] {
  return hollowTerms;
}
