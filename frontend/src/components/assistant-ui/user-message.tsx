/**
 * 消息渲染 — 用户消息气泡
 */
import { User } from "lucide-react";
import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import type { ChatMessage } from "@/stores/agent-store";

export function UserMessage({ message }: { message: ChatMessage }) {
  const text = message.parts
    .filter((p) => p.type === "text")
    .map((p) => (p as { text: string }).text)
    .join("");

  return (
    <div className="flex justify-end gap-3 px-4 py-3 anim-fade-up">
      <div className="flex flex-col items-end gap-1 max-w-[75%]">
        <div className="rounded-2xl rounded-tr-sm bg-primary px-4 py-2.5 text-sm text-primary-foreground shadow-sm">
          <p className="whitespace-pre-wrap">{text}</p>
        </div>
      </div>
      <Avatar className="h-8 w-8">
        <AvatarFallback className="bg-muted text-foreground">
          <User className="h-4 w-4" />
        </AvatarFallback>
      </Avatar>
    </div>
  );
}
