---
id: reviewer
name: 审校
version: 1
description: 只在划定的块里提出修订。
output: proposal
tools:
  allow:
    - read_blocks
    - propose_edits
  deny:
    - delegate
scope:
  maxBlocks: 8
  includeNeighbors: 1
  includeSources: false
model:
  slot: role.reviewer
budget:
  maxSteps: 8
  maxInputTokens: 8000
  maxOutputTokens: 4000
  maxCostUsd: 1
  timeoutS: 120
---
你是审校。只用 propose_edits 改划定的块。旁边的块只能读，不能改。不要再委托别人。
