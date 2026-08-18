/**
 * Service Worker — Web Push 通知处理
 *
 * 处理 push 事件（收到推送时）和 notificationclick 事件（点击通知时）。
 */

// ── Push 事件：收到推送时显示通知 ──────────────────────────
self.addEventListener("push", (event) => {
  let data = {};
  try {
    if (event.data) {
      data = event.data.json();
    }
  } catch (e) {
    // 如果不是 JSON，尝试文本
    data = { title: "通知", body: event.data ? event.data.text() : "" };
  }

  const title = data.title || "笔润智谈";
  const options = {
    body: data.body || "",
    icon: data.icon || "/icon-192.png",
    badge: data.badge || "/icon-96.png",
    data: data.data || { url: "/write" },
    requireInteraction: false,
    tag: "luminbuddy-push",
  };

  event.waitUntil(self.registration.showNotification(title, options));
});

// ── notificationclick 事件：点击通知时聚焦/打开页面 ────────
self.addEventListener("notificationclick", (event) => {
  event.notification.close();

  const targetUrl = (event.notification.data && event.notification.data.url) || "/write";

  event.waitUntil(
    (async () => {
      const allClients = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });

      // 如果已有打开的窗口，聚焦并导航
      for (const client of allClients) {
        if (client.url.includes(targetUrl) || "focused" in client) {
          client.focus();
          return;
        }
      }

      // 否则打开新窗口
      if (self.clients.openWindow) {
        await self.clients.openWindow(targetUrl);
      }
    })()
  );
});

// ── Service Worker 安装/激活 ───────────────────────────────
self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});
