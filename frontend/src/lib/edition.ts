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

export const EDITION: Edition = "oss";

// 断言回联合类型：EDITION 在单条代码线内会被 TS 收窄为字面量，直接比较会触发 TS2367
export const IS_COMMERCIAL: boolean = (EDITION as Edition) === "commercial";
