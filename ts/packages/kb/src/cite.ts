import { type LwDatabase, newId, VersionNotFound } from "@luminwrite/db";
import {
  type CitationState,
  type CitedEvidence,
  gapsFor,
  hasQuestion,
  isQuestionId,
  normalizeCitationText,
  parseResearchPlan,
  type QuoteCheck,
  type ResearchPlan,
  verifyQuote,
} from "@luminwrite/research";

import { chunkBlocks } from "./chunk.js";
import { KbInputError } from "./parse.js";

export interface CitationView {
  id: string;
  citationStatus: CitationState;
  citedVersionId: string | null;
  sourceRevision: number;
  sourceHasNewerVersion: boolean;
  quote: string;
  locator: { blockId: string; charStart: number; charEnd: number };
  note?: string;
}

export interface RecordEvidenceInput {
  ownerId: string;
  citingDocumentId: string;
  sourceId: string;
  quote: string;
  blockId?: string;
  sessionId?: string;
  turnId?: string;
  planId?: string;
  questionId?: string;
}

interface SourcePin {
  id: string;
  kind: string;
  document_id: string | null;
  indexed_version_id: string | null;
  revision: number | string;
  body: string;
  authority: number | string;
}

interface EvidenceRow {
  id: string;
  citing_document_id: string;
  source_id: string;
  source_revision: number | string;
  cited_version_id: string | null;
  quote: string;
  locator: unknown;
  citation_status: string;
  plan_id: string | null;
}

interface Pinned {
  check: QuoteCheck;
  citedVersionId: string | null;
  sourceHasNewerVersion: boolean;
  locator: CitationView["locator"];
}

function num(value: number | string): number {
  return typeof value === "number" ? value : Number(value);
}

function viewOf(input: {
  id: string;
  status: CitationState;
  citedVersionId: string | null;
  sourceRevision: number;
  sourceHasNewerVersion: boolean;
  quote: string;
  locator: CitationView["locator"];
  note?: string;
}): CitationView {
  return {
    id: input.id,
    citationStatus: input.status,
    citedVersionId: input.citedVersionId,
    sourceRevision: input.sourceRevision,
    sourceHasNewerVersion: input.sourceHasNewerVersion,
    quote: input.quote,
    locator: input.locator,
    ...(input.note ? { note: input.note } : {}),
  };
}

async function projectedVersion(db: LwDatabase, documentId: string, versionId: string) {
  try {
    const blocks = await db.readBlocksAt(documentId, versionId);
    const packed = chunkBlocks(
      blocks.map((block) => ({ blockId: block.blockId, text: block.text })),
    );
    return { blocks, body: packed.body };
  } catch (error) {
    if (error instanceof VersionNotFound) throw new KbInputError("找不到版本");
    throw error;
  }
}

function blockIdFor(
  blocks: Array<{ blockId: string; text: string }>,
  quote: string,
  window: string,
): string {
  const needle = normalizeCitationText(quote);
  for (const block of blocks) {
    const text = normalizeCitationText(block.text);
    if (needle && text.includes(needle)) return block.blockId;
    if (window && text.includes(window)) return block.blockId;
  }
  return "";
}

async function loadSource(db: LwDatabase, ownerId: string, sourceId: string): Promise<SourcePin> {
  const rows = await db.query<SourcePin>(
    `SELECT id, kind, document_id, indexed_version_id, revision, body, authority
     FROM sources WHERE id = $1 AND owner_id = $2`,
    [sourceId, ownerId],
  );
  const row = rows[0];
  if (!row) throw new KbInputError("找不到来源");
  return row;
}

async function assertPlan(db: LwDatabase, input: RecordEvidenceInput): Promise<void> {
  if (input.questionId && !isQuestionId(input.questionId)) {
    throw new KbInputError("问题 id 不合法");
  }
  if (!input.planId) return;
  if (!input.questionId) throw new KbInputError("引用需要问题 id");
  const rows = await db.query<{ plan: unknown }>(
    `SELECT p.plan
     FROM research_plans p
     JOIN sessions s ON s.id = p.session_id
     JOIN documents d ON d.id = s.document_id
     WHERE p.id = $1 AND s.document_id = $2 AND d.owner_id = $3`,
    [input.planId, input.citingDocumentId, input.ownerId],
  );
  const row = rows[0];
  if (!row) throw new KbInputError("找不到研究计划");
  let plan: ResearchPlan;
  try {
    plan = parseResearchPlan(row.plan);
  } catch {
    throw new KbInputError("研究计划无法读取");
  }
  if (!hasQuestion(plan, input.questionId)) throw new KbInputError("研究计划里没有这个问题");
}

async function refreshPlanGaps(db: LwDatabase, planId: string): Promise<void> {
  const plans = await db.query<{ plan: unknown }>(`SELECT plan FROM research_plans WHERE id = $1`, [
    planId,
  ]);
  const row = plans[0];
  if (!row) return;
  let plan: ResearchPlan;
  try {
    plan = parseResearchPlan(row.plan);
  } catch {
    return;
  }
  const evidence = await db.query<{ question_id: string | null; citation_status: string }>(
    `SELECT question_id, citation_status FROM evidence WHERE plan_id = $1`,
    [planId],
  );
  const answered = new Set(
    evidence
      .filter((item) => item.citation_status === "verified" || item.citation_status === "fuzzy")
      .map((item) => item.question_id)
      .filter((id): id is string => Boolean(id)),
  );
  await db.query(`UPDATE research_plans SET gaps = $2::jsonb WHERE id = $1`, [
    planId,
    JSON.stringify(gapsFor(plan, answered)),
  ]);
}

async function readCorpus(
  db: LwDatabase,
  source: SourcePin,
  citedVersionId: string | null,
  sourceRevision: number,
  preferredBlockId: string,
  quote: string,
): Promise<Pinned> {
  if (source.kind === "document" && source.document_id && citedVersionId) {
    const cited = await db.getDocument(source.document_id);
    const sourceHasNewerVersion = Boolean(
      cited?.headVersionId && cited.headVersionId !== citedVersionId,
    );
    try {
      const pinned = await projectedVersion(db, source.document_id, citedVersionId);
      const blockMissing =
        Boolean(preferredBlockId) &&
        !pinned.blocks.some((block) => block.blockId === preferredBlockId);
      const check = verifyQuote({ quote, corpus: pinned.body, blockMissing });
      const normalized = normalizeCitationText(pinned.body);
      const window =
        check.status === "fuzzy" ? normalized.slice(check.charStart, check.charEnd) : "";
      const found = blockIdFor(pinned.blocks, quote, window);
      return {
        check,
        citedVersionId,
        sourceHasNewerVersion,
        locator: {
          blockId: found || preferredBlockId,
          charStart: check.charStart,
          charEnd: check.charEnd,
        },
      };
    } catch (error) {
      if (!(error instanceof KbInputError)) throw error;
      const check = verifyQuote({ quote, corpus: null, sourceMissing: true });
      return {
        check: { ...check, note: "版本已不存在" },
        citedVersionId,
        sourceHasNewerVersion,
        locator: { blockId: preferredBlockId, charStart: 0, charEnd: 0 },
      };
    }
  }
  if (source.kind === "document") {
    return {
      check: verifyQuote({ quote, corpus: null, sourceMissing: true }),
      citedVersionId,
      sourceHasNewerVersion: false,
      locator: { blockId: preferredBlockId, charStart: 0, charEnd: 0 },
    };
  }
  const revised = num(source.revision) !== sourceRevision;
  const check = verifyQuote({ quote, corpus: source.body, revised });
  return {
    check,
    citedVersionId: null,
    sourceHasNewerVersion: false,
    locator: { blockId: "", charStart: check.charStart, charEnd: check.charEnd },
  };
}

async function chunkForQuote(
  db: LwDatabase,
  sourceId: string,
  quote: string,
): Promise<string | null> {
  const rows = await db.query<{ id: string; text: string }>(
    `SELECT id, text FROM chunks WHERE source_id = $1 ORDER BY ord`,
    [sourceId],
  );
  const needle = quote.trim();
  return rows.find((row) => row.text.includes(needle))?.id ?? null;
}

function asLocator(value: unknown): CitationView["locator"] {
  const raw = typeof value === "string" ? (JSON.parse(value) as unknown) : value;
  if (!raw || typeof raw !== "object") return { blockId: "", charStart: 0, charEnd: 0 };
  const record = raw as { blockId?: unknown; charStart?: unknown; charEnd?: unknown };
  return {
    blockId: typeof record.blockId === "string" ? record.blockId : "",
    charStart:
      typeof record.charStart === "number" ? record.charStart : Number(record.charStart ?? 0),
    charEnd: typeof record.charEnd === "number" ? record.charEnd : Number(record.charEnd ?? 0),
  };
}

export async function writeEvidence(
  db: LwDatabase,
  input: RecordEvidenceInput,
): Promise<CitationView> {
  const quote = input.quote.trim();
  if (!quote) throw new KbInputError("引用不能为空");
  await assertPlan(db, input);
  const citing = await db.getDocument(input.citingDocumentId);
  if (!citing || citing.ownerId !== input.ownerId) throw new KbInputError("找不到文档");
  const source = await loadSource(db, input.ownerId, input.sourceId);
  const citedVersionId = source.kind === "document" ? source.indexed_version_id : null;
  if (source.kind === "document" && (!source.document_id || !citedVersionId)) {
    throw new KbInputError("来源还没有索引版本");
  }
  const pinned = await readCorpus(
    db,
    source,
    citedVersionId,
    num(source.revision),
    input.blockId ?? "",
    quote,
  );
  const id = newId("evd");
  await db.query(
    `INSERT INTO evidence (
       id, owner_id, citing_document_id, session_id, turn_id, plan_id, question_id,
       source_id, source_revision, chunk_id, cited_version_id, quote, locator,
       authority, citation_status
     ) VALUES (
       $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::jsonb, $14, $15
     )`,
    [
      id,
      input.ownerId,
      input.citingDocumentId,
      input.sessionId ?? null,
      input.turnId ?? null,
      input.planId ?? null,
      input.questionId ?? null,
      source.id,
      num(source.revision),
      await chunkForQuote(db, source.id, quote),
      pinned.citedVersionId,
      quote,
      JSON.stringify(pinned.locator),
      source.kind === "document" ? 3 : num(source.authority),
      pinned.check.status,
    ],
  );
  if (input.planId) await refreshPlanGaps(db, input.planId);
  return viewOf({
    id,
    status: pinned.check.status,
    citedVersionId: pinned.citedVersionId,
    sourceRevision: num(source.revision),
    sourceHasNewerVersion: pinned.sourceHasNewerVersion,
    quote,
    locator: pinned.locator,
    ...(pinned.check.note ? { note: pinned.check.note } : {}),
  });
}

export async function resolveEvidence(
  db: LwDatabase,
  ownerId: string,
  evidenceId: string,
): Promise<CitationView> {
  const rows = await db.query<EvidenceRow>(
    `SELECT id, citing_document_id, source_id, source_revision, cited_version_id, quote, locator,
            citation_status, plan_id
     FROM evidence WHERE id = $1 AND owner_id = $2`,
    [evidenceId, ownerId],
  );
  const row = rows[0];
  if (!row) throw new KbInputError("找不到证据");
  const sources = await db.query<SourcePin>(
    `SELECT id, kind, document_id, indexed_version_id, revision, body, authority
     FROM sources WHERE id = $1 AND owner_id = $2`,
    [row.source_id, ownerId],
  );
  const locator = asLocator(row.locator);
  const source = sources[0];
  const pinned = source
    ? await readCorpus(
        db,
        source,
        row.cited_version_id,
        num(row.source_revision),
        locator.blockId,
        row.quote,
      )
    : {
        check: verifyQuote({ quote: row.quote, corpus: null, sourceMissing: true }),
        citedVersionId: row.cited_version_id,
        sourceHasNewerVersion: false,
        locator,
      };
  if (pinned.check.status !== row.citation_status) {
    await db.query(`UPDATE evidence SET citation_status = $1 WHERE id = $2 AND owner_id = $3`, [
      pinned.check.status,
      evidenceId,
      ownerId,
    ]);
    if (row.plan_id) await refreshPlanGaps(db, row.plan_id);
  }
  return viewOf({
    id: row.id,
    status: pinned.check.status,
    citedVersionId: pinned.citedVersionId,
    sourceRevision: num(row.source_revision),
    sourceHasNewerVersion: pinned.sourceHasNewerVersion,
    quote: row.quote,
    locator: pinned.locator,
    ...(pinned.check.note ? { note: pinned.check.note } : {}),
  });
}

export async function listEvidence(
  db: LwDatabase,
  ownerId: string,
  citingDocumentId: string,
): Promise<CitedEvidence[]> {
  const rows = await db.query<{ id: string }>(
    `SELECT id FROM evidence
     WHERE owner_id = $1 AND citing_document_id = $2
     ORDER BY created_at, id`,
    [ownerId, citingDocumentId],
  );
  const listed: CitedEvidence[] = [];
  for (const row of rows) {
    const view = await resolveEvidence(db, ownerId, row.id);
    listed.push({
      id: view.id,
      citingDocumentId,
      status: view.citationStatus,
      ...(view.note ? { note: view.note } : {}),
    });
  }
  return listed;
}
