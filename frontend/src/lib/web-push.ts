/**
 * Web Push 工具库
 *
 * 封装浏览器推送订阅/取消订阅逻辑。
 * 仅登录用户（非游客）可使用。
 */

import { useAuthStore } from "@/stores/auth-store";

// ─── 类型定义 ──────────────────────────────────────────────

interface PushSubscriptionKeys {
  p256dh: string;
  auth: string;
}

interface PushSubscriptionInfo {
  endpoint: string;
  keys: PushSubscriptionKeys;
}

interface VapidPublicKeyResponse {
  public_key: string;
}

// ─── API 调用 ──────────────────────────────────────────────

async function fetchVapidPublicKey(): Promise<string | null> {
  try {
    const res = await fetch("/api/v2/push/vapid-public-key");
    const json = await res.json();
    if (json.success && json.data?.public_key) {
      return json.data.public_key as string;
    }
    return null;
  } catch {
    return null;
  }
}

async function subscribeToServer(sub: PushSubscriptionInfo): Promise<boolean> {
  const token = useAuthStore.getState().token;
  try {
    const res = await fetch("/api/v2/push/subscribe", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify(sub),
    });
    const json = await res.json();
    return json.success === true;
  } catch {
    return false;
  }
}

async function unsubscribeFromServer(endpoint: string): Promise<boolean> {
  const token = useAuthStore.getState().token;
  try {
    const res = await fetch("/api/v2/push/unsubscribe", {
      method: "DELETE",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ endpoint }),
    });
    const json = await res.json();
    return json.success === true;
  } catch {
    return false;
  }
}

export async function sendTestPush(): Promise<{ ok: boolean; message: string }> {
  const token = useAuthStore.getState().token;
  try {
    const res = await fetch("/api/v2/push/test", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
    });
    const json = await res.json();
    if (json.success) {
      return { ok: true, message: `已发送 ${json.data?.sent ?? 0} 条测试通知` };
    }
    return { ok: false, message: json.error?.message ?? "发送失败" };
  } catch {
    return { ok: false, message: "网络错误" };
  }
}

// ─── Service Worker 注册 ──────────────────────────────────

let swRegistered: ServiceWorkerRegistration | null = null;

async function ensureServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) return null;

  if (swRegistered) return swRegistered;

  try {
    swRegistered = await navigator.serviceWorker.register("/sw.js", {
      scope: "/",
    });
    return swRegistered;
  } catch (err) {
    console.error("Service Worker registration failed:", err);
    return null;
  }
}

// ─── 公开方法 ─────────────────────────────────────────────

export type PushStatus = "unsupported" | "denied" | "not_configured" | "subscribed" | "unsubscribed";

export async function getPushStatus(): Promise<PushStatus> {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
    return "unsupported";
  }

  if (Notification.permission === "denied") {
    return "denied";
  }

  const vapidKey = await fetchVapidPublicKey();
  if (!vapidKey) {
    return "not_configured";
  }

  const reg = await ensureServiceWorker();
  if (!reg) return "unsupported";

  const existing = await reg.pushManager.getSubscription();
  return existing ? "subscribed" : "unsubscribed";
}

export async function subscribeToPush(): Promise<{ ok: boolean; message: string }> {
  // 1. Check browser support
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) {
    return { ok: false, message: "浏览器不支持推送通知" };
  }

  // 2. Request notification permission
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    return { ok: false, message: "通知权限被拒绝" };
  }

  // 3. Fetch VAPID public key
  const vapidKey = await fetchVapidPublicKey();
  if (!vapidKey) {
    return { ok: false, message: "推送服务未配置（缺少 VAPID 密钥）" };
  }

  // 4. Register Service Worker
  const reg = await ensureServiceWorker();
  if (!reg) {
    return { ok: false, message: "Service Worker 注册失败" };
  }

  // 5. Subscribe to push manager
  try {
    const sub = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(vapidKey) as BufferSource,
    });

    // 6. Send subscription to server
    const subInfo: PushSubscriptionInfo = {
      endpoint: sub.endpoint,
      keys: {
        p256dh: arrayBufferToBase64Url(sub.getKey("p256dh")),
        auth: arrayBufferToBase64Url(sub.getKey("auth")),
      },
    };

    const success = await subscribeToServer(subInfo);
    if (success) {
      return { ok: true, message: "推送通知已开启" };
    }
    // If server save failed, unsubscribe locally
    await sub.unsubscribe();
    return { ok: false, message: "保存订阅到服务器失败" };
  } catch (err) {
    return { ok: false, message: `订阅失败: ${err instanceof Error ? err.message : "未知错误"}` };
  }
}

export async function unsubscribeFromPush(): Promise<{ ok: boolean; message: string }> {
  if (!("serviceWorker" in navigator)) {
    return { ok: false, message: "浏览器不支持 Service Worker" };
  }

  const reg = await ensureServiceWorker();
  if (!reg) {
    return { ok: false, message: "Service Worker 未注册" };
  }

  const sub = await reg.pushManager.getSubscription();
  if (!sub) {
    return { ok: true, message: "无活跃订阅" };
  }

  const endpoint = sub.endpoint;
  await sub.unsubscribe();
  await unsubscribeFromServer(endpoint);

  return { ok: true, message: "推送通知已关闭" };
}

// ─── 工具函数 ─────────────────────────────────────────────

function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const rawData = atob(base64);
  const output = new Uint8Array(rawData.length);
  for (let i = 0; i < rawData.length; ++i) {
    output[i] = rawData.charCodeAt(i);
  }
  return output;
}

function arrayBufferToBase64Url(buffer: ArrayBuffer | null): string {
  if (!buffer) return "";
  const bytes = new Uint8Array(buffer);
  let binary = "";
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
