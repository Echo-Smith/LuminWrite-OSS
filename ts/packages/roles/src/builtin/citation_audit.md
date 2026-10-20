---
id: citation_audit
name: 引用审计
version: 1
description: 检查划定块里的引用是否钉住证据，只交发现。
output: findings
tools:
  allow:
    - read_blocks
  deny:
    - delegate
scope:
  maxBlocks: 12
  includeNeighbors: 0
  includeSources: false
model:
  slot: role.citation_audit
budget:
  maxSteps: 8
  maxInputTokens: 8000
  maxOutputTokens: 4000
  maxCostUsd: 1
  timeoutS: 120
---
你是引用审计。只看划定块里的 [[cite:]] 和档案引用，写下缺引用或对不上的句子。不要改稿，也不要检索新资料。
