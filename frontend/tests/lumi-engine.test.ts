import assert from "node:assert/strict";
import test from "node:test";

import {
  LUMI_PROFILE_SAMPLES,
  blendSamples,
  easeOutExpo,
  profileToPath,
  sampleLumi,
  staticSample,
} from "../src/lib/lumi/engine.ts";

test("pen profile is a sane radial silhouette", () => {
  const sample = staticSample("idle");
  assert.equal(sample.profile.length, LUMI_PROFILE_SAMPLES);
  for (const r of sample.profile) assert.ok(r > 0, "radius must be positive");
  const max = Math.max(...sample.profile);
  const min = Math.min(...sample.profile);
  // 钢笔细长：竖向半径明显大于横向
  assert.ok(max > 0.9, `max radius ${max}`);
  assert.ok(min < 0.2, `min radius ${min}`);
});

test("sampleLumi is a pure function of its inputs", () => {
  const a = sampleLumi("writing", 1234, 500);
  const b = sampleLumi("writing", 1234, 500);
  assert.deepEqual(a, b);
});

test("writing loop is continuous across the period wrap", () => {
  const period = 1600;
  const before = sampleLumi("writing", period - 1, 0);
  const after = sampleLumi("writing", period + 1, 0);
  assert.ok(Math.abs(before.rotation - after.rotation) < 0.5, `rotation wrap ${before.rotation} vs ${after.rotation}`);
  assert.ok(Math.abs(before.offsetY - after.offsetY) < 0.01);
  assert.ok(Math.abs(before.ink - after.ink) < 0.05);
});

test("thinking state is a breathing dot", () => {
  const s1 = sampleLumi("thinking", 0, 0);
  const s2 = sampleLumi("thinking", 450, 0);
  for (const r of s1.profile) assert.ok(Math.abs(r - s1.profile[0]) < 1e-9, "dot profile must be constant radius");
  // 呼吸在 ±5% 内
  const r1 = s1.profile[0];
  const r2 = s2.profile[0];
  assert.ok(r1 > 0.3 && r1 < 0.38 && r2 > 0.3 && r2 < 0.38);
  assert.ok(s1.pulse >= 0 && s1.pulse < 1);
});

test("done state pops the star then holds", () => {
  assert.equal(sampleLumi("done", 0, 50).star < 1, true);
  assert.equal(sampleLumi("done", 0, 1000).star, 1);
  assert.equal(sampleLumi("done", 0, 1000).pulse, -1);
});

test("error state topples the pen and settles", () => {
  const mid = sampleLumi("error", 0, 100);
  const settled = sampleLumi("error", 0, 1000);
  // 入场向 72° 倒下并稳定，不回弹
  assert.ok(mid.rotation > 0 && mid.rotation < 72, `mid rotation ${mid.rotation}`);
  assert.equal(settled.rotation, 72);
  assert.equal(settled.offsetY, 0.3);
  // 墨点涨出后收小但仍可见
  assert.ok(settled.ink > 0.5 && settled.ink < 1, `settled ink ${settled.ink}`);
  assert.ok(mid.ink > settled.ink, "墨点从涨出向收小过渡");
});

test("paused state lies the pen down with slow drift", () => {
  const mid = sampleLumi("paused", 0, 100);
  const settled = sampleLumi("paused", 0, 1200);
  assert.ok(mid.rotation > 0 && mid.rotation < 90, `mid rotation ${mid.rotation}`);
  assert.equal(settled.offsetY, 0.34);
  // 稳定后仍在 ±0.8° 内缓慢摆动（生动感，但不过冲）
  assert.ok(Math.abs(settled.rotation - 90) <= 0.8, `drift ${settled.rotation}`);
  assert.equal(settled.ink, 0);
});

test("blend endpoints reproduce their inputs", () => {
  const a = staticSample("idle");
  const b = staticSample("thinking");
  const atZero = blendSamples(a, b, 0);
  const atOne = blendSamples(a, b, 1);
  assert.deepEqual(atZero.profile, a.profile);
  assert.deepEqual(atOne.profile, b.profile);
  assert.equal(atOne.rotation, b.rotation);
});

test("easeOutExpo is monotonic and bounded", () => {
  let prev = 0;
  for (let i = 0; i <= 100; i++) {
    const v = easeOutExpo(i / 100);
    assert.ok(v >= prev && v <= 1);
    prev = v;
  }
  assert.equal(easeOutExpo(1), 1);
});

test("profileToPath emits a closed smooth path", () => {
  const d = profileToPath(staticSample("idle").profile, 20);
  assert.ok(d.startsWith("M "));
  assert.ok(d.includes(" Q "));
  assert.ok(d.endsWith(" Z"));
  for (const token of d.split(" ")) {
    const n = Number(token);
    assert.ok(Number.isFinite(n) || Number.isNaN(n) === false || token.startsWith("M") || token.startsWith("Q") || token === "Z");
  }
});
