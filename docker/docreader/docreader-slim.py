"""
Slim Docreader — TCP server for document parsing.

Replaces the 5.53GB wechatopenai/weknora-docreader gRPC sidecar.
Implements the simple TCP protocols expected by the backend:
  Request:  "PARSE <filepath>\n"
  Response: parsed text content (UTF-8, until connection close)
  Request:  "PARSEBYTES <size> <ext>\n" + exactly <size> raw bytes
  Response: parsed text content (UTF-8, until connection close)

PARSEBYTES keeps document bytes INSIDE the sidecar: the caller streams the
document over the socket and this container stages it in its own private
staging directory (DOCREADER_STAGING_DIR, mounted at /tmp/docreader in
compose). The caller's filesystem is never consulted, so no shared volume
between backend and docreader is needed — a path sent by the backend would
not exist inside this container (docker-compose.yml: backend mounts no
document volume).

Supports: .pdf, .docx, .doc, .xlsx, .xls, .pptx, .ppt, .csv, .html, .md, .txt
"""

# On Alpine the interpreter is python3, on Debian it's python.
# The Dockerfile CMD uses the correct interpreter name.

import io
import logging
import os
import re
import socketserver
import sys
import tempfile
import traceback
from pathlib import Path

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    stream=sys.stdout,
)
logger = logging.getLogger("docreader-slim")

# Try to import markitdown (lazy import in handler)
_markitdown = None

# Bounded inline-byte protocol: refuse documents above this cap before
# staging. The research path's own client-side design cap is 25 MiB
# (researchFetchSizeLimit); the sidecar keeps headroom above it.
MAX_INLINE_BYTES = int(os.environ.get("DOCREADER_MAX_INLINE_BYTES", str(32 * 1024 * 1024)))
# Private staging directory for inline bytes (compose mounts docreader_tmp
# here). Kept inside this container — never read caller-supplied paths.
STAGING_DIR = os.environ.get("DOCREADER_STAGING_DIR", "/tmp/docreader")
# The staged filename suffix must be a boring extension token; it feeds
# format detection only and never contains path separators.
_SUFFIX_RE = re.compile(r"^\.[a-z0-9]{1,10}$")

def get_markitdown():
    global _markitdown
    if _markitdown is None:
        try:
            from markitdown import MarkItDown
            _markitdown = MarkItDown()
        except Exception as e:
            logger.error("Failed to init MarkItDown: %s", e)
            raise
    return _markitdown


def read_exact(rfile, size: int) -> bytes:
    """Read exactly `size` bytes from the buffered socket file."""
    chunks = []
    remaining = size
    while remaining > 0:
        chunk = rfile.read(min(remaining, 1024 * 1024))
        if not chunk:
            raise IOError(f"client sent {size - remaining} of {size} bytes and disconnected")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def parse_inline(payload: bytes, suffix: str) -> str:
    """Stage inline bytes in the private staging dir, parse, and clean up."""
    path = None
    try:
        with tempfile.NamedTemporaryFile(dir=STAGING_DIR, prefix="inline-", suffix=suffix, delete=False) as staged:
            path = staged.name
            staged.write(payload)
        return parse_file(path)
    finally:
        if path:
            try:
                os.unlink(path)
            except OSError:
                pass


def parse_file(file_path: str) -> str:
    """Parse a file and return its text content as markdown."""
    path = Path(file_path)
    if not path.exists():
        return f"ERROR: File not found: {file_path}"

    ext = path.suffix.lower()

    # Direct text formats
    if ext in (".txt", ".md", ".markdown", ".csv", ".json", ".log", ".xml", ".yaml", ".yml"):
        try:
            return path.read_text(encoding="utf-8", errors="replace")
        except Exception as e:
            return f"ERROR: Failed to read text file: {e}"

    # HTML
    if ext in (".html", ".htm"):
        try:
            from bs4 import BeautifulSoup
            content = path.read_text(encoding="utf-8", errors="replace")
            soup = BeautifulSoup(content, "html.parser")
            for tag in soup(["script", "style"]):
                tag.decompose()
            text = soup.get_text(separator="\n", strip=True)
            return text or "ERROR: No text content extracted from HTML"
        except Exception as e:
            logger.warning("BeautifulSoup failed, trying markitdown: %s", e)

    # Old .doc format — use antiword
    if ext == ".doc":
        try:
            import subprocess
            result = subprocess.run(
                ["antiword", file_path],
                capture_output=True,
                text=True,
                timeout=60,
            )
            if result.returncode == 0 and result.stdout.strip():
                return result.stdout
            logger.warning("antiword failed (rc=%d), trying markitdown", result.returncode)
        except FileNotFoundError:
            logger.warning("antiword not installed, trying markitdown")
        except Exception as e:
            logger.warning("antiword error: %s, trying markitdown", e)

    # All other formats — use markitdown (docx, pdf, xlsx, pptx, etc.)
    try:
        md = get_markitdown()
        result = md.convert(file_path)
        content = result.text_content if hasattr(result, "text_content") else str(result)
        if content and content.strip():
            return content
        return f"ERROR: markitdown returned empty content for {file_path}"
    except Exception as e:
        logger.error("markitdown failed for %s: %s", file_path, e)
        logger.debug("Traceback: %s", traceback.format_exc())

        # Fallback: try direct read for any format
        try:
            raw = path.read_bytes()
            # Try to decode as text
            for enc in ("utf-8", "gbk", "latin-1"):
                try:
                    text = raw.decode(enc, errors="replace")
                    if len(text) > 100:
                        return text
                except Exception:
                    continue
        except Exception:
            pass

        return f"ERROR: Failed to parse {file_path}: {e}"


class DocreaderHandler(socketserver.StreamRequestHandler):
    """Handle a single TCP connection: read PARSE/PARSEBYTES, respond with text."""

    def handle(self):
        try:
            line = self.rfile.readline()
            if not line:
                return

            cmd = line.decode("utf-8", errors="replace").strip()
            if cmd.startswith("PARSEBYTES "):
                self._handle_inline(cmd)
                return
            if not cmd.startswith("PARSE "):
                self.wfile.write(b"ERROR: Unknown command\n")
                return

            file_path = cmd[len("PARSE "):].strip()
            logger.info("PARSE request: %s", file_path)

            content = parse_file(file_path)
            encoded = content.encode("utf-8")
            self.wfile.write(encoded)
            logger.info("PARSE response: %d bytes for %s", len(encoded), file_path)

        except Exception as e:
            logger.error("Handler error: %s", e)
            try:
                self.wfile.write(f"ERROR: {e}".encode("utf-8"))
            except Exception:
                pass

    def _handle_inline(self, cmd: str):
        """Bounded inline-byte request: 'PARSEBYTES <size> <.ext>' + raw bytes."""
        parts = cmd.split()
        if len(parts) < 2 or len(parts) > 3:
            self.wfile.write(b"ERROR: PARSEBYTES expects '<size> <.ext>'\n")
            return
        if not parts[1].isdigit():
            self.wfile.write(b"ERROR: PARSEBYTES size must be an integer\n")
            return
        size = int(parts[1])
        suffix = parts[2] if len(parts) == 3 else ".bin"
        if size <= 0 or size > MAX_INLINE_BYTES:
            self.wfile.write(
                f"ERROR: PARSEBYTES size {size} outside (0, {MAX_INLINE_BYTES}]\n".encode("utf-8"))
            return
        if not _SUFFIX_RE.match(suffix):
            self.wfile.write(b"ERROR: PARSEBYTES suffix must match .[a-z0-9]{1,10}\n")
            return

        payload = read_exact(self.rfile, size)
        logger.info("PARSEBYTES request: %d bytes as %s", size, suffix)
        content = parse_inline(payload, suffix)
        encoded = content.encode("utf-8")
        self.wfile.write(encoded)
        logger.info("PARSEBYTES response: %d bytes", len(encoded))


class ThreadedTCPServer(socketserver.ThreadingMixIn, socketserver.TCPServer):
    """Multi-threaded TCP server."""
    allow_reuse_address = True
    daemon_threads = True


def main():
    host = os.environ.get("DOCREADER_HOST", "0.0.0.0")
    port = int(os.environ.get("DOCREADER_PORT", "50051"))

    # Warm up markitdown to catch import errors early
    try:
        get_markitdown()
        logger.info("MarkItDown initialized successfully")
    except Exception as e:
        logger.warning("MarkItDown init failed (will retry on first request): %s", e)

    server = ThreadedTCPServer((host, port), DocreaderHandler)
    logger.info("Slim Docreader TCP server starting on %s:%d", host, port)
    logger.info("Protocol: 'PARSE <filepath>\\n' or 'PARSEBYTES <size> <ext>\\n'+bytes -> text content")
    logger.info("Inline-byte cap: %d bytes; staging dir: %s", MAX_INLINE_BYTES, STAGING_DIR)
    logger.info("Supported formats: pdf, docx, doc, xlsx, xls, pptx, ppt, csv, html, md, txt, json")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logger.info("Shutting down...")
        server.shutdown()


if __name__ == "__main__":
    main()
