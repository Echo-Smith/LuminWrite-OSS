# Slim Docreader - Lightweight document parsing sidecar
# Replaces the 5.53GB wechatopenai/weknora-docreader image with ~200MB
# Backend communicates via simple TCP protocol: "PARSE <filepath>\n"
# Uses Alpine + pip for smaller image size
FROM alpine:3.20

# Use Aliyun mirror for faster builds in China
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.aliyun.com#g' /etc/apk/repositories

# Install Python 3, pip, and minimal system dependencies
RUN apk add --no-cache \
    python3 \
    py3-pip \
    libjpeg-turbo \
    libpng \
    mesa-gl \
    glib \
    libxml2 \
    libxslt \
    antiword \
    && pip3 install --no-cache-dir --break-system-packages \
    -i https://pypi.tuna.tsinghua.edu.cn/simple \
    "markitdown[docx,pdf,xls,xlsx,pptx]" \
    python-docx \
    openpyxl \
    pypdf \
    pdfplumber \
    beautifulsoup4 \
    lxml \
    pillow

WORKDIR /app
COPY docreader-slim.py /app/docreader-slim.py
RUN mkdir -p /tmp/docreader

EXPOSE 50051

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python3 -c "import socket; s=socket.socket(); s.settimeout(2); s.connect(('127.0.0.1', 50051)); s.close()" || exit 1

CMD ["python3", "/app/docreader-slim.py"]
