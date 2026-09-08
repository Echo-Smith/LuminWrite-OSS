The key/cert pair in this directory is a TEST-ONLY self-signed certificate for
the fake hostname ``papers.example.org`` (SAN: DNS:papers.example.org), used by
``tests/test_downloader_pinning.py`` to run the constrained downloader against
a local TLS fixture without any network access.

- Generated locally (OpenSSL, 2026-09-08); not copied from anywhere.
- Not a secret: the private key is committed on purpose so the fixture server
  can start without a generation step. It signs nothing but this fake name and
  must never be used outside ``tests/``.
