"""Tiny ergonomic wrappers."""

from __future__ import annotations

import threading

from .client import default_client


_secret_cache: dict[str, str] = {}
_cache_lock = threading.Lock()


def get(name: str, *, refresh: bool = False) -> str:
    """Fetch a secret value from the vault.

    Cached in-process for the life of the interpreter (so importing this
    module 100 times doesn't generate 100 audit entries). Pass ``refresh=True``
    to force a re-fetch — useful right after a rotation in a long-running
    process.
    """
    if not refresh:
        with _cache_lock:
            if name in _secret_cache:
                return _secret_cache[name]
    val = default_client().get_secret(name)
    with _cache_lock:
        _secret_cache[name] = val
    return val
