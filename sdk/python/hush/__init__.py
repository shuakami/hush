"""Hush — credential & access broker client.

Public re-exports (lazy):

    from hush import remote, get
    import hush.paramiko_compat as paramiko

We intentionally lazy-load the modules that depend on requests so that
``python3 -m hush.migrate`` can run without optional deps installed.
"""

from __future__ import annotations

import importlib
from typing import Any

__version__ = "0.1.0"

__all__ = [
    "Client",
    "ExecResult",
    "MultiResult",
    "HushError",
    "get",
    "remote",
]


def __getattr__(name: str) -> Any:  # PEP 562
    if name in {"Client", "ExecResult", "MultiResult", "HushError"}:
        return getattr(importlib.import_module(".client", __name__), name)
    if name == "get":
        return importlib.import_module(".helpers", __name__).get
    if name == "remote":
        return importlib.import_module(".remote", __name__)
    raise AttributeError(f"module 'hush' has no attribute {name!r}")
