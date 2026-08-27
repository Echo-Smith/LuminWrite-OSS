/**
 * AI 风格创建对话框 — 多轮对话生成自定义写作风格
 */
import { useState, useRef, useEffect, useMemo, type ReactNode } from "react";
import { Sparkles, Send, Loader2, Check, ChevronRight, Code2, Paperclip, X } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@/components/ui/collapsible";
import { useAuthStore } from "@/stores/auth-store";
import { MarkdownContent } from "@/components/assistant-ui/markdown-content";

interface Message {
  role: "user" | "assistant";
  content: string;
}

interface ParsedMessage {
  /** Natural-language text before the JSON block */
  text: string;
  /** Extracted JSON string (empty if none) */
  json: string;
  /** Pretty-printed JSON for display */
  prettyJson: string;
  /** Whether a JSON block was found */
  hasJson: boolean;
}

/**
 * Parse an assistant message into text + JSON parts.
 * Handles markdown-fenced (```json ... ```) and raw JSON objects.
 */
function parseAssistantMessage(content: string): ParsedMessage {
  let text = content;
  let json = "";

  // 1. Try markdown code block: ```json ... ``` or ``` ... ```
  const fenceMatch = content.match(/```(?:json)?\s*([\s\S]*?)```/);
  if (fenceMatch) {
    json = fenceMatch[1].trim();
    text = content.replace(fenceMatch[0], "").trim();
  } else {
    // 2. Try unfenced JSON object at the end of the message
    // Look for a { ... } block that spans to the end (or near end)
    const startIdx = content.search(/\n\s*\{[\s\S]/);
    if (startIdx >= 0) {
      const candidate = content.slice(startIdx).trim();
      if (candidate.startsWith("{") && candidate.endsWith("}")) {
        try {
          JSON.parse(candidate);
          json = candidate;
          text = content.slice(0, startIdx).trim();
        } catch {
          // Not valid JSON, leave as-is
        }
      }
    }
  }

  // Pretty-print the JSON for display
  let prettyJson = "";
  if (json) {
    try {
      prettyJson = JSON.stringify(JSON.parse(json), null, 2);
    } catch {
      prettyJson = json; // fallback to raw
    }
  }

  return { text, json, prettyJson, hasJson: !!json };
}

/**
 * Render an assistant message with optional collapsible JSON block.
 */
function AssistantMessage({ content }: { content: string }) {
  const parsed = useMemo(() => parseAssistantMessage(content), [content]);
  const [jsonOpen, setJsonOpen] = useState(false);

  return (
    <div className="space-y-2">
      {parsed.text && (
        <MarkdownContent content={parsed.text} />
      )}
      {parsed.hasJson && (
        <Collapsible open={jsonOpen} onOpenChange={setJsonOpen}>
          <CollapsibleTrigger asChild>
            <button
              className="flex items-center gap-1.5 rounded-md bg-zinc-900/80 dark:bg-zinc-800/80 px-2.5 py-1.5 text-xs font-medium text-zinc-300 hover:bg-zinc-800 dark:hover:bg-zinc-700/80 transition-colors"
              type="button"
            >
              <Code2 className="h-3.5 w-3.5 text-purple-400" />
              <span>风格配置 JSON</span>
              <ChevronRight
                className={`h-3.5 w-3.5 transition-transform ${jsonOpen ? "rotate-90" : ""}`}
              />
            </button>
          </CollapsibleTrigger>
          <CollapsibleContent>
            <pre
              className="mt-2 max-h-64 overflow-auto rounded-md bg-zinc-900 dark:bg-zinc-950 p-3 text-xs leading-relaxed"
            >
              <code className="font-mono text-zinc-300 whitespace-pre">
                {highlightJson(parsed.prettyJson)}
              </code>
            </pre>
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  );
}

/**
 * Simple JSON syntax highlighter — wraps keys, strings, numbers,
 * booleans and null in colored spans.
 */
function highlightJson(jsonStr: string): ReactNode {
  // Tokenize: keys ("...":), strings, numbers, booleans, null, punctuation
  const tokens: ReactNode[] = [];
  const regex = /("(?:[^"\\]|\\.)*"\s*:)|("(?:[^"\\]|\\.)*")|\b(true|false|null)\b|(-?\d+\.?\d*(?:[eE][+-]?\d+)?)|([{}[\],])/g;
  let lastIndex = 0;
  let key = 0;

  let match: RegExpExecArray | null;
  while ((match = regex.exec(jsonStr)) !== null) {
    // Push preceding whitespace/plain text
    if (match.index > lastIndex) {
      tokens.push(jsonStr.slice(lastIndex, match.index));
    }
    const [full, isKey, isString, isBool, isNum, isPunct] = match;
    if (isKey) {
      tokens.push(
        <span key={key++} className="text-purple-400">{full}</span>
      );
    } else if (isString) {
      tokens.push(
        <span key={key++} className="text-emerald-400">{full}</span>
      );
    } else if (isBool) {
      tokens.push(
        <span key={key++} className="text-orange-400">{full}</span>
      );
    } else if (isNum) {
      tokens.push(
        <span key={key++} className="text-blue-400">{full}</span>
      );
    } else if (isPunct) {
      tokens.push(
        <span key={key++} className="text-zinc-500">{full}</span>
      );
    } else {
      tokens.push(full);
    }
    lastIndex = regex.lastIndex;
  }
  // Push trailing text
  if (lastIndex < jsonStr.length) {
    tokens.push(jsonStr.slice(lastIndex));
  }
  return tokens;
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
  const [pendingFiles, setPendingFiles] = useState<File[]>([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
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
    if ((!input.trim() && pendingFiles.length === 0) || !sessionId || loading) return;

    const userMsg = input.trim() || (pendingFiles.length > 0 ? `上传了 ${pendingFiles.length} 个文件` : "");
    const filesToSend = [...pendingFiles];
    setPendingFiles([]);
    setInput("");
    setMessages((prev) => [...prev, { role: "user", content: userMsg + (filesToSend.length > 0 ? ` \n📎 ${filesToSend.map(f => f.name).join(", ")}` : "") }]);
    setLoading(true);

    try {
      let res: Response;
      if (filesToSend.length > 0) {
        // Multipart form with files
        const fd = new FormData();
        fd.append("message", userMsg);
        filesToSend.forEach((f) => fd.append("files", f));
        res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/messages`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}` },
          body: fd,
        });
      } else {
        // JSON request (no files)
        res = await fetch(`/api/v2/style-builder/sessions/${sessionId}/messages`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({ message: userMsg }),
        });
      }
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
                {msg.role === "assistant" ? (
                  <AssistantMessage content={msg.content} />
                ) : (
                  <p className="whitespace-pre-wrap">{msg.content}</p>
                )}
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

        {/* Pending files display */}
        {pendingFiles.length > 0 && (
          <div className="flex flex-wrap gap-1.5 px-1 pb-2">
            {pendingFiles.map((f, i) => (
              <div key={i} className="flex items-center gap-1.5 rounded-md bg-muted px-2 py-1 text-xs">
                <Paperclip className="h-3 w-3" />
                <span className="max-w-[150px] truncate">{f.name}</span>
                <button
                  onClick={() => setPendingFiles(pendingFiles.filter((_, idx) => idx !== i))}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <X className="h-3 w-3" />
                </button>
              </div>
            ))}
          </div>
        )}

        <div className="flex gap-2 pt-2 border-t">
          {/* File upload button */}
          <input
            ref={fileInputRef}
            type="file"
            className="hidden"
            multiple
            accept=".md,.txt,.markdown,.json,.csv,.yaml,.yml,.html,.htm,.zip"
            onChange={(e) => {
              const files = Array.from(e.target.files ?? []);
              if (files.length > 0) {
                setPendingFiles((prev) => [...prev, ...files]);
              }
              if (fileInputRef.current) fileInputRef.current.value = "";
            }}
          />
          <button
            onClick={() => fileInputRef.current?.click()}
            disabled={loading || ready}
            className="flex items-center justify-center h-9 w-9 rounded-md border border-input text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
            title="上传参考文章或 skill 文件"
          >
            <Paperclip className="h-4 w-4" />
          </button>
          <textarea
            value={input}
            onChange={(e) => {
              setInput(e.target.value);
              // 自动调整高度
              e.target.style.height = "auto";
              e.target.style.height = `${Math.min(e.target.scrollHeight, 120)}px`;
            }}
            onKeyDown={(e) => {
              // Enter 发送，Shift+Enter 换行，Ctrl/Cmd+Enter 也发送
              if (e.key === "Enter" && !e.shiftKey && !(e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                sendMessage();
              } else if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                sendMessage();
              }
              // Ctrl/Cmd+A 全选
              // Ctrl/Cmd+C 复制 — 浏览器默认行为
              // Tab 键插入缩进
              if (e.key === "Tab") {
                e.preventDefault();
                const textarea = e.currentTarget;
                const start = textarea.selectionStart;
                const end = textarea.selectionEnd;
                const newValue = input.substring(0, start) + "  " + input.substring(end);
                setInput(newValue);
                requestAnimationFrame(() => {
                  textarea.selectionStart = textarea.selectionEnd = start + 2;
                });
              }
            }}
            placeholder="描述你想要的写作风格...（Enter 发送，Shift+Enter 换行）"
            disabled={loading || ready}
            rows={1}
            className="flex-1 resize-none rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
          />
          {ready ? (
            <Button onClick={commit} disabled={committing} className="gap-1.5">
              {committing ? <Loader2 className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}
              保存风格
            </Button>
          ) : (
            <Button onClick={sendMessage} disabled={loading || (!input.trim() && pendingFiles.length === 0)} size="icon">
              <Send className="h-4 w-4" />
            </Button>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
