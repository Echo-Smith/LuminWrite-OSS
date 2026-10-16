import { z } from "zod";

/** 文档档案。正文不在这里，正文只在版本链里。 */
export interface DossierCitation {
  title: string;
  locator: string;
}

export interface DossierContent {
  outline: string[];
  decisions: string[];
  citations: DossierCitation[];
  requirements: string[];
}

export const DossierContentSchema = z.object({
  outline: z.array(z.string()),
  decisions: z.array(z.string()),
  citations: z.array(z.object({ title: z.string(), locator: z.string() })),
  requirements: z.array(z.string()),
});

export const DossierOpSchema = z.discriminatedUnion("op", [
  z.object({ op: z.literal("set_outline"), items: z.array(z.string().min(1).max(500)).max(50) }),
  z.object({ op: z.literal("add_decision"), text: z.string().min(1).max(500) }),
  z.object({
    op: z.literal("add_citation"),
    title: z.string().min(1).max(300),
    locator: z.string().max(300).default(""),
  }),
  z.object({ op: z.literal("add_requirement"), text: z.string().min(1).max(500) }),
]);

export type DossierOp = z.infer<typeof DossierOpSchema>;

export function emptyDossier(): DossierContent {
  return { outline: [], decisions: [], citations: [], requirements: [] };
}

/**
 * 派生文档的档案。inherit 为真时复制大纲、引用和要求；关键决定不复制，派生文档自己再记。
 * 版本号从 1 重新计，不沿用源文档的档案版本。
 */
export function dossierForDerived(source: DossierContent, inherit: boolean): DossierContent {
  if (!inherit) return emptyDossier();
  return {
    outline: [...source.outline],
    decisions: [],
    citations: source.citations.map((item) => ({ title: item.title, locator: item.locator })),
    requirements: [...source.requirements],
  };
}

export function applyDossierOps(
  content: DossierContent,
  ops: readonly DossierOp[],
): DossierContent {
  const next: DossierContent = {
    outline: [...content.outline],
    decisions: [...content.decisions],
    citations: content.citations.map((item) => ({ title: item.title, locator: item.locator })),
    requirements: [...content.requirements],
  };
  for (const op of ops) {
    if (op.op === "set_outline") next.outline = [...op.items];
    else if (op.op === "add_decision") next.decisions = [...next.decisions, op.text];
    else if (op.op === "add_citation") {
      next.citations = [...next.citations, { title: op.title, locator: op.locator }];
    } else next.requirements = [...next.requirements, op.text];
  }
  return next;
}
