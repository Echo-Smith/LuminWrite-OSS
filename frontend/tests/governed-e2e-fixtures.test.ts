import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

type Scenario = {
  scenario_id: string;
  initial_artifacts: string[];
  stages: Array<{ id: string; outputs: string[] }>;
  required_validators: string[];
  allowed_quality_states: string[];
  blocking_cases: string[];
};

const load = (name: string): Scenario => JSON.parse(readFileSync(
  new URL(`../../specs/lcp/v1/fixtures/scenarios/${name}.json`, import.meta.url),
  "utf8",
)) as Scenario;

test("Task12 fixtures preserve one document lifecycle and three quality states", () => {
  for (const name of ["long-form-creation", "multi-material-synthesis", "faithful-rewrite"]) {
    const scenario = load(name);
    assert.ok(scenario.scenario_id.length > 0);
    assert.ok(scenario.initial_artifacts.includes("contract"));
    assert.ok(scenario.stages.some((stage) => stage.outputs.includes("full_draft")));
    assert.ok(scenario.stages.some((stage) => stage.outputs.includes("quality_report")));
    assert.deepEqual(scenario.allowed_quality_states, [
      "candidate_draft", "accepted_draft", "verified_deliverable",
    ]);
    assert.ok(scenario.required_validators.length >= 3);
    assert.ok(scenario.blocking_cases.length >= 3);
  }
});

test("material synthesis and faithful rewrite retain their fail-closed cases", () => {
  const synthesis = load("multi-material-synthesis");
  assert.ok(synthesis.initial_artifacts.includes("materials"));
  assert.ok(synthesis.blocking_cases.includes("source_conflict_requires_decision"));
  assert.ok(synthesis.blocking_cases.includes("material_integrity_failed"));

  const rewrite = load("faithful-rewrite");
  for (const blocker of ["new_fact", "meaning_changed", "locked_block_modified"]) {
    assert.ok(rewrite.blocking_cases.includes(blocker));
  }
});
