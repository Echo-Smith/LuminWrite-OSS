/**
 * Packages API — 下载安装体系（风格 / 技能 / 服务）
 *
 * 包安装到固定目录 data/packages/<kind>/<slug>/<version>/，注册信息见
 * installed_packages（docs/34）。安装不携带凭据：服务包只注册 MCP 服务器，
 * 密钥由用户在 MCP 服务界面单独配置。
 */
import { useAuthStore } from "@/stores/auth-store";

function authHeaders(): Record<string, string> {
  const token = useAuthStore.getState().token;
  return token ? { Authorization: `Bearer ${token}` } : {};
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...(init?.headers ?? {}),
    },
  });
  const body = await res.json().catch(() => ({}));
  if (!res.ok || body?.success === false) {
    const code = body?.error?.code ?? `http_${res.status}`;
    const message = body?.error?.message ?? "请求失败";
    const err = new Error(message) as Error & { code: string };
    err.code = code;
    throw err;
  }
  return body?.data as T;
}

// ─── 类型 ──────────────────────────────────────────────────

export type PackageKind = "style" | "skill" | "service";

export interface CatalogEntry {
  kind: PackageKind;
  slug: string;
  version: string;
  title: string;
  description: string;
}

export interface InstalledPackage {
  id: string;
  kind: PackageKind;
  slug: string;
  version: string;
  title: string;
  description: string;
  source: "builtin" | "url";
  install_path: string;
  manifest: Record<string, unknown>;
  installed_at: string;
  updated_at: string;
}

export interface InstallResult {
  package: InstalledPackage;
  style_profile_id?: string;
  mcp_server_id?: string;
}

// ─── 目录与安装 ────────────────────────────────────────────

export async function listCatalog(): Promise<CatalogEntry[]> {
  const data = await request<{ packages: CatalogEntry[] }>("/api/v2/packages/catalog");
  return data.packages ?? [];
}

export async function listInstalledPackages(kind?: PackageKind): Promise<InstalledPackage[]> {
  const query = kind ? `?kind=${kind}` : "";
  const data = await request<{ packages: InstalledPackage[] }>(`/api/v2/packages${query}`);
  return data.packages ?? [];
}

export async function installPackage(input: {
  source: "builtin" | "url";
  slug?: string;
  url?: string;
}): Promise<InstallResult> {
  return request<InstallResult>("/api/v2/packages/install", {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function uninstallPackage(id: string): Promise<void> {
  await request(`/api/v2/packages/${id}`, { method: "DELETE" });
}

// ─── 错误码文案 ────────────────────────────────────────────

export const PACKAGE_ERROR_LABELS: Record<string, string> = {
  already_installed: "该包已安装（需先卸载）",
  upstream_error: "包地址不可达或不安全（仅支持公网 http/https）",
  packages_unavailable: "本部署未开启包安装",
  install_failed: "安装失败",
};
