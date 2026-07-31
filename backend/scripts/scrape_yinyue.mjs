#!/usr/bin/env node
/**
 * 预设评论风格文章抓取 + 知识库导入脚本
 *
 * 用法:
 *   node backend/scripts/scrape_yinyue.mjs [--import]
 *
 * --import  抓取后自动通过 API 导入知识库（需要后端运行中）
 *
 * 不带 --import 时仅抓取文章列表，输出 JSON 到 stdout。
 *
 * 流程:
 *   1. 抓取栏目页 1-10，提取文章 URL
 *   2. 逐篇抓取文章正文
 *   3. 如指定 --import，调用 /api/v2/kb/manage 创建 KB，再逐篇导入
 */

const BASE_URL = "https://www.hangzhou.com.cn";
const COLUMN_URL = "/pinglun/node_152931.htm";
const API_BASE = process.env.API_BASE || "http://localhost:8080/api/v2";
const KB_ID = "yinyue";
const KB_NAME = "预设评论风格";
const KB_DESC = "杭州网「预设评论风格」时评专栏文章集 — 用于写作风格参考与知识检索";
const MAX_PAGES = 10;
const CONCURRENCY = 3;
const DELAY_MS = 500;

// ─── Helpers ───────────────────────────────────────────

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function fetchText(url) {
  const resp = await fetch(url, {
    headers: { "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36" },
  });
  if (!resp.ok) throw new Error(`HTTP ${resp.status} for ${url}`);
  return resp.text();
}

// ─── Step 1: Scrape article list from column pages ─────

function extractArticles(html) {
  const articles = [];
  // Match: <a href="...content_XXX.htm" ...>Title</a> followed by <a href="...content_XXX.htm" ...>Summary</a> and date
  // The HTML has pairs of links: first is title, second is summary, followed by a date
  const linkRe = /<a\s+href="([^"]*content_\d+\.htm)"[^>]*>\s*([^<]+)<\/a>/g;
  const dateRe = /(\d{4}-\d{2}-\d{2})/g;

  const links = [];
  let m;
  while ((m = linkRe.exec(html)) !== null) {
    links.push({ url: m[1].trim(), text: m[2].trim() });
  }

  // Extract dates
  const dates = [];
  let dm;
  while ((dm = dateRe.exec(html)) !== null) {
    dates.push(dm[1].trim());
  }

  // Links come in pairs: title + summary
  // Group by URL — the first occurrence of each URL is the title
  const seen = new Set();
  let dateIdx = 0;
  for (let i = 0; i < links.length; i++) {
    if (seen.has(links[i].url)) continue;
    seen.add(links[i].url);

    // Find the summary (next link with same URL)
    let summary = "";
    if (i + 1 < links.length && links[i + 1].url === links[i].url) {
      summary = links[i + 1].text;
    }

    const date = dateIdx < dates.length ? dates[dateIdx] : "";
    dateIdx++;

    articles.push({
      title: links[i].text,
      url: links[i].url,
      summary,
      date,
    });
  }
  return articles;
}

async function scrapeAllPages() {
  const all = [];
  const seen = new Set();

  // Page 1 has no suffix
  const pages = [COLUMN_URL];
  for (let i = 2; i <= MAX_PAGES; i++) {
    pages.push(`/pinglun/node_152931_${i}.htm`);
  }

  for (const page of pages) {
    const url = BASE_URL + page;
    process.stderr.write(`  [list] ${page} ... `);
    try {
      const html = await fetchText(url);
      const arts = extractArticles(html);
      let newCount = 0;
      for (const a of arts) {
        if (!seen.has(a.url)) {
          seen.add(a.url);
          all.push(a);
          newCount++;
        }
      }
      process.stderr.write(`${arts.length} found (${newCount} new)\n`);
    } catch (e) {
      process.stderr.write(`ERROR: ${e.message}\n`);
    }
    await sleep(DELAY_MS);
  }

  return all;
}

// ─── Step 2: Scrape article content ────────────────────

function extractContent(html) {
  // Remove script/style
  html = html.replace(/<script[^>]*>[\s\S]*?<\/script>/gi, "");
  html = html.replace(/<style[^>]*>[\s\S]*?<\/style>/gi, "");

  // Try to find article body — hangzhou.com.cn uses .TRS_Editor or .content
  let bodyMatch = html.match(/<div[^>]*class="[^"]*TRS_Editor[^"]*"[^>]*>([\s\S]*?)<\/div>\s*<div/i);
  if (!bodyMatch) {
    bodyMatch = html.match(/<div[^>]*class="[^"]*content[^"]*"[^>]*>([\s\S]*?)<\/div>/i);
  }
  if (!bodyMatch) {
    bodyMatch = html.match(/<div[^>]*id="[^"]*content[^"]*"[^>]*>([\s\S]*?)<\/div>/i);
  }

  let text = bodyMatch ? bodyMatch[1] : html;

  // Remove HTML tags
  text = text.replace(/<[^>]*>/g, "");
  // Decode entities
  text = text
    .replace(/&nbsp;/g, " ")
    .replace(/&ldquo;/g, "\u201c")
    .replace(/&rdquo;/g, "\u201d")
    .replace(/&mdash;/g, "\u2014")
    .replace(/&hellip;/g, "\u2026")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'");

  // Normalize whitespace
  text = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n").replace(/\t/g, " ");
  text = text.replace(/ {2,}/g, " ");
  text = text.replace(/\n{3,}/g, "\n\n");

  return text.trim();
}

async function scrapeArticleContent(article) {
  process.stderr.write(`  [article] ${article.title} ... `);
  try {
    const html = await fetchText(article.url);
    const content = extractContent(html);
    process.stderr.write(`${content.length} chars\n`);
    return { ...article, content };
  } catch (e) {
    process.stderr.write(`ERROR: ${e.message}\n`);
    return { ...article, content: "", error: e.message };
  }
}

async function scrapeAllArticles(articles) {
  const results = [];

  // Process in batches of CONCURRENCY
  for (let i = 0; i < articles.length; i += CONCURRENCY) {
    const batch = articles.slice(i, i + CONCURRENCY);
    const batchResults = await Promise.all(batch.map((a) => scrapeArticleContent(a)));
    results.push(...batchResults);
    await sleep(DELAY_MS);
  }

  return results;
}

// ─── Step 3: Import to KB via API ──────────────────────

async function api(path, method, body) {
  const resp = await fetch(`${API_BASE}${path}`, {
    method,
    headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  return resp.json();
}

async function ensureKB() {
  // Check if KB already exists
  const listResp = await api("/kb/kbs");
  const kbs = listResp?.data?.knowledge_bases || [];
  if (kbs.some((kb) => kb.id === KB_ID)) {
    process.stderr.write(`  [kb] KB "${KB_ID}" already exists\n`);
    return;
  }

  // Create KB
  const resp = await api("/kb/manage", "POST", {
    id: KB_ID,
    name: KB_NAME,
    description: KB_DESC,
  });
  if (resp?.success) {
    process.stderr.write(`  [kb] Created KB "${KB_ID}"\n`);
  } else {
    process.stderr.write(`  [kb] Create failed: ${JSON.stringify(resp)}\n`);
  }
}

async function importArticle(article) {
  if (!article.content || article.content.length < 50) {
    process.stderr.write(`  [skip] ${article.title} (content too short)\n`);
    return;
  }

  // Use URL import endpoint — backend will fetch, extract, chunk, and store
  const resp = await api("/kb/knowledge/url", "POST", {
    url: article.url,
    title: article.title,
    kb_id: KB_ID,
  });

  if (resp?.success) {
    process.stderr.write(`  [ok] ${article.title}\n`);
  } else {
    // Fallback: import text directly
    const resp2 = await api("/kb/knowledge", "POST", {
      title: article.title,
      content: article.content,
      kb_id: KB_ID,
    });
    if (resp2?.success) {
      process.stderr.write(`  [ok] ${article.title} (text import)\n`);
    } else {
      process.stderr.write(`  [fail] ${article.title}: ${JSON.stringify(resp2)}\n`);
    }
  }
}

async function importAllArticles(articles) {
  await ensureKB();

  let success = 0;
  let failed = 0;

  for (const a of articles) {
    await importArticle(a);
    if (a.content && a.content.length >= 50) success++;
    else failed++;
    await sleep(300);
  }

  process.stderr.write(`\n  Import complete: ${success} success, ${failed} failed\n`);
}

// ─── Main ──────────────────────────────────────────────

async function main() {
  const shouldImport = process.argv.includes("--import");

  process.stderr.write("=== 预设评论风格文章抓取 ===\n");
  process.stderr.write(`时间: ${new Date().toISOString()}\n\n`);

  process.stderr.write("Step 1: 抓取文章列表...\n");
  const articles = await scrapeAllPages();
  process.stderr.write(`\n共发现 ${articles.length} 篇文章\n\n`);

  process.stderr.write("Step 2: 抓取文章正文...\n");
  const full = await scrapeAllArticles(articles);
  const valid = full.filter((a) => a.content && a.content.length > 50);
  process.stderr.write(`\n成功抓取 ${valid.length}/${articles.length} 篇\n\n`);

  if (shouldImport) {
    process.stderr.write("Step 3: 导入知识库...\n");
    await importAllArticles(full);
  } else {
    // Output as JSON
    const output = {
      scraped_at: new Date().toISOString(),
      total: full.length,
      valid: valid.length,
      articles: full.map((a) => ({
        title: a.title,
        url: a.url,
        date: a.date,
        summary: a.summary,
        content_length: a.content?.length || 0,
        error: a.error,
      })),
    };
    console.log(JSON.stringify(output, null, 2));
    process.stderr.write(`\n使用 --import 参数可自动导入知识库\n`);
  }
}

main().catch((e) => {
  process.stderr.write(`Fatal error: ${e.message}\n`);
  process.exit(1);
});
