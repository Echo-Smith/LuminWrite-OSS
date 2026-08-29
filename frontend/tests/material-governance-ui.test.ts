import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const materialsTab = readFileSync(new URL("../src/components/topic/materials-tab.tsx", import.meta.url), "utf8");
const materialAPI = readFileSync(new URL("../src/lib/material-api.ts", import.meta.url), "utf8");
const types = readFileSync(new URL("../src/lib/types.ts", import.meta.url), "utf8");

test("material writing entry sends server-resolved references instead of preview content", () => {
  assert.match(materialsTab, /material_refs:/);
  assert.doesNotMatch(materialsTab, /getMaterialContent/);
  assert.doesNotMatch(materialsTab, /user_materials:/);
  assert.match(types, /material_refs\?:/);
});

test("material list presents truthful governance readiness", () => {
  assert.match(materialAPI, /pending_run_snapshot/);
  assert.match(materialsTab, /待运行快照/);
  assert.match(materialsTab, /兼容材料/);
});
