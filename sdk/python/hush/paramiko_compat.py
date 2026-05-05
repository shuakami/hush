"""Drop-in replacement for the most common paramiko patterns.

Usage::

    import hush.paramiko_compat as paramiko

    ssh = paramiko.SSHClient()
    ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())  # no-op
    ssh.connect("hk1")                                         # name from hush
    stdin, stdout, stderr = ssh.exec_command("ls /")
    print(stdout.read().decode())
    print(stdout.channel.recv_exit_status())
    ssh.close()

The shim resolves the first positional ``connect`` argument:

1. Exact match against a registered Hush host name (e.g. ``"hk1"``).
2. Exact match against any registered host's ``address`` field (e.g. an IP).
3. Otherwise, ``paramiko_compat.SSHException`` is raised with a clear message
   pointing the caller at ``hush host add``.

For SFTP, ``open_sftp().put(local, remote)`` and ``.get(remote, local)``
delegate to the Hush broker's file transfer endpoints. Most other paramiko
APIs (transport, channel, agent forwarding, ProxyCommand, etc.) are NOT
covered — the migration tool flags those for human review.
"""

from __future__ import annotations

import io
from typing import Any

from .client import Client, ExecResult, default_client


# ---- public exception types (mirror paramiko) ------------------------------


class SSHException(Exception):
    """Raised on broker resolution failure or remote error."""


class AuthenticationException(SSHException):
    """Compatibility shim. Most resolution problems map here."""


class BadHostKeyException(SSHException):
    """Compatibility shim — host keys are managed server-side now."""


# ---- policies (no-ops) -----------------------------------------------------


class _PolicyBase:
    def missing_host_key(self, client: "SSHClient", hostname: str, key: Any) -> None:  # noqa: D401
        return None


class AutoAddPolicy(_PolicyBase):
    pass


class WarningPolicy(_PolicyBase):
    pass


class RejectPolicy(_PolicyBase):
    pass


# ---- host resolver ---------------------------------------------------------


def _resolve_host(client: Client, candidate: str) -> str:
    """Map the first connect() positional arg to a registered Hush host name."""
    try:
        client.get_host(candidate)
        return candidate
    except Exception:
        pass

    try:
        hosts = client.list_hosts()
    except Exception as e:
        raise SSHException(f"hush server unreachable: {e}") from e

    for h in hosts:
        if h.get("address") == candidate:
            return h["name"]

    hint = (
        f"  hush host add <name> --transport ssh --address {candidate} \\\n"
        f"                        --user <ssh user> --auth-secret <secret name>"
    )
    raise SSHException(
        f"no registered Hush host matches {candidate!r}.\n"
        "Register the host first:\n" + hint
    )


# ---- output streams --------------------------------------------------------


class _Channel:
    """Minimal paramiko Channel facade. Currently exposes recv_exit_status only."""

    def __init__(self, exit_code: int) -> None:
        self._exit_code = exit_code

    def recv_exit_status(self) -> int:
        return self._exit_code

    @property
    def exit_status(self) -> int:
        return self._exit_code


class _OutStream(io.BytesIO):
    """BytesIO with a ``.channel`` attribute mirroring paramiko's output stream."""

    def __init__(self, data: bytes, channel: _Channel) -> None:
        super().__init__(data)
        self.channel = channel

    def __iter__(self):
        # paramiko output streams iterate by lines.
        for line in super().__iter__():
            yield line


class _InStream(io.BytesIO):
    """Stand-in for paramiko's stdin. Writes are accepted but dropped — exec is
    one-shot in v0.1, so writing more after the call has already returned has
    no effect on the remote side. The migration tool flags any code that
    actually relies on stdin streaming."""

    def write(self, data: bytes | str) -> int:  # type: ignore[override]
        if isinstance(data, str):
            data = data.encode("utf-8")
        return super().write(data)


# ---- SSHClient -------------------------------------------------------------


class SSHClient:
    """Stand-in for paramiko.SSHClient.

    `connect()` resolves the first arg to a Hush host. `exec_command()` runs
    via the broker. `open_sftp()` returns a thin SFTP-like facade that maps
    `put`/`get` to the broker's file endpoints.
    """

    def __init__(self) -> None:
        self._client: Client = default_client()
        self._host: str | None = None
        self._policy: _PolicyBase = RejectPolicy()

    # ---- paramiko surface area -------------------------------------------

    def set_missing_host_key_policy(self, policy: _PolicyBase) -> None:
        self._policy = policy

    def load_system_host_keys(self, *args: Any, **kwargs: Any) -> None:
        # No-op — host keys live in Hush's inventory now.
        return None

    def load_host_keys(self, *args: Any, **kwargs: Any) -> None:
        return None

    def connect(self, hostname: str, *args: Any, **kwargs: Any) -> None:  # noqa: ARG002
        # We deliberately ignore port/username/password/key_filename — the whole
        # point is that Hush owns those facts.
        self._host = _resolve_host(self._client, hostname)

    def exec_command(
        self,
        command: str,
        *_args: Any,
        **kwargs: Any,
    ) -> tuple[_InStream, _OutStream, _OutStream]:
        if self._host is None:
            raise SSHException("SSHClient.connect() must be called before exec_command()")
        timeout = int(kwargs.get("timeout") or 0)
        result: ExecResult = self._client.exec(self._host, command, timeout_sec=timeout)
        chan = _Channel(result.exit_code)
        return _InStream(), _OutStream(result.stdout, chan), _OutStream(result.stderr, chan)

    def open_sftp(self) -> "SFTPClient":
        if self._host is None:
            raise SSHException("SSHClient.connect() must be called before open_sftp()")
        return SFTPClient(self._client, self._host)

    def close(self) -> None:
        self._host = None

    def get_transport(self) -> Any:
        # Best-effort: most callers use this to check is_active(). Provide a
        # truthy stand-in.
        return _Transport(self._host is not None)

    def __enter__(self) -> "SSHClient":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()


class _Transport:
    def __init__(self, active: bool) -> None:
        self._active = active

    def is_active(self) -> bool:
        return self._active


# ---- SFTP ------------------------------------------------------------------


class SFTPClient:
    """Subset of paramiko.SFTPClient mapped to Hush's file endpoints."""

    def __init__(self, client: Client, host: str) -> None:
        self._client = client
        self._host = host

    def put(self, localpath: str, remotepath: str, callback: Any = None, confirm: bool = True) -> None:  # noqa: ARG002
        self._client.put(self._host, remotepath, localpath)

    def putfo(self, fl: io.IOBase, remotepath: str, file_size: int = 0, callback: Any = None, confirm: bool = True) -> None:  # noqa: ARG002
        self._client.put(self._host, remotepath, fl)

    def get(self, remotepath: str, localpath: str, callback: Any = None) -> None:  # noqa: ARG002
        self._client.get(self._host, remotepath, localpath)

    def getfo(self, remotepath: str, fl: io.IOBase, callback: Any = None) -> int:  # noqa: ARG002
        self._client.get(self._host, remotepath, fl)
        return -1  # paramiko returns total bytes; we don't have it cheaply

    def chmod(self, path: str, mode: int) -> None:
        # We don't expose remote chmod separately; piggyback on a tiny exec.
        result = self._client.exec(self._host, f"chmod {mode:o} {path}")
        if result.exit_code != 0:
            raise SSHException(f"chmod {path} failed: {result.stderr_text}")

    def close(self) -> None:
        return None

    def __enter__(self) -> "SFTPClient":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()


# ---- module-level conveniences -----------------------------------------


def Transport(*_args: Any, **_kwargs: Any) -> Any:
    """paramiko.Transport stand-in for code that uses it directly. Limited."""
    raise SSHException(
        "paramiko.Transport low-level transport is not supported by hush.paramiko_compat. "
        "Use hush.remote.exec / hush.paramiko_compat.SSHClient instead."
    )


__all__ = [
    "SSHClient",
    "SFTPClient",
    "AutoAddPolicy",
    "WarningPolicy",
    "RejectPolicy",
    "SSHException",
    "AuthenticationException",
    "BadHostKeyException",
    "Transport",
]
