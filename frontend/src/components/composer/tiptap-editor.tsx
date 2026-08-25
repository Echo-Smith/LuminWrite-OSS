/**
 * Tiptap 富文本编辑器 — 替代原生 textarea
 *
 * 功能：
 *   - 基于 ProseMirror 的富文本编辑（加粗/斜体/列表/引用等）
 *   - 输出纯文本用于发送 getText()，保留格式化能力
 *   - Enter 发送 / Shift+Enter 换行 / Cmd+Ctrl+Enter 发送
 *   - 自动高度适应（40px~200px）
 *   - 空内容时 placeholder
 */
import { useEditor, EditorContent } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import Placeholder from "@tiptap/extension-placeholder";
import CharacterCount from "@tiptap/extension-character-count";
import { forwardRef, useEffect, useImperativeHandle } from "react";

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
            "tiptap-content outline-none min-h-[44px] max-h-[200px] overflow-y-auto text-[15px] leading-[24px] px-4 pt-3 pb-1",
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
      <EditorContent editor={editor} />
    );
  }
);
