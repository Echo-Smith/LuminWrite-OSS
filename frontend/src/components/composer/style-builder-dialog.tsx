/**
 * AI 风格创建对话框 — 多轮对话生成自定义写作风格
 */
import { useState, useRef, useEffect } from "react";
import { Sparkles, Send, Loader2, Check } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth-store";

interface Message {
  role: "user" | "assistant";
  content: string;
}

interface StyleBuilderDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreated?: () => void;
}

export function StyleBuilderDialog({ open, onOpenChange, onCreated }: StyleBuilderDialogProps) {
  const [sessionId, setSessionId] = useState<string | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(false);
  const [ready, setReady] = useState(false);
  const [committing, setCommitting] = useState(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const token = useAuthStore((s) => s.token);

  // eslint-disable-next-line react-hooks/exhaustive-deps -- createSession is stable enough; depends on open only
  useEffect(() => {
    if (open && !sessionId) {
      createSession();
    }
    if (!open) {
      setSessionId(null);
      setMessages([]);
      setInput("");
      setReady(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const createSession = async () => {
    try {
      const res = await fetch("/api/v2/style-builder/sessions", {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (json.success) {
        setSessionId(json.data.session_id);
        setMessages([
          {
            role: "assistant",
            content: "你好！我是写作风格配置助手。请描述你想要的写作风格，比如「我想写科技评论，要有深度但不枯燥」。",
          },
        ]);
      }
    } catch (e) {
      console.error("Failed to create session", e);
    }
  };

  const sendMessage = async () => {
    if (!input.trim() || !sessionId || loading) return;

    const userMsg = input.trim();
    setInput("");
    setMessages((prev) => [...prev, { role: "user", content: userMsg }]);
    setLoading(true);

    try {
      const res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/messages`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ message: userMsg }),
      });
      const json = await res.json();
      if (json.success) {
        setMessages((prev) => [...prev, { role: "assistant", content: json.data.message }]);
        if (json.data.ready) {
          setReady(true);
        }
      } else {
        setMessages((prev) => [...prev, { role: "assistant", content: "抱歉，出了点问题，请重试。" }]);
      }
    } catch (e) {
      setMessages((prev) => [...prev, { role: "assistant", content: "网络错误，请重试。" }]);
    } finally {
      setLoading(false);
    }
  };

  const commit = async () => {
    if (!sessionId || !ready) return;
    setCommitting(true);
    try {
      const res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/commit`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      const json = await res.json();
      if (json.success) {
        onCreated?.();
        onOpenChange(false);
      } else {
        setMessages((prev) => [...prev, { role: "assistant", content: `保存失败：${json.error?.message ?? "未知错误"}` }]);
      }
    } catch (e) {
      setMessages((prev) => [...prev, { role: "assistant", content: "网络错误，请重试。" }]);
    } finally {
      setCommitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl h-[600px] flex flex-col">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Sparkles className="h-5 w-5 text-purple-500" />
            AI 风格创建
          </DialogTitle>
        </DialogHeader>

        <div className="flex-1 overflow-y-auto space-y-4 px-1" ref={scrollRef}>
          {messages.map((msg, i) => (
            <div
              key={i}
              className={`flex ${msg.role === "user" ? "justify-end" : "justify-start"}`}
            >
              <div
                className={`max-w-[80%] rounded-lg px-4 py-2.5 text-sm ${
                  msg.role === "user"
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted"
                }`}
              >
                <p className="whitespace-pre-wrap">{msg.content}</p>
              </div>
            </div>
          ))}
          {loading && (
            <div className="flex justify-start">
              <div className="bg-muted rounded-lg px-4 py-2.5">
                <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
              </div>
            </div>
          )}
          {ready && (
            <div className="flex justify-center">
              <Badge variant="outline" className="gap-1.5 text-green-600 border-green-500/50">
                <Check className="h-3 w-3" />
                风格已就绪，可以保存
              </Badge>
            </div>
          )}
        </div>

        <div className="flex gap-2 pt-2 border-t">
          <Input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                sendMessage();
              }
            }}
            placeholder="描述你想要的写作风格..."
            disabled={loading || ready}
            className="flex-1"
          />
          {ready ? (
            <Button onClick={commit} disabled={committing} className="gap-1.5">
              {committing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
              保存风格
            </Button>
          ) : (
            <Button onClick={sendMessage} disabled={loading || !input.trim()} size="icon">
              <Send className="h-4 w-4" />
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
