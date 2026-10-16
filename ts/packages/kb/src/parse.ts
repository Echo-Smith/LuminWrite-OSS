import { createRequire } from "node:module";
import { extractText, getDocumentProxy } from "unpdf";

const require = createRequire(import.meta.url);
const mammoth = require("mammoth") as {
  extractRawText(input: { buffer: Buffer }): Promise<{ value: string }>;
};

export class KbInputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "KbInputError";
  }
}

export async function parseUpload(input: {
  filename: string;
  mime?: string;
  bytes: Uint8Array;
}): Promise<{ title: string; body: string }> {
  const kind = detectKind(input.filename, input.mime);
  if (kind === "text" || kind === "markdown") {
    const body = decodeText(input.bytes);
    return { title: titleFrom(input.filename, body), body };
  }
  if (kind === "pdf") {
    const proxy = await getDocumentProxy(input.bytes);
    const extracted = await extractText(proxy, { mergePages: false });
    const pages = Array.isArray(extracted.text) ? extracted.text : [extracted.text];
    const body = pages
      .map((page) => page.trim())
      .filter((page) => page.length > 0)
      .join("\n\f\n");
    return { title: titleFrom(input.filename, body), body };
  }
  const extracted = await mammoth.extractRawText({ buffer: Buffer.from(input.bytes) });
  return { title: titleFrom(input.filename, extracted.value), body: extracted.value };
}

function detectKind(filename: string, mime?: string): "text" | "markdown" | "pdf" | "docx" {
  const lower = filename.toLowerCase();
  const type = mime?.toLowerCase() ?? "";
  if (type.includes("pdf") || lower.endsWith(".pdf")) return "pdf";
  if (type.includes("wordprocessingml") || type.includes("msword") || lower.endsWith(".docx")) {
    return "docx";
  }
  if (type.includes("markdown") || lower.endsWith(".md") || lower.endsWith(".markdown")) {
    return "markdown";
  }
  if (type.startsWith("text/") || lower.endsWith(".txt")) return "text";
  throw new KbInputError("只接受 MD、TXT、PDF、DOCX");
}

function decodeText(bytes: Uint8Array): string {
  return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
}

function titleFrom(filename: string, body: string): string {
  const heading = /^#{1,6}\s+(\S.*)$/m.exec(body);
  if (heading?.[1]) return heading[1].trim().slice(0, 200);
  const base = filename.replace(/\.[^.]+$/, "").trim();
  return base || "未命名资料";
}

export function normalizeBody(body: string): string {
  return body
    .replace(/\r\n/g, "\n")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}
