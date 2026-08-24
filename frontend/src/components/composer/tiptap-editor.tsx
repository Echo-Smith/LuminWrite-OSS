/**
 * Tiptap 富文本编辑器 — 替代原生 textarea
 *
 * 功能：
 *   - 基于 ProseMirror 的富文本编辑（加粗/斜体/列表/引用等）
 *   - 输出纯文本用于发送 getText()，保留格式化能力
 *   - Enter 发送 / Shift+Enter 换行 / Cmd+Ctrl+Enter 发送
 *   - 自动高度适应（40px~200px）
 *   - 空内容时 placeholder
 *   - 工具栏：加粗/斜体/无序列表/有序列表/引用/撤销/重做
 */
import { useEditor, EditorContent, type Editor } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Placeholder from "@tiptap/extension-placeholder";
import CharacterCount from "@tiptap/extension-character-count";
import { forwardRef, useEffect, useImperativeHandle } from "react";
import { Bold, Italic, List, ListOrdered, Quote, Undo2, Redo2 } from "lucide-react";
import { cn } from "@/lib/utils";

export interface TiptapEditorHandle {
  focus: () => void;
  getText: () => string;
  clear: () => void;
  insertText: (text: string) => void;
}

interface TiptapEditorProps {
  placeholder?: string;
  onChange?: (text: string) => void;
  onSend?: () => void;
  editable?: boolean;
}

// ── 工具栏按钮 ──
function ToolbarButton({
  action,
  icon: Icon,
  label,
  isActive,
}: {
  action: () => void;
  icon: typeof Bold;
  label: string;
  isActive?: boolean;
}) {
  return (
    <button
      type="button"
      onMouseDown={(e) => {
        e.preventDefault(); // 防止编辑器失焦
        action();
      }}
      className={cn(
        "flex items-center justify-center h-6 w-6 rounded transition-ui",
        isActive
          ? "bg-accent text-foreground"
          : "text-muted-foreground hover:bg-accent/50 hover:text-foreground"
      )}
      title={label}
    >
      <Icon className="h-3.5 w-3.5" />
    </button>
  );
}

// ── 工具栏 ──
function Toolbar({ editor }: { editor: Editor }) {
  return (
    <div className="flex items-center gap-0.5 px-4 pt-2 anim-fade-in">
      <ToolbarButton
        action={() => editor.chain().focus().toggleBold().run()}
        icon={Bold}
        label="加粗 (Cmd+B)"
        isActive={editor.isActive("bold")}
      />
      <ToolbarButton
        action={() => editor.chain().focus().toggleItalic().run()}
        icon={Italic}
        label="斜体 (Cmd+I)"
        isActive={editor.isActive("italic")}
      />
      <div className="w-px h-4 bg-border/60 mx-1" />
      <ToolbarButton
        action={() => editor.chain().focus().toggleBulletList().run()}
        icon={List}
        label="无序列表"
        isActive={editor.isActive("bulletList")}
      />
      <ToolbarButton
        action={() => editor.chain().focus().toggleOrderedList().run()}
        icon={ListOrdered}
        label="有序列表"
        isActive={editor.isActive("orderedList")}
      />
      <ToolbarButton
        action={() => editor.chain().focus().toggleBlockquote().run()}
        icon={Quote}
        label="引用"
        isActive={editor.isActive("blockquote")}
      />
      <div className="w-px h-4 bg-border/60 mx-1" />
      <ToolbarButton
        action={() => editor.chain().focus().undo().run()}
        icon={Undo2}
        label="撤销 (Cmd+Z)"
      />
      <ToolbarButton
        action={() => editor.chain().focus().redo().run()}
        icon={Redo2}
        label="重做 (Cmd+Shift+Z)"
      />
    </div>
  );
}

export const TiptapEditor = forwardRef<TiptapEditorHandle, TiptapEditorProps>(
  function TiptapEditor(
    { placeholder = "", onChange, onSend, editable = true },
    ref
  ) {
    const editor = useEditor({
      extensions: [
        StarterKit.configure({
          heading: false,
          codeBlock: false,
          code: false,
          horizontalRule: false,
          paragraph: {
            HTMLAttributes: {
              class: "tiptap-p",
            },
          },
        }),
        Placeholder.configure({
          placeholder,
          emptyEditorClass: "is-editor-empty",
        }),
        CharacterCount,
      ],
      editable,
      onUpdate({ editor }) {
        onChange?.(editor.getText());
      },
      editorProps: {
        attributes: {
          class:
            "tiptap-content outline-none min-h-[40px] max-h-[200px] overflow-y-auto text-sm px-4 pt-3 pb-1",
          "aria-label": "写作输入框",
        },
        handleKeyDown(_view, event) {
          if (event.key !== "Enter") return false;

          // Cmd/Ctrl + Enter → 发送
          if (event.metaKey || event.ctrlKey) {
            event.preventDefault();
            onSend?.();
            return true;
          }

          // Shift + Enter → 换行（默认行为，不拦截）
          if (event.shiftKey) {
            return false;
          }

          // 纯 Enter → 发送
          event.preventDefault();
          onSend?.();
          return true;
        },
      },
    });

    // 同步 editable 状态
    useEffect(() => {
      if (editor) {
        editor.setEditable(editable);
      }
    }, [editor, editable]);

    // 暴露命令式 API 给父组件
    useImperativeHandle(
      ref,
      () => ({
        focus: () => editor?.chain().focus().run(),
        getText: () => editor?.getText() ?? "",
        clear: () => editor?.chain().focus().setContent("").run(),
        insertText: (text: string) =>
          editor?.chain().focus().insertContent(text).run(),
      }),
      [editor]
    );

    if (!editor) {
      return (
        <div className="min-h-[40px] px-4 pt-3 text-sm text-muted-foreground/60">
          {placeholder}
        </div>
      );
    }

    return (
      <>
        <Toolbar editor={editor} />
        <EditorContent editor={editor} />
      </>
    );
  }
);
