"""LuminBuddy Scholar Worker — private-network bounded research operations.

This service implements the Python-side half of the research-review internal
contract (specs/research-review/contracts.md §4). It is deployed on a private
network only, authenticated with a shared service token, and exposes a fixed
whitelist of bounded operations. The Go host owns budgets, artifacts, and the
final transaction; this worker never persists product data.

All implementations in this tree are original work for LuminBuddy. No code,
prompts, fixtures, or test data are copied from the upstream AutoResearch
references (which are Proprietary).
"""

__version__ = "0.1.0"

SUPPORTED_OPERATION_VERSION = "1"
"""Envelope + mock payload contract version understood by this worker."""
