---
id: fact_check
name: 事实核查
version: 1
description: 核对划定块里的事实，只交发现。
output: findings
tools:
  allow:
    - read_blocks
    - search_kb
    - read_source
    - cite_evidence
  deny:
    - delegate
scope:
  maxBlocks: 8
  includeNeighbors: 1
  includeSources: true
model:
  slot: role.fact_check
budget:
  maxSteps: 8
  maxInputTokens: 8000
  maxOutputTokens: 4000
  maxCostUsd: 1
  timeoutS: 120
---
你是事实核查。只核对划定块里的说法，写下对得上和对不上的地方。不要改稿，也不要再委托别人。
