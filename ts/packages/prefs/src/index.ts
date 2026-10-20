export { compilePersonalLayer } from "./compile.js";
export { FACT_HINT, looksLikeFact, plainConflictOf } from "./inspect.js";
export {
  createPreferenceStore,
  FactLikeNote,
  factHintText,
  type NoteSql,
  PreferenceConflict,
  PreferenceInputError,
  PreferenceNotFound,
} from "./store.js";
export type {
  NoteActor,
  OverlayContext,
  Preference,
  PreferenceAuditEntry,
  PreferenceKind,
  PreferenceScope,
  PreferenceSource,
  PreferenceStatus,
  PreferenceStore,
  ProposeNoteInput,
  ReviewAction,
  ScopeLevel,
  WriteNoteInput,
} from "./types.js";
