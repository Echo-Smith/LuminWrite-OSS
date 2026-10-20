import type { CitationStatus } from "./verify.js";

export type CitationState = CitationStatus | "unverified";

export interface CitedEvidence {
  id: string;
  citingDocumentId: string;
  status: CitationState;
  note?: string;
}

export interface ClaimFlag {
  blockId?: string;
  evidenceId?: string;
  status: CitationState;
  excerpt: string;
  note: string;
}

const CITE = /\[\[cite:([A-Za-z0-9][A-Za-z0-9_.-]{0,127})\]\]/gu;
const CITE_TEST = /\[\[cite:[A-Za-z0-9][A-Za-z0-9_.-]{0,127}\]\]/u;
const STRONG =
  /(?:[0-9]+(?:\.[0-9]+)?\s*(?:%|％|万|千|亿|倍))|(?:导致|致使|证明了|表明了|显著提高|显著降低|显著提升|显著下降|毫无疑问|必然导致)/u;

function sentences(text: string): string[] {
  return text
    .split(/\n+/u)
    .flatMap((line) => line.split(/(?<=[。！？!?])/u))
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}

function pushUnchecked(
  flags: ClaimFlag[],
  flag: Omit<ClaimFlag, "status" | "note"> & { status: CitationState; note?: string },
): void {
  if (flag.status === "verified" || flag.status === "fuzzy") return;
  flags.push({
    ...(flag.blockId ? { blockId: flag.blockId } : {}),
    ...(flag.evidenceId ? { evidenceId: flag.evidenceId } : {}),
    status: flag.status,
    excerpt: flag.excerpt,
    note: flag.note ?? "引用未通过校验",
  });
}

/**
 * 正文和档案里对不上的引用。verified / fuzzy 不标。
 * 无证据的数字或强判断记为 unverified，对应原来的「没有证据的强断言」，不另做一种状态。
 */
export function flagClaims(input: {
  documentId: string;
  blocks: Array<{ blockId: string; text: string }>;
  dossier: { citations: Array<{ title: string; locator: string; evidenceId?: string }> };
  evidence: CitedEvidence[];
}): ClaimFlag[] {
  const byId = new Map(input.evidence.map((item) => [item.id, item]));
  const flags: ClaimFlag[] = [];
  for (const citation of input.dossier.citations) {
    if (!citation.evidenceId) {
      flags.push({
        status: "unverified",
        excerpt: citation.title,
        note: "档案引用没有 evidenceId",
      });
      continue;
    }
    const row = byId.get(citation.evidenceId);
    if (!row || row.citingDocumentId !== input.documentId) {
      flags.push({
        evidenceId: citation.evidenceId,
        status: "mismatch",
        excerpt: citation.title,
        note: "档案引用的证据不存在",
      });
      continue;
    }
    pushUnchecked(flags, {
      evidenceId: row.id,
      status: row.status,
      excerpt: citation.title,
      note: row.note ?? "档案引用未通过校验",
    });
  }
  for (const block of input.blocks) {
    const seen = new Set<string>();
    for (const match of block.text.matchAll(CITE)) {
      const evidenceId = match[1];
      if (!evidenceId || seen.has(evidenceId)) continue;
      seen.add(evidenceId);
      const row = byId.get(evidenceId);
      if (!row || row.citingDocumentId !== input.documentId) {
        flags.push({
          blockId: block.blockId,
          evidenceId,
          status: "mismatch",
          excerpt: match[0],
          note: "正文引用没有对应证据",
        });
        continue;
      }
      pushUnchecked(flags, {
        blockId: block.blockId,
        evidenceId,
        status: row.status,
        excerpt: match[0],
        note: row.note,
      });
    }
    for (const sentence of sentences(block.text)) {
      if (CITE_TEST.test(sentence)) continue;
      if ([...sentence].length < 8) continue;
      if (!STRONG.test(sentence)) continue;
      flags.push({
        blockId: block.blockId,
        status: "unverified",
        excerpt: [...sentence].slice(0, 180).join(""),
        note: "这句话有数字或强判断，但没有 [[cite:]]",
      });
    }
  }
  return flags;
}
