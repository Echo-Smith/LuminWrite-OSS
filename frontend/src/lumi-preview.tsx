/**
 * Lumi / 风格助手 视觉预览页（dev only，不进入任何路由）
 * 访问 /lumi-preview.html 查看；验收后可整体删除。
 */
/* eslint-disable react-refresh/only-export-components -- 预览页不参与应用打包 */
import { useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { Lumi } from "@/components/lumi/lumi";
import { SelectionLumi } from "@/components/lumi/selection-lumi";
import { StyleAssistantDialog } from "@/components/composer/style-assistant-dialog";
import { useStyleChatStore } from "@/stores/style-chat-store";

// 供浏览器验收脚本直接操作（仅 dev 预览页）
(window as unknown as Record<string, unknown>).__styleChat = useStyleChatStore;import "../src/index.css";

function Preview() {
  const [spying, setSpying] = useState(false);
  const setOpen = useStyleChatStore((s) => s.setOpen);

  const openDialogWithMock = () => {
    setOpen(true);
    if (!spying) {
      setSpying(true);
      void mockSend();
    }
  };

  return (
    <div className="min-h-screen bg-[color:var(--desk-canvas)] p-10 text-foreground">
      <h1 className="mb-6 text-xl font-semibold">Lumi 预览（dev only）</h1>

      <section className="mb-10">
        <h2 className="mb-3 text-sm text-muted-foreground">六态 · 40px（墨色）</h2>
        <div className="flex items-end gap-10 rounded-2xl border bg-[color:var(--desk-paper)] p-6">
          {(["idle", "thinking", "writing", "done", "error", "paused"] as const).map((s) => (
            <div key={s} className="flex flex-col items-center gap-2">
              <Lumi state={s} size={40} label={`Lumi ${s}`} />
              <span className="text-xs text-muted-foreground">{s}</span>
            </div>
          ))}
        </div>
      </section>

      <section className="mb-10">
        <h2 className="mb-3 text-sm text-muted-foreground">按钮内 18px（composer 入口规格）</h2>
        <div className="flex gap-4 rounded-2xl border bg-[color:var(--desk-paper)] p-4">
          {(["idle", "thinking", "writing", "done"] as const).map((s) => (
            <button key={s} className="flex h-8 w-8 items-center justify-center rounded-xl text-muted-foreground hover:bg-accent hover:text-foreground">
              <Lumi state={s} size={18} />
            </button>
          ))}
          <span className="self-center text-xs text-muted-foreground">← 与 lucide 控件同框对比</span>
          <button className="flex h-8 w-8 items-center justify-center rounded-xl text-muted-foreground hover:bg-accent hover:text-foreground" aria-label="示例">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 20h9" /><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" /></svg>
          </button>
        </div>
      </section>

      <section className="mb-10">
        <h2 className="mb-3 text-sm text-muted-foreground">暗色模式</h2>
        <div className="flex items-end gap-10 rounded-2xl border bg-[color:var(--desk-ink)] p-6 text-[color:var(--desk-paper)]">
          {(["idle", "thinking", "writing", "done", "error", "paused"] as const).map((s) => (
            <div key={s} className="flex flex-col items-center gap-2">
              <Lumi state={s} size={40} />
              <span className="text-xs opacity-60">{s}</span>
            </div>
          ))}
        </div>
      </section>

      <section className="mb-10">
        <h2 className="mb-3 text-sm text-muted-foreground">选区唤起（用鼠标选中下方文字）</h2>
        <SelectionDemo />
      </section>

      <section>
        <h2 className="mb-3 text-sm text-muted-foreground">风格助手对话框（mock 回复）</h2>
        <button
          onClick={openDialogWithMock}
          className="rounded-xl bg-foreground px-4 py-2 text-sm text-background hover:scale-[1.02] transition-transform-precise"
        >
          打开对话框
        </button>
      </section>

      <StyleAssistantDialog />
    </div>
  );
}

/** 选区唤起演示：挂 SelectionLumi，点击后展示回调到的文本 */
function SelectionDemo() {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  return (
    <div className="rounded-2xl border bg-[color:var(--desk-paper)] p-5">
      <div ref={containerRef} className="text-sm leading-7 text-foreground">
        <p>
          写作是一种克制的艺术。好的句子不是堆出来的，而是删出来的——每一个形容词都要接受审问：
          你带来的信息，删掉你之后句子会损失什么？如果答案是「没有」，你就不配留在这里。
        </p>
        <p className="mt-2 text-muted-foreground">
          （选中任意一句，浮动菜单会出现；点击「让 Lumi 润色」模拟回调。）
        </p>
      </div>
      <SelectionLumi containerRef={containerRef} onPolish={(text) => setPicked(text)} />
      {picked && (
        <p className="mt-3 rounded-xl bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
          onPolish 已回调：{picked.slice(0, 60)}…
        </p>
      )}
    </div>
  );
}

/** 预览专用：拦截 style-builder 请求并真实调用 send，呈现完整动效 */
async function mockSend() {
  const original = window.fetch;
  window.fetch = async (input, init) => {
    const url = typeof input === "string" ? input : input instanceof Request ? input.url : String(input);
    if (url.includes("/api/v2/style-builder/sessions")) {
      const body = init?.body ? JSON.parse(String(init.body)) : null;
      const reply =
        "好的，我提炼出了这样的风格：**冷静克制 · 短句驱动**。\n\n- 句子平均 12 字以内，多用句号，少用连词\n- 不堆形容词，情绪藏在动作里\n- 段落之间留白，让读者自己拼图\n\n这个方向接近你说的感觉吗？可以再举一段你喜欢的文字让我校准。";
      return new Response(
        JSON.stringify(
          body
            ? { success: true, data: { message: reply, ready: true, profile: { name: "冷静克制 · 短句驱动", description: reply } } }
            : { success: true, data: { session_id: "preview-session" } }
        ),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }
    return original(input as RequestInfo, init);
  };
  const { useStyleChatStore: store } = await import("@/stores/style-chat-store");
  await store.getState().send("想要冷静克制、短句多、不堆形容词的风格");
}

createRoot(document.getElementById("root")!).render(<Preview />);
