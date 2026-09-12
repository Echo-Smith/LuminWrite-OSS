# Scholar Worker — private-network only. Never publish a port to a public
# interface; the Go host (compose network) is the sole caller.
FROM python:3.12-slim AS builder

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1

# Build the wheel in an isolated stage; no compilers needed for stdlib-only.
WORKDIR /build
COPY pyproject.toml README.md ./
COPY src ./src
RUN pip wheel --no-deps --wheel-dir /wheels .

FROM python:3.12-slim

# Run as the unprivileged runtime user; worker performs no filesystem writes
# outside its own temp dir.
RUN useradd --system --create-home --shell /usr/sbin/nologin scholar
WORKDIR /app
COPY --from=builder /wheels/*.whl /tmp/lumin_scholar.whl
RUN pip install --no-cache-dir /tmp/lumin_scholar.whl && rm /tmp/lumin_scholar.whl

USER scholar

# Internal convention only: map this as a private compose port; do NOT declare
# a host-side published port for this container.
EXPOSE 8971

# Liveness probe (GET /healthz) never calls models or the network.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["python", "-c", "import urllib.request; urllib.request.urlopen('http://127.0.0.1:8971/healthz', timeout=2)"]

# Token comes from the orchestrator's secret store; server refuses to start
# without SCHOLAR_WORKER_TOKEN (fail closed).
CMD ["python", "-m", "lumin_scholar"]
