"""Real document parsing for the parse operation (T05).

Splits an untrusted document into bounded, hash-verified text blocks:

- PDF (application/pdf) via pypdf, page by page; a PDF with no embedded
  ``/Font`` resources is flagged ``likely_scanned=True`` (a scanned page
  carries images, not text-extractable fonts).
- TXT / Markdown split natively on blank lines into paragraph blocks; page is
  always ``None`` for these (the contract forbids fabricating page numbers —
  contracts.md §2: "不能给 TXT/摘要伪造 PDF 页码").

Blocks are bounded (~1200 code points target; overlapping is intentionally
omitted in v1) and capped per document: when the cap (MAX_BLOCKS_PER_DOC) is
hit the output is marked ``truncated`` and the ``complete`` coverage flag goes
false — the host propagates this into the pack's truncated/read_block_ids
coverage boundary (contracts.md §2).

Block ids are stable and deterministic: ``blk-<n>-<8 hex of text hash>`` so a
re-parse of identical content yields identical ids (cache reuse across runs
keeps working). Hashes are sha256 over the block's UTF-8 text
(``sha256:<64 hex>``), the same convention the Go host verifies.

Offsets are Unicode code points (Python string indices), left-closed
right-open, into the *extracted document text*; they satisfy
``document_text[start:end] == block.text`` by construction, and the reader
sub-checks ``quote == block_text[start:end]`` against block text.
"""

from __future__ import annotations

import hashlib
from typing import Any

#: Target block size (code points) for long paragraphs.
BLOCK_TARGET_CODEPOINTS = 1200
#: Hard cap on blocks per document; beyond this the parse is truncated.
MAX_BLOCKS_PER_DOC = 400
#: A split chunk below this many code points is merged into the previous
#: block instead of standing alone (avoids sliver blocks).
MIN_CHUNK_CODEPOINTS = 40

PARSER_VERSION = "parser/1"

PDF_MEDIA_TYPE = "application/pdf"
TEXT_MEDIA_TYPES = ("text/plain", "text/markdown")

BLOCK_HASH_PREFIX = "sha256:"


def block_hash(text: str) -> str:
    """sha256 over the block's UTF-8 bytes (cross-language convention)."""
    digest = hashlib.sha256(text.encode("utf-8")).hexdigest()
    return BLOCK_HASH_PREFIX + digest


def make_block_id(index: int, text: str) -> str:
    """Deterministic block id: ordinal + 8 hex of the text hash."""
    digest = hashlib.sha256(text.encode("utf-8")).hexdigest()[:8]
    return f"blk-{index:04d}-{digest}"


class ParseError(Exception):
    """Typed parse failure; mapped to an operation error by the caller."""

    def __init__(self, code: str, message: str, *, retryable: bool = False) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.retryable = retryable


# ---------------------------------------------------------------------------
# PDF extraction
# ---------------------------------------------------------------------------


def _extract_pdf_pages(data: bytes) -> tuple[list[str], bool]:
    """Extract per-page text. Returns (page_texts, likely_scanned).

    ``likely_scanned`` is True when the PDF has no /Font resources anywhere
    (its glyphs cannot be text-extracted, so the content is probably page
    images). A hard parse failure is a typed error, never a silent empty doc.
    """
    import io

    from pypdf import PdfReader

    try:
        reader = PdfReader(io.BytesIO(data), strict=False)
    except Exception as exc:  # noqa: BLE001 - pypdf raises several exception types
        raise ParseError("parse_pdf_invalid", f"document is not a readable PDF: {exc}") from exc

    try:
        page_texts: list[str] = []
        fonts_seen = 0
        for page in reader.pages:
            try:
                page_texts.append(page.extract_text() or "")
            except Exception:  # noqa: BLE001 - one bad page degrades to empty text
                page_texts.append("")
            try:
                resources = page.get("/Resources", None)
                if resources is not None and "/Font" in resources:
                    fonts_seen += 1
            except Exception:  # noqa: BLE001
                pass
    except Exception as exc:  # noqa: BLE001 - pypdf structure errors surface typed
        raise ParseError("parse_pdf_invalid", f"PDF structure unreadable: {exc}") from exc

    likely_scanned = fonts_seen == 0
    return page_texts, likely_scanned


# ---------------------------------------------------------------------------
# Shared bounding / block emission
# ---------------------------------------------------------------------------


def _emit_bounded_block(
    text: str,
    *,
    page: int | None,
    blocks: list[dict[str, Any]],
) -> bool:
    """Append one block dict; returns False when the per-document cap is hit.

    Long blocks are split at ~BLOCK_TARGET_CODEPOINTS on whitespace when the
    text carries any; a giant unbroken token is hard-split at the target.
    """
    chunks = _bound_chunks(text, BLOCK_TARGET_CODEPOINTS)
    for chunk in chunks:
        if len(blocks) >= MAX_BLOCKS_PER_DOC:
            return False
        blocks.append(
            {
                "block_id": make_block_id(len(blocks) + 1, chunk),
                "text": chunk,
                "page": page,
                "block_hash": block_hash(chunk),
            }
        )
    return True


def _bound_chunks(text: str, target: int) -> list[str]:
    """Split one source segment into <= target-code-point chunks."""
    if len(text) <= target:
        return [text] if text else []
    chunks: list[str] = []
    cursor = 0
    while cursor < len(text):
        window_end = min(cursor + target, len(text))
        if window_end < len(text):
            # Prefer a whitespace break within the tail of the window so the
            # split lands between words when the document allows it.
            window = text[cursor:window_end]
            break_at = window.rfind(" ")
            if break_at > MIN_CHUNK_CODEPOINTS:
                window_end = cursor + break_at + 1
        chunks.append(text[cursor:window_end])
        cursor = window_end
    return chunks


def _blocks_from_plain_text(
    document: str, *, hard_split: bool
) -> tuple[list[dict[str, Any]], bool]:
    """Segment a text/markdown document into bounded blocks.

    ``hard_split`` (PDF text layer) takes one segment per page line and never
    merges across pages; plain text/markdown takes one segment per blank-line
    paragraph. Segments longer than the target are split on whitespace.
    """
    segments: list[tuple[str, int | None]] = []
    if hard_split:
        # The caller passes PDF text with \f page separators: one segment per
        # page (bounded chunks never cross a page boundary).
        for index, page_text in enumerate(document.split("\f"), start=1):
            stripped = page_text.strip()
            if stripped:
                segments.append((stripped, index))
    else:
        for paragraph in document.split("\n\n"):
            stripped = paragraph.strip()
            if stripped:
                segments.append((stripped, None))

    blocks: list[dict[str, Any]] = []
    complete = True
    for text, page in segments:
        if not _emit_bounded_block(text, page=page, blocks=blocks):
            complete = False
            break
    return blocks, complete


def parse_document(data: bytes, media_type: str) -> dict[str, Any]:
    """Parse one document into the parse operation's outputs.

    Raises :class:`ParseError` for unreadable/unrelated byte payloads.
    """
    if media_type == PDF_MEDIA_TYPE:
        page_texts, likely_scanned = _extract_pdf_pages(data)
        # \f separates pages so blocks keep their true 1-based page numbers.
        document_text = "\f".join(page_texts)
        blocks, complete = _blocks_from_plain_text(document_text, hard_split=True)
        total_codepoints = len(document_text)
    elif media_type in TEXT_MEDIA_TYPES:
        try:
            document_text = data.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise ParseError(
                "parse_text_invalid", f"document is not valid UTF-8: {exc}"
            ) from exc
        blocks, complete = _blocks_from_plain_text(document_text, hard_split=False)
        likely_scanned = False
        total_codepoints = len(document_text)
    else:
        raise ParseError(
            "unsupported_media_type",
            f"media_type must be {', '.join((PDF_MEDIA_TYPE, *TEXT_MEDIA_TYPES))}",
        )

    warnings: list[str] = []
    if not blocks:
        warnings.append("document produced no text blocks")
    if likely_scanned:
        warnings.append(
            "PDF carries no /Font resources; likely a scanned document — "
            "text extraction may be empty"
        )
    truncated = not complete

    return {
        "blocks": blocks,
        "coverage": {
            "media_type": media_type,
            "parser_version": PARSER_VERSION,
            "total_blocks": len(blocks),
            "total_codepoints": total_codepoints,
            "complete": complete,
            "truncated": truncated,
            "likely_scanned": likely_scanned,
        },
        "warnings": warnings,
    }
