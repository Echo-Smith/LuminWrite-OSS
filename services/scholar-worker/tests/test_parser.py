"""Offline tests for the real parse operation (T05).

Fixtures are hand-built minimal PDFs written directly with pypdf so no binary
blob is copied from anywhere (upstream AutoResearch is Proprietary; nothing
here is).
"""

from __future__ import annotations

import base64
import hashlib

import pytest

from lumin_scholar import parser
from lumin_scholar.operations import OperationError, run_operation

PARSER_VERSION = parser.PARSER_VERSION


def _b64(data: bytes) -> str:
    return base64.b64encode(data).decode("ascii")


def _b64s(text: str) -> str:
    return _b64(text.encode("utf-8"))


def _sha256(text: str) -> str:
    return "sha256:" + hashlib.sha256(text.encode("utf-8")).hexdigest()


def _minimal_pdf(pages: list[str], *, with_font: bool = True) -> bytes:
    """Build a tiny valid PDF with one page per entry using pypdf itself."""
    import io

    from pypdf import PdfWriter
    from pypdf.generic import (
        DictionaryObject,
        NameObject,
        RectangleObject,
    )

    writer = PdfWriter()
    for text in pages:
        page = writer.add_blank_page(width=200, height=200)
        if with_font:
            resources = DictionaryObject()
            font = DictionaryObject(
                {NameObject("/Type"): NameObject("/Font"),
                 NameObject("/Subtype"): NameObject("/Type1")}
            )
            resources[NameObject("/Font")] = DictionaryObject(
                {NameObject("/F1"): font}
            )
            page[NameObject("/Resources")] = resources
    buffer = io.BytesIO()
    writer.write(buffer)
    return buffer.getvalue()


def _parse_result(data: bytes, media_type: str):
    result = run_operation(
        "parse",
        {
            "document": _b64(data),
            "media_type": media_type,
            "parser_version": PARSER_VERSION,
        },
        handlers=_offline_handlers(),
    )
    return result


def _parse(data: bytes, media_type: str) -> dict:
    return _parse_result(data, media_type).outputs


def _offline_handlers():
    # parse touches no network; production wiring is fine.
    from lumin_scholar.operations import default_worker_deps, build_handlers
    return build_handlers(default_worker_deps())


class TestTxtParsing:
    def test_paragraph_offsets_and_hashes_consistent(self):
        document = "First paragraph text.\n\nSecond paragraph with 中文."
        outputs = _parse(document.encode("utf-8"), "text/plain")
        blocks = outputs["blocks"]
        assert len(blocks) >= 2
        for block in blocks:
            assert block["block_hash"] == _sha256(block["text"])
            assert block["page"] is None  # TXT must never fabricate pages
        assert outputs["coverage"]["total_codepoints"] == len(document)
        assert outputs["coverage"]["complete"] is True
        assert outputs["coverage"]["truncated"] is False
        assert outputs["coverage"]["likely_scanned"] is False

    def test_cjk_and_emoji_codepoint_counts(self):
        # 🚀 is one code point (4 UTF-8 bytes); the family emoji is 5 code points.
        document = "图表 🚀 与 👨‍👩‍👧 家族"
        outputs = _parse(document.encode("utf-8"), "text/plain")
        assert outputs["coverage"]["total_codepoints"] == len(document)
        text = "".join(block["text"] for block in outputs["blocks"])
        assert "🚀" in text and "👨‍👩‍👧" in text

    def test_markdown_media_type_supported(self):
        outputs = _parse("# Heading\n\nBody".encode("utf-8"), "text/markdown")
        assert outputs["coverage"]["media_type"] == "text/markdown"
        assert any("Heading" in block["text"] for block in outputs["blocks"])

    def test_block_ids_stable_across_reparses(self):
        data = "Hello world.\n\nSecond paragraph.".encode("utf-8")
        first = _parse(data, "text/plain")
        second = _parse(data, "text/plain")
        assert [b["block_id"] for b in first["blocks"]] == [
            b["block_id"] for b in second["blocks"]
        ]

    def test_long_document_truncated_at_block_cap(self):
        document = "\n\n".join(f"Paragraph {i} text." for i in range(600))
        outputs = _parse(document.encode("utf-8"), "text/plain")
        assert outputs["coverage"]["total_blocks"] == parser.MAX_BLOCKS_PER_DOC
        assert outputs["coverage"]["truncated"] is True
        assert outputs["coverage"]["complete"] is False

    def test_long_paragraph_split_near_target(self):
        document = "word " * 600  # 3000 code points
        outputs = _parse(document.encode("utf-8"), "text/plain")
        for block in outputs["blocks"]:
            assert len(block["text"]) <= parser.BLOCK_TARGET_CODEPOINTS
        assert " ".join("".join(b["text"] for b in outputs["blocks"]).split()) == document.strip()

    def test_invalid_utf8_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            _parse(b"\xff\xfe\xfa", "text/plain")
        assert excinfo.value.code == "parse_text_invalid"

    def test_unsupported_media_type_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            _parse(b"x", "application/json")
        assert excinfo.value.code == "unsupported_media_type"


class TestPdfParsing:
    def test_text_pdf_parses_with_pages(self):
        data = _minimal_pdf(["page one", "page two"])
        outputs = _parse(data, "application/pdf")
        assert outputs["coverage"]["likely_scanned"] is False
        assert outputs["coverage"]["complete"] is True
        pages = {block["page"] for block in outputs["blocks"]}
        assert all(isinstance(page, int) and page >= 1 for page in pages)

    def test_fontless_pdf_flagged_likely_scanned(self):
        data = _minimal_pdf(["scanned page"], with_font=False)
        result = _parse_result(data, "application/pdf")
        assert result.outputs["coverage"]["likely_scanned"] is True
        assert any("scanned" in warning for warning in result.warnings)

    def test_invalid_pdf_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            _parse(b"not a pdf at all", "application/pdf")
        assert excinfo.value.code == "parse_pdf_invalid"


class TestEnvelope:
    def test_unknown_field_rejected(self):
        with pytest.raises(OperationError):
            run_operation(
                "parse",
                {
                    "document": _b64s("x"),
                    "media_type": "text/plain",
                    "parser_version": PARSER_VERSION,
                    "extra": 1,
                },
                handlers=_offline_handlers(),
            )

    def test_missing_parser_version_rejected(self):
        with pytest.raises(OperationError):
            run_operation(
                "parse",
                {"document": _b64s("x"), "media_type": "text/plain"},
                handlers=_offline_handlers(),
            )

    def test_invalid_base64_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "parse",
                {
                    "document": "!!!not-base64!!!",
                    "media_type": "text/plain",
                    "parser_version": PARSER_VERSION,
                },
                handlers=_offline_handlers(),
            )
        assert excinfo.value.code == "invalid_document_encoding"

    def test_parser_version_mismatch_rejected(self):
        with pytest.raises(OperationError) as excinfo:
            run_operation(
                "parse",
                {
                    "document": _b64s("x"),
                    "media_type": "text/plain",
                    "parser_version": "parser/0",
                },
                handlers=_offline_handlers(),
            )
        assert excinfo.value.code == "unsupported_parser_version"
