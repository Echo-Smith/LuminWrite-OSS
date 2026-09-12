/**
 * ─── 风格列表刷新信号 ───────────────────────────────────────
 *
 * 风格列表分散在多处本地加载（composer 的 StylePicker、my-styles 页）。
 * Lumi 保存新风格后 bump 版本号，已挂载的列表据此重新拉取，
 * 避免「保存成功但列表要重开才出现」。
 */
import { create } from "zustand";

interface StyleListState {
  /** 递增即表示风格库有变化（新增/更新），消费方监听后重新拉取 */
  version: number;
  requestRefresh: () => void;
}

export const useStyleListStore = create<StyleListState>((set) => ({
  version: 0,
  requestRefresh: () => set((s) => ({ version: s.version + 1 })),
}));
