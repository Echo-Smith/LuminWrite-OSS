"""
Slim Docreader — TCP server for document parsing.

Replaces the 5.53GB wechatopenai/weknora-docreader gRPC sidecar.
Implements the simple TCP protocol expected by backend file_parse.go:
  Request:  "PARSE <filepath>\n"
  Response: parsed text content (UTF-8, until connection close)

Supports: .pdf, .docx, .doc, .xlsx, .xls, .pptx, .ppt, .csv, .html, .md, .txt
"""

# On Alpine the interpreter is python3, on Debian it's python.
# The Dockerfile CMD uses the correct interpreter name.

import io
import logging
import os
import socketserver
import sys
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
    """Handle a single TCP connection: read PARSE command, respond with text."""

    def handle(self):
        try:
            line = self.rfile.readline()
            if not line:
                return

            cmd = line.decode("utf-8", errors="replace").strip()
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
    logger.info("Protocol: 'PARSE <filepath>\\n' -> text content")
    logger.info("Supported formats: pdf, docx, doc, xlsx, xls, pptx, ppt, csv, html, md, txt, json")

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        logger.info("Shutting down...")
        server.shutdown()


if __name__ == "__main__":
    main()
