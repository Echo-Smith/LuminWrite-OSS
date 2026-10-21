/** Reciprocal Rank Fusion。k=60。只有一路结果时顺序不变。 */
export const RRF_K = 60;
export const RETRIEVAL_POOL = 50;

export function rrfMerge(lists: string[][], k = RRF_K): Array<{ id: string; score: number }> {
  const scores = new Map<string, number>();
  for (const list of lists) {
    list.forEach((id, index) => {
      scores.set(id, (scores.get(id) ?? 0) + 1 / (k + index + 1));
    });
  }
  return [...scores.entries()]
    .map(([id, score]) => ({ id, score }))
    .sort((a, b) => b.score - a.score || a.id.localeCompare(b.id));
}
