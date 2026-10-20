import type { StyleLayer } from "@luminwrite/style";

export const PREFERENCE_KINDS = [
  "habit",
  "sentence_pattern",
  "taboo",
  "term",
  "structure",
  "tone",
  "format",
] as const;

export type PreferenceKind = (typeof PREFERENCE_KINDS)[number];

/** `superseded` 是改措辞后的关闭态。地图里的四态没有这个名字。 */
export const PREFERENCE_STATUSES = [
  "candidate",
  "active",
  "muted",
  "rejected",
  "superseded",
] as const;

export type PreferenceStatus = (typeof PREFERENCE_STATUSES)[number];

export const PREFERENCE_SOURCES = [
  "corpus",
  "manual_edit",
  "hunk_decision",
  "user_written",
  "agent_proposed",
] as const;

export type PreferenceSource = (typeof PREFERENCE_SOURCES)[number];

export const SCOPE_LEVELS = ["global", "pack", "genre", "document"] as const;

export type ScopeLevel = (typeof SCOPE_LEVELS)[number];

export interface PreferenceScope {
  level: ScopeLevel;
  ref?: string;
}

export interface Preference {
  id: string;
  userId: string;
  kind: PreferenceKind;
  statement: string;
  rule: null;
  scope: PreferenceScope;
  strength: "must" | "prefer";
  status: PreferenceStatus;
  source: PreferenceSource;
  support: number;
  contradict: number;
  plainConflict: boolean;
  version: number;
  supersededBy: string | null;
  proposedInSessionId: string | null;
  updatedAt: string;
  factHint?: boolean;
}

export interface PreferenceAuditEntry {
  id: string;
  preferenceId: string;
  action: string;
  actorType: string;
  actorId: string;
  before: string;
  after: string;
  at: string;
}

export interface NoteActor {
  type: string;
  id: string;
}

export interface WriteNoteInput {
  text: string;
  kind: PreferenceKind;
  scope: PreferenceScope;
  strength?: "must" | "prefer";
}

export interface ProposeNoteInput {
  text: string;
  kind: PreferenceKind;
  scope: PreferenceScope;
  sessionId: string;
}

export type ReviewAction = "accept" | "reject" | "mute" | "unmute";

export interface OverlayContext {
  genre?: string;
  documentId?: string;
  packRef?: string;
}

export interface PreferenceStore {
  list(userId: string, filter?: { status?: PreferenceStatus[] }): Promise<Preference[]>;
  write(
    userId: string,
    input: WriteNoteInput,
    by: NoteActor,
  ): Promise<{ preference: Preference; factHint: boolean }>;
  propose(userId: string, input: ProposeNoteInput): Promise<Preference>;
  review(userId: string, id: string, action: ReviewAction, by: NoteActor): Promise<Preference>;
  correct(
    userId: string,
    id: string,
    expectedVersion: number,
    newText: string,
    by: NoteActor,
  ): Promise<Preference>;
  forget(
    userId: string,
    id: string,
    by: NoteActor,
  ): Promise<{ status: "verified"; receiptId: string }>;
  explain(userId: string, id: string): Promise<PreferenceAuditEntry[]>;
  compileOverlay(userId: string, ctx: OverlayContext): Promise<StyleLayer>;
}
