"""Entry point: ``python -m lumin_scholar`` starts the worker HTTP server.

Environment:
- ``SCHOLAR_WORKER_TOKEN`` — required service-to-service bearer token. The
  server refuses to start (and rejects every request) without it.
- ``SCHOLAR_WORKER_HOST`` — bind host, default ``127.0.0.1``. Deployments set
  the private-network interface explicitly; the worker never binds a public
  address by default.
- ``SCHOLAR_WORKER_PORT`` — bind port, default ``8971`` (private convention).
"""

from __future__ import annotations

import os
import sys

from .api import ScholarWorkerAPI, make_server


def main() -> int:
    token = os.environ.get("SCHOLAR_WORKER_TOKEN", "")
    if not token:
        print(
            "lumin_scholar: SCHOLAR_WORKER_TOKEN is not set; refusing to start "
            "(the worker fails closed without a service token)",
            file=sys.stderr,
        )
        return 2

    host = os.environ.get("SCHOLAR_WORKER_HOST", "127.0.0.1")
    try:
        port = int(os.environ.get("SCHOLAR_WORKER_PORT", "8971"))
    except ValueError:
        print("lumin_scholar: SCHOLAR_WORKER_PORT must be an integer", file=sys.stderr)
        return 2

    api = ScholarWorkerAPI(token)
    server = make_server(host, port, api)
    print(
        f"lumin_scholar: listening on {host}:{port} "
        f"(private network only; operations: discover rank fetch_full_text parse read)",
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
