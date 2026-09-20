/**
 * 消息渲染 — 用户消息气泡（无头像）
 *
 * 对话框宽度自适应内容，最大不超过 75% 对话区宽度。
 */
import type { ChatMessage } from "@/lib/writing-runtime-types";

export function UserMessage({ message }: { message: ChatMessage }) {
  const text = message.parts
    .filter((p) => p.type === "text")
    .map((p) => (p as { text: string }).text)
    .join("");

  return (
    <div className="user-message-row flex justify-end anim-fade-up">
      <div className="user-message-bubble max-w-[75%] rounded-2xl rounded-tr-sm bg-primary px-4 py-2.5 text-sm text-primary-foreground">
        <p className="whitespace-pre-wrap break-words">{text}</p>
      </div>
    </div>
  );
}
