import * as React from "react";
import { PenLine, RotateCcw, Save, Copy, Check } from "lucide-react";
import { toast } from "@/stores/toast-store";

import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  MessageScroller,
  MessageScrollerButton,
  MessageScrollerContent,
  MessageScrollerItem,
  MessageScrollerProvider,
  MessageScrollerViewport,
} from "@/components/ui/message-scroller";
import { Lumi } from "@/components/lumi/lumi";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useStyleChatStore, type StyleChatMessage } from "@/stores/style-chat-store";

/**
 * ─── 风格助手（Lumi）对话框 ─────────────────────────────────
 *
 * 滚动行为交给 message-scroller（stick-to-bottom、读者上滚即解除跟随、
 * 「回到最新」浮钮）；助手形象由 Lumi 驱动，phase 映射：
 *   thinking → 呼吸圆点 / writing → 摆笔书写 / done → 星星 / idle → 静置笔。
 * 视觉语言对齐写作区 composer：黄铜强调色、rounded-xl 控件、PenLine 发送键。
 */

const SUGGESTIONS = [
  "想要冷静克制、短句多、不堆形容词的新闻特稿风格",
  "参考王小波：反讽幽默，节奏松弛，口语但有文学感",
  "把一段我喜欢的文字提炼成风格",
];

/** ready 后追加在消息流末尾的操作卡：风格信息足够，可保存 */
function ReadyCard({ onSave }: { onSave: () => Promise<boolean> }) {
  const [committing, setCommitting] = React.useState(false);
  const handle = async () => {
    setCommitting(true);
    try {
      await onSave();
    } finally {
      setCommitting(false);
    }
  };
  return (
    <div
      className="flex items-center gap-3 rounded-2xl border px-4 py-2.5"
      style={{
        borderColor: "color-mix(in srgb, var(--desk-brass, #9a6b2f) 40%, transparent)",
        background: "color-mix(in srgb, var(--desk-brass, #9a6b2f) 6%, transparent)",
      }}
    >
      <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">风格信息已经足够，可以保存了</p>
      <Button size="sm" className="h-7 shrink-0 gap-1.5 rounded-xl px-3 text-xs" onClick={() => void handle()} disabled={committing}>
        <Save className="h-3.5 w-3.5" />
        {committing ? "保存中…" : "保存到我的风格"}
      </Button>
    </div>
  );
}

function UserBubble({ message }: { message: StyleChatMessage }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-2xl rounded-br-md bg-muted px-3.5 py-2.5 text-sm leading-relaxed text-foreground">
        {message.content}
      </div>
    </div>
  );
}

function AssistantBubble({ message }: { message: StyleChatMessage }) {
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(message.content);
      toast.success("已复制");
    } catch {
      toast.error("复制失败");
    }
  };
  return (
    <div className="group/msg flex items-start gap-2.5">
      <Lumi state={message.revealing ? "writing" : "idle"} size={26} className="mt-0.5 text-foreground" />
      <div className="min-w-0 max-w-[92%]">
        {/* 对话气泡是纯文本：markdown/JSON 已在 store 层净化，不用文章排版渲染 */}
        <div className="whitespace-pre-wrap rounded-2xl rounded-tl-md border bg-background px-3.5 py-2.5 text-sm leading-relaxed">
          {message.content}
        </div>
        {!message.revealing && message.content && (
          <div className="mt-1 flex gap-1 opacity-0 transition-opacity group-hover/msg:opacity-100">
            <CopyAction onClick={copy} label="复制回复" icon={<Copy className="h-3 w-3" />} />
          </div>
        )}
      </div>
    </div>
  );
}

function CopyAction({ onClick, label, icon }: { onClick: () => void; label: string; icon: React.ReactNode }) {
  const [copied, setCopied] = React.useState(false);
  return (
    <button
      onClick={() => {
        onClick();
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
      className="flex h-6 items-center gap-1 rounded-md px-1.5 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
      aria-label={label}
      title={label}
    >
      {copied ? <Check className="h-3 w-3 text-[color:var(--desk-proof)]" /> : icon}
    </button>
  );
}

export function StyleAssistantDialog() {
  const open = useStyleChatStore((s) => s.open);
  const messages = useStyleChatStore((s) => s.messages);
  const phase = useStyleChatStore((s) => s.phase);
  const ready = useStyleChatStore((s) => s.ready);
  const input = useStyleChatStore((s) => s.input);
  const error = useStyleChatStore((s) => s.error);
  const setOpen = useStyleChatStore((s) => s.setOpen);
  const setInput = useStyleChatStore((s) => s.setInput);
  const reset = useStyleChatStore((s) => s.reset);
  const send = useStyleChatStore((s) => s.send);
  const commit = useStyleChatStore((s) => s.commit);

  const busy = phase === "thinking" || phase === "writing";
  const inputRef = React.useRef<HTMLTextAreaElement | null>(null);

  const handleSubmit = () => {
    if (!input.trim() || busy) return;
    void send(input);
  };

  const handleCommit = async () => {
    const ok = await commit();
    if (ok) toast.success("风格已保存");
    return ok;
  };

  // 打开后聚焦输入行（等 Dialog 完成挂载动画）
  React.useEffect(() => {
    if (!open) return;
    const t = setTimeout(() => inputRef.current?.focus(), 120);
    return () => clearTimeout(t);
  }, [open]);

  const lastUserIndex = React.useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) if (messages[i].role === "user") return i;
    return -1;
  }, [messages]);

  // 仅剩开场问候时显示建议卡；一旦开始对话就不再出现
  const showSuggestions = messages.length <= 1 && !busy;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="flex h-[640px] max-w-2xl flex-col gap-0 overflow-hidden p-0">
        {/* 「新对话」与全局关闭按钮并排（右上角） */}
        <button
          onClick={() => {
            reset();
            setTimeout(() => inputRef.current?.focus(), 50);
          }}
          className="absolute right-[3.25rem] top-3 z-10 flex h-8 items-center gap-1.5 whitespace-nowrap rounded-xl px-2 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
          title="开启新对话"
        >
          <RotateCcw className="h-3.5 w-3.5" />
          新对话
        </button>
        <DialogHeader className="border-b px-5 py-3.5">
          <DialogTitle className="flex items-center gap-2.5 text-base">
            <Lumi state={busy ? phase : "idle"} size={32} label="Lumi 风格助手" className="text-foreground" />
            <span>风格助手</span>
            <span className="rounded-full border bg-muted/50 px-2 py-0.5 text-xs font-normal text-muted-foreground">Lumi</span>
          </DialogTitle>
          <DialogDescription className="sr-only">与 Lumi 对话，共创你的写作风格</DialogDescription>
        </DialogHeader>

        <MessageScrollerProvider autoScroll defaultScrollPosition="last-anchor" scrollPreviousItemPeek={64}>
          <MessageScroller className="min-h-0 flex-1 bg-muted/20">
            <MessageScrollerViewport>
              <MessageScrollerContent className="gap-4 px-5 py-4">
                {messages.map((message, i) => (
                  <React.Fragment key={message.id}>
                    <MessageScrollerItem
                      messageId={message.id}
                      scrollAnchor={i === lastUserIndex}
                      className={showSuggestions && i === 0 ? "mt-auto" : undefined}
                    >
                      {message.role === "user" ? <UserBubble message={message} /> : <AssistantBubble message={message} />}
                    </MessageScrollerItem>
                    {/* 开场建议：信息流内左下角，无笔形头像 */}
                    {showSuggestions && i === 0 && (
                      <MessageScrollerItem messageId="style-chat-suggestions">
                        <div className="flex flex-wrap gap-1.5">
                          {SUGGESTIONS.map((s) => (
                            <button
                              key={s}
                              onClick={() => void send(s)}
                              className="rounded-full border bg-background px-2.5 py-1 text-xs text-muted-foreground transition-ui hover:bg-accent hover:text-foreground"
                            >
                              {s}
                            </button>
                          ))}
                        </div>
                      </MessageScrollerItem>
                    )}
                  </React.Fragment>
                ))}
                {error && (
                  <MessageScrollerItem messageId="style-chat-error">
                    <p className="text-xs text-destructive">{error}</p>
                  </MessageScrollerItem>
                )}
                {ready && (
                  <MessageScrollerItem messageId="style-chat-ready">
                    <ReadyCard onSave={handleCommit} />
                  </MessageScrollerItem>
                )}
              </MessageScrollerContent>
            </MessageScrollerViewport>
            <MessageScrollerButton />
          </MessageScroller>
        </MessageScrollerProvider>

        {/* 底部：输入行（与 composer 同视觉语言） */}
        <div className="border-t px-4 py-3">
          <div className="flex items-end gap-2 rounded-2xl border bg-background px-3 py-2 transition-shadow focus-within:shadow-[var(--shadow-composer-focus)]">
            <textarea
              ref={inputRef}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  handleSubmit();
                }
              }}
              rows={1}
              placeholder="描述你想要的风格，或粘贴一段喜欢的文字…"
              aria-label="向风格助手发送消息"
              className="max-h-28 min-h-6 flex-1 resize-none bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
            <button
              onClick={handleSubmit}
              disabled={!input.trim() || busy}
              className={cn(
                "flex h-8 w-8 shrink-0 items-center justify-center rounded-xl transition-transform-precise",
                input.trim() && !busy ? "bg-foreground text-background hover:scale-105 active:scale-95" : "bg-muted text-muted-foreground cursor-not-allowed"
              )}
              title="发送 (Enter)"
              aria-label="发送"
            >
              <PenLine className="h-4 w-4" />
            </button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
