/**
 * 微博开放平台审核素材生成器
 * 生成应用图标 (16/80/120) 和介绍图片 (450x300 x3)
 * 
 * 运行: node generate-assets.js
 * 需要: npm install canvas
 */

const { createCanvas, registerFont } = require('canvas');
const fs = require('fs');
const path = require('path');

const OUT_DIR = __dirname;

// ── 颜色常量（匹配笔润智谈 V2 设计系统） ──
const C = {
  black: '#1a1a1a',
  darkGray: '#333333',
  midGray: '#666666',
  lightGray: '#e5e5e5',
  offWhite: '#fafafa',
  white: '#ffffff',
  accent: '#2d2d2d',
  warmBg: '#f7f6f3',
};

// ── 工具函数 ──
function saveCanvas(canvas, filename) {
  const buf = canvas.toBuffer('image/png');
  fs.writeFileSync(path.join(OUT_DIR, filename), buf);
  console.log(`  ✓ ${filename} (${canvas.width}x${canvas.height})`);
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.lineTo(x + w - r, y);
  ctx.quadraticCurveTo(x + w, y, x + w, y + r);
  ctx.lineTo(x + w, y + h - r);
  ctx.quadraticCurveTo(x + w, y + h, x + w - r, y + h);
  ctx.lineTo(x + r, y + h);
  ctx.quadraticCurveTo(x, y + h, x, y + h - r);
  ctx.lineTo(x, y + r);
  ctx.quadraticCurveTo(x, y, x + r, y);
  ctx.closePath();
}

// ═══════════════════════════════════════════
//  图标生成 — 笔尖 + 灯泡 Logo
// ═══════════════════════════════════════════
function drawLogoIcon(size, paddingRatio = 0.12) {
  const canvas = createCanvas(size, size);
  const ctx = canvas.getContext('2d');
  const pad = size * paddingRatio;
  const cx = size / 2;
  const cy = size / 2;
  const s = size - pad * 2; // 可用绘制区域

  // 背景（白色或透明）
  // ctx.clearRect(0, 0, size, size);

  // 笔尖主体
  ctx.fillStyle = C.black;
  ctx.strokeStyle = C.black;
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';

  const penW = s * 0.28;   // 笔尖宽度
  const penH = s * 0.55;   // 笔尖高度
  const tipY = cy + penH * 0.35;
  const topY = cy - penH * 0.5;

  // 绘制钢笔尖形状（简化几何）
  ctx.beginPath();
  // 从顶部中心开始
  ctx.moveTo(cx, topY);
  // 右上肩
  ctx.lineTo(cx + penW * 0.5, topY + penH * 0.2);
  // 右翼
  ctx.lineTo(cx + penW * 0.45, tipY - penH * 0.12);
  // 笔尖分叉右
  ctx.lineTo(cx + penW * 0.08, tipY - penH * 0.04);
  // 笔尖尖端右
  ctx.lineTo(cx + penW * 0.03, tipY);
  // 尖端中心
  ctx.lineTo(cx, tipY - penH * 0.02);
  // 笔尖尖端左
  ctx.lineTo(cx - penW * 0.03, tipY);
  // 笔尖分叉左
  ctx.lineTo(cx - penW * 0.08, tipY - penH * 0.04);
  // 左翼
  ctx.lineTo(cx - penW * 0.45, tipY - penH * 0.12);
  // 左上肩
  ctx.lineTo(cx - penW * 0.5, topY + penH * 0.2);
  ctx.closePath();
  ctx.fill();

  // 笔尖中线（装饰缝线）
  ctx.beginPath();
  ctx.moveTo(cx, topY + penH * 0.18);
  ctx.lineTo(cx, tipY - penH * 0.06);
  ctx.lineWidth = Math.max(1, size * 0.035);
  ctx.strokeStyle = C.white;
  ctx.stroke();

  // 灯泡火花（在笔尖上方）
  const sparkCy = topY - s * 0.04;
  const sparkR = s * 0.08;
  ctx.beginPath();
  ctx.arc(cx, sparkCy, sparkR, 0, Math.PI * 2);
  ctx.fillStyle = C.black;
  ctx.fill();

  // 火花内部高光点
  ctx.beginPath();
  ctx.arc(cx - sparkR * 0.2, sparkCy - sparkR * 0.2, sparkR * 0.3, 0, Math.PI * 2);
  ctx.fillStyle = C.white;
  ctx.fill();

  return canvas;
}

// ═══════════════════════════════════════════
//  介绍图片生成 (450x300)
// ═══════════════════════════════════════════
function drawIntroImage1() {
  /* 图片1: 主功能展示 — AI写作助手概览 */
  const W = 450, H = 300;
  const canvas = createCanvas(W, H);
  const ctx = canvas.getContext('2d');

  // 背景
  ctx.fillStyle = C.warmBg;
  ctx.fillRect(0, 0, W, H);

  // 左侧大区域：模拟编辑器界面
  const editorX = 30, editorY = 30, editorW = 260, editorH = 240;
  roundRect(ctx, editorX, editorY, editorW, editorH, 12);
  ctx.fillStyle = C.white;
  ctx.fill();
  ctx.strokeStyle = C.lightGray;
  ctx.lineWidth = 1;
  ctx.stroke();

  // 编辑器标题栏
  ctx.fillStyle = C.offWhite;
  roundRect(ctx, editorX, editorY, editorW, 32, [12, 12, 0, 0]);
  ctx.fill();

  // 标题栏圆点（红黄绿）
  [{ c: '#ff5f57', x: 16 }, { c: '#febc2e', x: 28 }, { c: '#28c840', x: 40 }].forEach(d => {
    ctx.beginPath(); ctx.arc(editorX + d.x, editorY + 16, 5, 0, Math.PI * 2); ctx.fillStyle = d.c; ctx.fill();
  });

  // 模拟文本行
  ctx.fillStyle = C.darkGray;
  let ty = editorY + 56;
  const lines = [
    { w: 180, h: 8, opacity: 0.9 },
    { w: 220, h: 8, opacity: 0.7 },
    { w: 160, h: 8, opacity: 0.5 },
    { w: 200, h: 8, opacity: 0.5 },
    { w: 140, h: 8, opacity: 0.3 },
  ];
  lines.forEach(l => {
    ctx.globalAlpha = l.opacity;
    roundRect(ctx, editorX + 20, ty, l.w, l.h, 4);
    ctx.fill();
    ty += 22;
  });
  ctx.globalAlpha = 1;

  // 光标闪烁效果
  ctx.fillStyle = C.black;
  roundRect(ctx, editorX + 20 + lines[4].w + 8, ty - 22, 2, 16, 1);
  ctx.fill();

  // 右侧面板：功能卡片
  const cardX = 310, cardStartY = 30;
  const cards = [
    { title: 'AI 续写', desc: '智能补全', icon: '✦' },
    { title: '热搜素材', desc: '实时热点', icon: '◉' },
    { title: '风格调整', desc: '多档可选', icon: '♢' },
    { title: '多平台', desc: '一键发布', icon: '▣' },
  ];

  cards.forEach((card, i) => {
    const cy = cardStartY + i * 58;
    roundRect(ctx, cardX, cy, 110, 48, 10);
    ctx.fillStyle = C.white;
    ctx.fill();
    ctx.strokeStyle = C.lightGray;
    ctx.lineWidth = 1;
    ctx.stroke();

    // 图标圆
    ctx.beginPath();
    ctx.arc(cardX + 26, cy + 24, 14, 0, Math.PI * 2);
    ctx.fillStyle = C.black;
    ctx.fill();

    // 文字
    ctx.fillStyle = C.black;
    ctx.font = 'bold 13px -apple-system, "PingFang SC", sans-serif';
    ctx.fillText(card.title, cardX + 48, cy + 20);
    ctx.fillStyle = C.midGray;
    ctx.font = '11px -apple-system, sans-serif';
    ctx.fillText(card.desc, cardX + 48, cy + 36);
  });

  // 底部品牌条
  ctx.fillStyle = C.black;
  roundRect(ctx, 0, H - 40, W, 40, [0, 0, 12, 12]);
  ctx.fill();
  ctx.fillStyle = C.white;
  ctx.font = 'bold 15px -apple-system, "PingFang SC", sans-serif';
  ctx.fillText('笔润智谈 V2', 24, H - 15);

  return canvas;
}

function drawIntroImage2() {
  /* 图片2: AI 写作能力展示 */
  const W = 450, H = 300;
  const canvas = createCanvas(W, H);
  const ctx = canvas.getContext('2d');

  // 深色背景（暗黑模式风格）
  ctx.fillStyle = '#1a1a1a';
  ctx.fillRect(0, 0, W, H);

  // 中央大标题区域
  ctx.fillStyle = C.white;
  ctx.font = 'bold 28px -apple-system, "PingFang SC", sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText('AI 驱动', W / 2, 60);
  ctx.fillText('智能写作', W / 2, 98);
  ctx.textAlign = 'left';

  // 三列能力展示
  const cols = [
    { label: '文章续写', sub: '理解上下文\n自然延展', color: '#3a3a3a' },
    { label: '润色改写', sub: '保留原意\n提升表达', color: '#3a3a3a' },
    { label: '灵感激发', sub: '打破瓶颈\n创意涌现', color: '#3a3a3a' },
  ];
  const colW = 120, colGap = 15, startX = (W - colW * 3 - colGap * 2) / 2;

  cols.forEach((col, i) => {
    const cx = startX + i * (colW + colGap);
    const cy = 130;

    // 卡片背景
    roundRect(ctx, cx, cy, colW, 120, 12);
    ctx.fillStyle = col.color;
    ctx.fill();

    // 编号圆
    ctx.beginPath();
    ctx.arc(cx + colW / 2, cy + 32, 18, 0, Math.PI * 2);
    ctx.fillStyle = C.white;
    ctx.fill();
    ctx.fillStyle = C.black;
    ctx.font = 'bold 16px -apple-system, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(String(i + 1), cx + colW / 2, cy + 38);

    // 标签
    ctx.fillStyle = C.white;
    ctx.font = 'bold 15px -apple-system, "PingFang SC", sans-serif';
    ctx.fillText(col.label, cx + colW / 2, cy + 72);

    // 副标签
    ctx.fillStyle = '#999999';
    ctx.font = '11px -apple-system, "PingFang SC", sans-serif';
    const subs = col.sub.split('\n');
    subs.forEach((s, si) => {
      ctx.fillText(s, cx + colW / 2, cy + 92 + si * 16);
    });
  });
  ctx.textAlign = 'left';

  // 底部标语
  ctx.fillStyle = '#666666';
  ctx.font = '13px -apple-system, "PingFang SC", sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText('让每一篇文章都熠熠生辉', W / 2, H - 30);

  return canvas;
}

function drawIntroImage3() {
  /* 图片3: 热搜 & 素材聚合 */
  const W = 450, H = 300;
  const canvas = createCanvas(W, H);
  const ctx = canvas.getContext('2d');

  // 浅暖背景
  ctx.fillStyle = C.offWhite;
  ctx.fillRect(0, 0, W, H);

  // 顶部标题区
  ctx.fillStyle = C.black;
  ctx.font = 'bold 22px -apple-system, "PingFang SC", sans-serif';
  ctx.fillText('全网热点 · 一手掌握', 30, 42);

  // 平台图标行
  const platforms = ['微博', '百度', '知乎', '抖音', 'B站', '头条'];
  const pxStart = 30, py = 70;
  platforms.forEach((p, i) => {
    const px = pxStart + i * 68;
    roundRect(ctx, px, py, 56, 28, 14);
    ctx.fillStyle = C.black;
    ctx.fill();
    ctx.fillStyle = C.white;
    ctx.font = '12px -apple-system, "PingFang SC", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(p, px + 28, py + 19);
  });
  ctx.textAlign = 'left';

  // 热搜列表模拟
  const hotItems = [
    { rank: 1, text: 'AI 写作助手成为创作者新工具', hot: '热', heat: 0.95 },
    { rank: 2, text: '内容创作行业迎来智能化变革', hot: '新', heat: 0.88 },
    { rank: 3, text: '微博热搜实时更新机制解读', hot: '沸', heat: 0.82 },
    { rank: 4, text: '自媒体人如何提高创作效率', heat: 0.72 },
    { rank: 5, text: '智能写作工具评测对比', heat: 0.65 },
  ];

  const listY = 115;
  hotItems.forEach((item, i) => {
    const ly = listY + i * 34;

    // 排名
    ctx.font = item.rank <= 3 ? 'bold 18px -apple-system, sans-serif' : 'bold 16px -apple-system, sans-serif';
    ctx.fillStyle = item.rank === 1 ? '#e54d42' : item.rank === 2 ? '#f0913b' : item.rank === 3 ? '#e8b934' : C.midGray;
    ctx.textAlign = 'center';
    ctx.fillText(String(item.rank), 30, ly + 12);
    ctx.textAlign = 'left';

    // 标题文字
    ctx.fillStyle = C.darkGray;
    ctx.font = '14px -apple-system, "PingFang SC", sans-serif';
    ctx.fillText(item.text, 58, ly + 12);

    // 热度标签
    if (item.hot) {
      const tagW = 22, tagH = 18;
      roundRect(ctx, 380, ly - 4, tagW, tagH, 4);
      ctx.fillStyle = item.hot === '热' ? '#ff6b35' : item.hot === '新' ? '#10b981' : '#ef4444';
      ctx.fill();
      ctx.fillStyle = C.white;
      ctx.font = 'bold 11px -apple-system, sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText(item.hot, 380 + tagW / 2, ly + 9);
      ctx.textAlign = 'left';
    }

    // 热度条
    const barW = 340, barH = 3;
    roundRect(ctx, 58, ly + 18, barW, barH, 2);
    ctx.fillStyle = C.lightGray;
    ctx.fill();
    roundRect(ctx, 58, ly + 18, barW * item.heat, barH, 2);
    ctx.fillStyle = item.rank <= 3 ? C.black : C.midGray;
    ctx.fill();
  });

  // 底部品牌
  ctx.fillStyle = C.black;
  roundRect(ctx, 0, H - 40, W, 40, [0, 0, 12, 12]);
  ctx.fill();
  ctx.fillStyle = C.white;
  ctx.font = 'bold 15px -apple-system, "PingFang SC", sans-serif';
  ctx.fillText('笔润智谈 V2 · 全网热点聚合', 24, H - 15);

  return canvas;
}

// ═══════════════════════════════════════════
//  主流程
// ═══════════════════════════════════════════
console.log('╔══════════════════════════════════════╗');
console.log('║  微博开放平台审核素材生成器          ║');
console.log('║  笔润智谈 V2                         ║');
console.log('╚══════════════════════════════════════╝\n');

console.log('── 生成应用图标 ──');
saveCanvas(drawLogoIcon(16, 0.15), 'icon-16.png');
saveCanvas(drawLogoIcon(80, 0.12), 'icon-80.png');
saveCanvas(drawLogoIcon(120, 0.10), 'icon-120.png');

console.log('\n── 生成介绍图片 (450×300) ──');
saveCanvas(drawIntroImage1(), 'intro-1-main.png');
saveCanvas(drawIntroImage2(), 'intro-2-ai-writing.png');
saveCanvas(drawIntroImage3(), 'intro-3-hot-topics.png');

console.log('\n✅ 所有素材已生成完成！');
console.log(`📁 输出目录: ${OUT_DIR}`);
