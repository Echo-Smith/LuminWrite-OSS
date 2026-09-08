"""F4 end-to-end payload-ceiling tests (review 2026-09-08): a full download
→ parse cycle with a document that the OLD 8 MiB transport cap would have
made unparseable.

Shape: a real loopback fixture HTTP server (conftest FixtureServer) serves a
generated ~7 MiB valid PDF; ``fetch_full_text`` downloads it through the real
constrained downloader (TEST-ONLY loopback IP policy); the fetched bytes then
travel through the worker's real HTTP layer (api.make_server on loopback,
with the production 40 MiB body cap) to the parse operation, and the parse
outputs are verified (blocks, page numbers, hash integrity). The companion
case proves a >25 MiB document is rejected by the per-file ceiling even
though the 40 MiB transport cap would still admit its base64 form.

All offline: the PDF is generated locally with pypdf, both servers listen on
127.0.0.1, and the download uses the documented TestOnlyIpPolicy injection.
"""

from __future__ import annotations

import base64
import hashlib
import io
import json
import threading
import time
import urllib.error
import urllib.request

import pytest

from lumin_scholar import downloader, parser
from lumin_scholar.api import DEFAULT_MAX_BODY_BYTES, ScholarWorkerAPI, make_server
from lumin_scholar.contracts import compute_input_hash
from lumin_scholar.operations import (
    MAX_DOCUMENT_BYTES,
    MAX_FETCH_SIZE_BYTES,
    build_handlers,
    default_worker_deps,
)


def _big_text_pdf(pages: int, pad_per_page: int, text: str) -> bytes:
    """A valid, font-bearing PDF of ~pages*pad_per_page bytes.

    Padding lives in content-stream comments (``% ...``): valid PDF that
    pypdf ignores during text extraction, so the fixture is big but cheap to
    parse.
    """
    from pypdf import PdfWriter
    from pypdf.generic import DictionaryObject, NameObject, StreamObject

    writer = PdfWriter()
    for _ in range(pages):
        page = writer.add_blank_page(width=612, height=792)
        resources = DictionaryObject()
        font = DictionaryObject(
            {NameObject("/Type"): NameObject("/Font"),
             NameObject("/Subtype"): NameObject("/Type1"),
             NameObject("/BaseFont"): NameObject("/Helvetica")}
        )
        resources[NameObject("/Font")] = DictionaryObject({NameObject("/F1"): font})
        page[NameObject("/Resources")] = resources
        content = (
            f"BT /F1 12 Tf 72 720 Td ({text}) Tj ET\n% {'x' * pad_per_page}\n"
        ).encode("latin-1")
        stream = StreamObject()
        stream.set_data(content)
        page[NameObject("/Contents")] = stream
    buffer = io.BytesIO()
    writer.write(buffer)
    return buffer.getvalue()


PAGE_TEXT = "Payload ceiling end-to-end fixture page."
# 8 pages x ~850 KB ≈ 6.8 MiB: comfortably above the old 8 MiB body cap's
# ~6 MiB base64 capacity, well under the 25 MiB design ceiling.
PDF_BYTES = _big_text_pdf(8, 850_000, PAGE_TEXT)


@pytest.fixture(scope="module")
def pdf_fixture_server():
    from conftest import FixtureServer

    routes = {
        "/paper.pdf": lambda _req: (
            200,
            {"Content-Type": "application/pdf"},
            PDF_BYTES,
        )
    }
    server = FixtureServer(routes)
    server.start()
    yield server
    server.stop()


@pytest.fixture(scope="module")
def worker_server():
    """Real worker HTTP layer on loopback with the production body cap and
    production handlers (parse touches no network, so default deps are
    offline-safe for the parse route)."""
    api = ScholarWorkerAPI("e2e-token", handlers=build_handlers(default_worker_deps()))
    server = make_server("127.0.0.1", 0, api)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield server
    server.shutdown()
    server.server_close()
    thread.join(timeout=5)


def _post_operation(server, operation: str, payload: dict) -> tuple[int, dict]:
    body = json.dumps(
        {
            "request_id": "req-e2e-ceiling-0001",
            "input_hash": compute_input_hash(payload),
            "operation_version": "1",
            "deadline_ms": int(time.time() * 1000) + 120_000,
            "payload": payload,
        }
    ).encode("utf-8")
    req = urllib.request.Request(
        f"http://127.0.0.1:{server.server_address[1]}/internal/v1/operations/{operation}",
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Authorization": "Bearer e2e-token",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        return exc.code, json.loads(exc.read().decode("utf-8"))


class TestDownloadParseEndToEnd:
    def test_seven_mib_pdf_downloads_and_parses_over_worker_http(
        self, pdf_fixture_server, worker_server, download_client, test_ip_policy
    ):
        assert 6 * 1024 * 1024 < len(PDF_BYTES) < 8 * 1024 * 1024

        # 1) fetch_full_text: real downloader over the real fixture server.
        result = downloader.constrained_download(
            download_client,
            pdf_fixture_server.base_url + "/paper.pdf",
            size_limit=MAX_FETCH_SIZE_BYTES,
            ip_policy=test_ip_policy,
        )
        assert result.media_type == "application/pdf"
        assert result.size_bytes == len(PDF_BYTES)
        assert result.content_hash == "sha256:" + hashlib.sha256(PDF_BYTES).hexdigest()

        # 2) parse through the worker's REAL HTTP layer (40 MiB cap): the
        # base64 document is ~9 MiB — the old 8 MiB cap would 413 here.
        document_b64 = base64.b64encode(result.content).decode("ascii")
        assert len(document_b64) > 8 * 1024 * 1024  # would not fit 8 MiB cap
        status, body = _post_operation(
            worker_server,
            "parse",
            {
                "document": document_b64,
                "media_type": "application/pdf",
                "parser_version": parser.PARSER_VERSION,
            },
        )
        assert status == 200, body
        assert "error" not in body

        # 3) blocks are page-bounded and hash-verified.
        blocks = body["outputs"]["blocks"]
        assert len(blocks) == 8
        for index, block in enumerate(blocks, start=1):
            assert block["page"] == index
            assert block["block_hash"] == (
                "sha256:" + hashlib.sha256(block["text"].encode("utf-8")).hexdigest()
            )
            assert PAGE_TEXT in block["text"]
        coverage = body["outputs"]["coverage"]
        assert coverage["media_type"] == "application/pdf"
        assert coverage["complete"] is True
        assert coverage["truncated"] is False

    def test_document_over_25_mib_rejected_despite_40_mib_transport_cap(
        self, worker_server
    ):
        """A 26 MiB document: its base64 (~34.7 MiB) still fits the 40 MiB
        transport cap, but the per-file ceiling rejects it — the transport
        limit must not raise the single-file limit."""
        oversized = b"%PDF-1.4\n" + b"x" * (26 * 1024 * 1024)
        document_b64 = base64.b64encode(oversized).decode("ascii")
        assert len(document_b64) < DEFAULT_MAX_BODY_BYTES

        status, body = _post_operation(
            worker_server,
            "parse",
            {
                "document": document_b64,
                "media_type": "application/pdf",
                "parser_version": parser.PARSER_VERSION,
            },
        )
        assert status == 422
        assert body["error"]["code"] == "document_too_large"

    def test_max_document_bytes_equals_design_cap(self):
        # The transport raise must not leak into the per-file ceiling.
        assert MAX_DOCUMENT_BYTES == 25 * 1024 * 1024
        assert MAX_DOCUMENT_BYTES == downloader.DEFAULT_SIZE_LIMIT
