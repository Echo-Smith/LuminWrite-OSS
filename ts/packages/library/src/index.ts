/** 文章库就是已保存文档的查询、标签和归档。归档沿用文档的丢弃和清退。 */
export const phase = "library" as const;

export class TagNameError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TagNameError";
  }
}

export class ArticleQueryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ArticleQueryError";
  }
}

export interface ArticleListQuery {
  text?: string;
  tags: string[];
  genre?: string;
  createdVia?: string;
  archived: "active" | "archived" | "all";
  kbExcluded?: boolean;
  limit: number;
}

const TAG_MAX = 40;
const TEXT_MAX = 200;
const LIMIT_DEFAULT = 50;
const LIMIT_MAX = 100;
const VIAS = new Set(["ui", "api", "import", "derive"]);

/** 去掉首尾空白，把连续空白收成一个空格。空的、过长的、带逗号的名称拒绝。 */
export function normalizeTagName(raw: string): string {
  const name = raw.trim().replace(/\s+/g, " ");
  if (!name || name.length > TAG_MAX) {
    throw new TagNameError(`标签要在 1 到 ${TAG_MAX} 个字之间`);
  }
  if (name.includes(",")) throw new TagNameError("标签不能包含逗号");
  return name;
}

export function parseArticleListQuery(input: {
  q?: string;
  tag?: string[];
  genre?: string;
  via?: string;
  archived?: string;
  kb?: string;
  limit?: string;
}): ArticleListQuery {
  const text = input.q?.trim();
  if (text && text.length > TEXT_MAX) throw new ArticleQueryError("搜索词太长");
  const tags: string[] = [];
  for (const part of input.tag ?? []) {
    for (const piece of part.split(",")) {
      if (!piece.trim()) continue;
      const name = normalizeTagName(piece);
      if (!tags.includes(name)) tags.push(name);
    }
  }
  const genre = input.genre?.trim();
  if (genre && genre.length > TAG_MAX) throw new ArticleQueryError("体裁名称太长");
  const via = input.via?.trim();
  if (via && !VIAS.has(via)) throw new ArticleQueryError("不认识的来源");
  let archived: ArticleListQuery["archived"] = "active";
  const archivedRaw = input.archived?.trim();
  if (archivedRaw === "true" || archivedRaw === "archived") archived = "archived";
  else if (archivedRaw === "all") archived = "all";
  else if (archivedRaw && archivedRaw !== "false" && archivedRaw !== "active") {
    throw new ArticleQueryError("归档过滤只能是 active、archived 或 all");
  }
  let kbExcluded: boolean | undefined;
  const kb = input.kb?.trim();
  if (kb === "excluded" || kb === "true") kbExcluded = true;
  else if (kb === "included" || kb === "false") kbExcluded = false;
  else if (kb) throw new ArticleQueryError("知识库过滤只能是 included、excluded 或不填");
  let limit = LIMIT_DEFAULT;
  if (input.limit != null && input.limit !== "") {
    if (!/^\d+$/.test(input.limit)) throw new ArticleQueryError("条数必须是正整数");
    limit = Number(input.limit);
    if (limit < 1 || limit > LIMIT_MAX) {
      throw new ArticleQueryError(`条数要在 1 到 ${LIMIT_MAX} 之间`);
    }
  }
  return {
    ...(text ? { text } : {}),
    tags,
    ...(genre ? { genre } : {}),
    ...(via ? { createdVia: via } : {}),
    archived,
    ...(kbExcluded === undefined ? {} : { kbExcluded }),
    limit,
  };
}
