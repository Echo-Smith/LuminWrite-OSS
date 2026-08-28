import { useMemo, useState } from "react";
import type { DocumentNode, Revision } from "@/lib/writing-runtime-types";
import { cn } from "@/lib/utils";

function nodeText(node: DocumentNode): string {
  return node.text ?? node.children.map(nodeText).join("");
}

function InlineNode({ node }: { node: DocumentNode }) {
  const children = node.children.map((child, index) => <InlineNode key={child.block_id ?? `${child.type}-${index}`} node={child} />);
  if (node.type === "text") return <>{node.text}</>;
  if (node.type === "strong") return <strong>{children}</strong>;
  if (node.type === "emphasis") return <em>{children}</em>;
  if (node.type === "link") return <a href={node.destination} rel="noreferrer" target="_blank">{children}</a>;
  if (node.type === "citation") return <sup data-source-id={node.source_id}>[{nodeText(node)}]</sup>;
  return <>{children}</>;
}

function EditableTextBlock({ node, className, onRevision }: { node: DocumentNode; className?: string; onRevision?: (revision: Revision) => void }) {
  const original = useMemo(() => nodeText(node), [node]);
  const [draft, setDraft] = useState(original);
  const canEdit = Boolean(onRevision && node.block_id && node.content_hash);

  if (!canEdit) return <div className={className}>{node.children.map((child, index) => <InlineNode key={child.block_id ?? index} node={child} />)}</div>;
  return (
    <div
      className={cn(className, "document-editable-block")}
      contentEditable
      suppressContentEditableWarning
      role="textbox"
      aria-label="编辑正文段落"
      onInput={(event) => setDraft(event.currentTarget.textContent ?? "")}
      onBlur={() => {
        const next = draft.trim();
        if (!next || next === original) return;
        onRevision?.({
          operation: "replace",
          target_block_id: node.block_id,
          target_hash: node.content_hash,
          node: {
            ...node,
            content_hash: undefined,
            children: [{ type: "text", attrs: {}, children: [], text: next, origin: { kind: "user", ref: "workspace-edit" } }],
          },
          reason: "用户在文档工作台直接编辑",
          semantic_impact: "局部正文替换，待验证后提交",
        });
      }}
    >
      {original}
    </div>
  );
}

export function DocumentBlock({ node, onRevision }: { node: DocumentNode; onRevision?: (revision: Revision) => void }) {
  const keyFor = (child: DocumentNode, index: number) => child.block_id ?? `${child.type}-${index}`;
  switch (node.type) {
    case "document":
      return <>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}</>;
    case "section": {
      const level = Number(node.attrs.level ?? 2);
      const title = String(node.attrs.title ?? "");
      const Heading = (level <= 2 ? "h2" : level === 3 ? "h3" : "h4") as "h2" | "h3" | "h4";
      return (
        <section className="document-section" data-block-id={node.block_id}>
          {title && <Heading>{title}</Heading>}
          {node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}
        </section>
      );
    }
    case "paragraph":
      return <EditableTextBlock node={node} className="document-paragraph" onRevision={onRevision} />;
    case "blockquote":
      return <blockquote>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}</blockquote>;
    case "code_block":
      return <pre><code>{nodeText(node)}</code></pre>;
    case "ordered_list":
      return <ol>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}</ol>;
    case "unordered_list":
      return <ul>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}</ul>;
    case "list_item":
      return <li>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} onRevision={onRevision} />)}</li>;
    case "table":
      return <table><tbody>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} />)}</tbody></table>;
    case "table_row":
      return <tr>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} />)}</tr>;
    case "table_cell":
      return <td>{node.children.map((child, index) => <DocumentBlock key={keyFor(child, index)} node={child} />)}</td>;
    default:
      return <InlineNode node={node} />;
  }
}
