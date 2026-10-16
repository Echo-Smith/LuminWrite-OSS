import { Readability } from "@mozilla/readability";
import { parseHTML } from "linkedom";

export function stripTags(html: string): string {
  return html
    .replace(/<script[\s\S]*?<\/script>/gi, " ")
    .replace(/<style[\s\S]*?<\/style>/gi, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&nbsp;/g, " ")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&#39;/g, "'")
    .replace(/&quot;/g, '"')
    .replace(/\s+/g, " ")
    .trim();
}

export function looksLikeHtml(body: string, contentType?: string): boolean {
  if (contentType?.includes("html")) return true;
  return /<html|<body|<p[\s>]|<article/i.test(body);
}

export function extractArticle(html: string): { title: string; text: string } {
  try {
    const { document } = parseHTML(html);
    const parsed = new Readability(document as never).parse();
    const text = parsed?.textContent?.trim() || stripTags(html);
    const title = parsed?.title?.trim() || "";
    return { title, text };
  } catch {
    return { title: "", text: stripTags(html) };
  }
}
