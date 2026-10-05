/**
 * 产品版本标识
 *
 * - "oss"        开源版：不含计费等商业功能（后端对应接口为 stub）
 * - "commercial" 商业版：包含完整商业功能（计费、支付等）
 *
 * 两条代码线分别固定自己的版本值；商业专属能力必须通过 IS_COMMERCIAL 门控，
 * 避免 OSS 端出现调用 503 stub 接口的入口。
 */
export type Edition = "oss" | "commercial";

// IS_COMMERCIAL 必须是字面量（true/false），不能写成
// `(EDITION as Edition) === "commercial"` 这类派生表达式：Rollup 的跨模块
// 内联只作用于字面量本身，派生常量不会被折叠，minifier 也就无法剔除
// 商业死分支——此前 747 行 billing.tsx 因此一直被打进 OSS bundle。
export const EDITION = "oss" as const;
export const IS_COMMERCIAL = false;
