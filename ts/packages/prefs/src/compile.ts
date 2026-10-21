import type { StyleLayer } from "@luminwrite/style";

import type { OverlayContext, Preference } from "./types.js";

function scopeMatches(note: Preference, ctx: OverlayContext): boolean {
  if (note.scope.level === "global") return true;
  if (note.scope.level === "genre") return Boolean(ctx.genre && note.scope.ref === ctx.genre);
  if (note.scope.level === "document") {
    return Boolean(ctx.documentId && note.scope.ref === ctx.documentId);
  }
  return Boolean(ctx.packRef && note.scope.ref === ctx.packRef);
}

/** 只有 active、未关闭、且不和说人话冲突的条目进个人风格层。 */
export function compilePersonalLayer(
  userId: string,
  notes: readonly Preference[],
  ctx: OverlayContext,
): StyleLayer {
  const included = notes.filter(
    (note) =>
      note.status === "active" &&
      note.supersededBy == null &&
      !note.plainConflict &&
      scopeMatches(note, ctx),
  );
  const version = included.reduce((max, note) => Math.max(max, note.version), 0);
  const prose = included.map((note) => note.statement).join("\n");
  return {
    origin: "personal",
    ref: `prefs:${userId}@v${version}`,
    rules: [],
    ...(prose ? { prose } : {}),
  };
}
