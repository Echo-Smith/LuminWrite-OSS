import { z } from "zod";

/** 文档档案。正文不在这里，正文只在版本链里。 */
export interface DossierCitation {
  title: string;
  locator: string;
  /** 可选。老档案没有这一项，不回填。 */
  evidenceId?: string;
}

export interface DossierContent {
  outline: string[];
  decisions: string[];
  citations: DossierCitation[];
  requirements: string[];
}

const DossierCitationSchema = z.object({
  title: z.string(),
  locator: z.string(),
  evidenceId: z.string().min(1).max(80).optional(),
});

export const DossierContentSchema = z.object({
  outline: z.array(z.string()),
  decisions: z.array(z.string()),
  citations: z.array(DossierCitationSchema),
  requirements: z.array(z.string()),
});

export const DossierOpSchema = z.discriminatedUnion("op", [
  z.object({ op: z.literal("set_outline"), items: z.array(z.string().min(1).max(500)).max(50) }),
  z.object({ op: z.literal("add_decision"), text: z.string().min(1).max(500) }),
  z.object({
    op: z.literal("add_citation"),
    title: z.string().min(1).max(300),
    locator: z.string().max(300).default(""),
    evidenceId: z.string().min(1).max(80).optional(),
  }),
  z.object({ op: z.literal("add_requirement"), text: z.string().min(1).max(500) }),
]);

export type DossierOp = z.infer<typeof DossierOpSchema>;

export function emptyDossier(): DossierContent {
  return { outline: [], decisions: [], citations: [], requirements: [] };
}

/**
 * 派生时只复制起点材料：大纲、引用、要求。
 * 关键决定不以任何形式复制，也不做引用态或停用态。要留下某条决定，由派生文档自己的档案操作写入。
 * 版本号从 1 重新计。
 */
function citationOf(item: DossierCitation): DossierCitation {
  return {
    title: item.title,
    locator: item.locator,
    ...(item.evidenceId ? { evidenceId: item.evidenceId } : {}),
  };
}

export function dossierForDerived(source: DossierContent, inherit: boolean): DossierContent {
  if (!inherit) return emptyDossier();
  return {
    outline: [...source.outline],
    decisions: [],
    citations: source.citations.map(citationOf),
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
    citations: content.citations.map(citationOf),
    requirements: [...content.requirements],
  };
  for (const op of ops) {
    if (op.op === "set_outline") next.outline = [...op.items];
    else if (op.op === "add_decision") next.decisions = [...next.decisions, op.text];
    else if (op.op === "add_citation") {
      next.citations = [
        ...next.citations,
        citationOf({
          title: op.title,
          locator: op.locator,
          ...(op.evidenceId ? { evidenceId: op.evidenceId } : {}),
        }),
      ];
    } else next.requirements = [...next.requirements, op.text];
  }
  return next;
}
