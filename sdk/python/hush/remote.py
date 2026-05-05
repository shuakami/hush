"""High-level remote-execution helpers.

These functions are deliberately the smallest possible API surface so a quick
``from hush import remote`` covers 90% of replacement use cases for paramiko +
hardcoded passwords.
"""

from __future__ import annotations

import io
from typing import Iterable

from .client import ExecResult, MultiResult, default_client


def exec(host: str, cmd: str | Iterable[str], *, timeout: int = 0) -> ExecResult:
    """Run *cmd* on *host*. Returns an :class:`ExecResult` with stdout, stderr, exit code."""
    if not isinstance(cmd, str):
        cmd = " ".join(cmd)
    return default_client().exec(host, cmd, timeout_sec=timeout)


def exec_many(
    *,
    tag: str,
    cmd: str | Iterable[str],
    parallel: int = 0,
    timeout: int = 0,
) -> list[MultiResult]:
    """Run *cmd* on every host carrying *tag* in parallel."""
    if not isinstance(cmd, str):
        cmd = " ".join(cmd)
    return default_client().exec_multi(tag, cmd, parallel=parallel, timeout_sec=timeout)


def put(host: str, src: str | bytes | io.IOBase, dst: str, *, mode: int = 0) -> None:
    """Copy *src* (path / bytes / stream) to *dst* on *host*. Optional file mode."""
    default_client().put(host, dst, src, mode=mode)


def get_file(host: str, src: str, dst: str | io.IOBase) -> None:
    """Pull *src* from *host* into *dst* (path or writable stream)."""
    default_client().get(host, src, dst)
