export { extractArticle, stripTags } from "./article.js";
export {
  canonicalUrl,
  createFeedService,
  type Feed,
  FeedError,
  type FeedService,
  importUrl,
  type PageFetch,
  type RefreshResult,
} from "./feeds.js";
export {
  defaultLookup,
  type LookupFn,
  SafeFetchError,
  type SafeFetchOptions,
  type SafeFetchResult,
  safeFetch,
} from "./safe-fetch.js";

export const phase = "rss" as const;
export const note =
  "safeFetch 拦私网和环回。RSS 按所有者入库，条目在该用户的全部会话和文档里可检索。开源版不带中文热榜。";
