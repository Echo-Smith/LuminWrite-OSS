/**
 * 会话已读标记 — 侧栏状态点的「未读」语义
 *
 * 蓝点（已完成）/ 红点（失败）相当于手机消息的未读提示：
 * 点击会话一次即视为已读，点随之消失；新一轮写作产生新 trace 后重新视为未读。
 * 进行中的状态（写作中 / 已暂停）不参与已读逻辑。
 *
 * 以 traceId（无则本地会话 id）为键持久化在 localStorage——每轮写作 trace 会更换，
 * 天然支持"新结果重新点亮"。
 */

const STORAGE_KEY = "lumin-session-read-v1";

type ReadMap = Record<string, number>;

function readMap(): ReadMap {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as ReadMap;
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

export function sessionReadKey(session: { traceId: string | null; id: string }): string {
  return session.traceId || session.id;
}

/** 该会话是否已读（曾被打开过一次）。 */
export function isSessionRead(session: { traceId: string | null; id: string }): boolean {
  const key = sessionReadKey(session);
  if (!key) return false;
  const at = readMap()[key];
  return typeof at === "number" && at > 0;
}

/** 标记会话为已读（点击会话时调用）。 */
export function markSessionRead(session: { traceId: string | null; id: string }): void {
  const key = sessionReadKey(session);
  if (!key) return;
  try {
    const map = readMap();
    map[key] = Date.now();
    // 防止长期膨胀：只保留最近 500 条
    const entries = Object.entries(map).sort((a, b) => b[1] - a[1]).slice(0, 500);
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(entries)));
  } catch {
    // localStorage 不可用（隐私模式等）时静默降级：点不消失
  }
}
