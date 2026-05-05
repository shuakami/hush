"""Low-level HTTP client. Mirrors internal/clientlib/clientlib.go but in Python.

Most users should reach for `hush.remote` or `hush.get` (which internally
build a `Client` from env / config). This module is documented because tests
and migration tooling poke at it directly.
"""

from __future__ import annotations

import io
import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Iterable, Mapping

import requests


class HushError(RuntimeError):
    """Server-side or transport-level error from a Hush HTTP call."""

    def __init__(self, status: int, message: str) -> None:
        super().__init__(f"hush server returned {status}: {message}")
        self.status = status
        self.message = message


@dataclass
class ExecResult:
    """Outcome of a single remote command."""

    exit_code: int
    stdout: bytes
    stderr: bytes
    duration_ms: int = 0

    @property
    def ok(self) -> bool:
        return self.exit_code == 0

    @property
    def stdout_text(self) -> str:
        return self.stdout.decode("utf-8", errors="replace")

    @property
    def stderr_text(self) -> str:
        return self.stderr.decode("utf-8", errors="replace")


@dataclass
class MultiResult:
    """One host's slice of a multi-host exec call."""

    host: str
    result: ExecResult | None = None
    error: str | None = None


def _b64_to_bytes(maybe: Any) -> bytes:
    """Server returns base64-encoded stdout/stderr in JSON. This decodes it.

    Falls back to empty bytes if the value is missing.
    """
    if maybe is None:
        return b""
    if isinstance(maybe, bytes):
        return maybe
    if isinstance(maybe, str):
        import base64

        try:
            return base64.b64decode(maybe)
        except Exception:
            return maybe.encode("utf-8")
    return b""


def _parse_exec_result(payload: Mapping[str, Any]) -> ExecResult:
    return ExecResult(
        exit_code=int(payload.get("exit_code", 0)),
        stdout=_b64_to_bytes(payload.get("stdout")),
        stderr=_b64_to_bytes(payload.get("stderr")),
        duration_ms=int(payload.get("duration_ms", 0)),
    )


def _load_config() -> tuple[str, str]:
    """Resolve (endpoint, api_key) from env first, ~/.hush/config second."""
    endpoint = os.environ.get("HUSH_ENDPOINT", "")
    api_key = os.environ.get("HUSH_API_KEY", "")
    if endpoint and api_key:
        return endpoint, api_key

    cfg_path = Path(os.environ.get("HUSH_CONFIG", str(Path.home() / ".hush" / "config")))
    if cfg_path.is_file():
        try:
            data = json.loads(cfg_path.read_text())
        except json.JSONDecodeError:
            data = {}
        endpoint = endpoint or data.get("endpoint", "")
        api_key = api_key or data.get("api_key", "")

    if not endpoint:
        endpoint = "http://127.0.0.1:8443"
    return endpoint, api_key


@dataclass
class Client:
    """HTTP client. Pass nothing to auto-detect from env / `~/.hush/config`."""

    endpoint: str = ""
    api_key: str = ""
    timeout: float = 300.0
    session: requests.Session = field(default_factory=requests.Session)

    def __post_init__(self) -> None:
        if not self.endpoint or not self.api_key:
            ep, ak = _load_config()
            self.endpoint = self.endpoint or ep
            self.api_key = self.api_key or ak
        self.endpoint = self.endpoint.rstrip("/")

    # ---- low-level helpers ------------------------------------------------

    def _headers(self) -> dict[str, str]:
        return {"Authorization": f"Bearer {self.api_key}"}

    def _request(
        self,
        method: str,
        path: str,
        *,
        json_body: Any | None = None,
        files: Mapping[str, Any] | None = None,
        data: Mapping[str, Any] | None = None,
        stream: bool = False,
    ) -> requests.Response:
        kwargs: dict[str, Any] = {
            "headers": self._headers(),
            "timeout": self.timeout,
        }
        if json_body is not None:
            kwargs["json"] = json_body
        if files is not None:
            kwargs["files"] = files
        if data is not None:
            kwargs["data"] = data
        if stream:
            kwargs["stream"] = True
        resp = self.session.request(method, f"{self.endpoint}{path}", **kwargs)
        if resp.status_code >= 400:
            try:
                msg = resp.json().get("error", "")
            except ValueError:
                msg = resp.text.strip()
            raise HushError(resp.status_code, msg or "")
        return resp

    # ---- secrets ----------------------------------------------------------

    def get_secret(self, name: str) -> str:
        return self._request("GET", f"/api/v1/secrets/{name}").json()["value"]

    def put_secret(self, name: str, value: str, metadata: Mapping[str, str] | None = None) -> dict:
        body: dict[str, Any] = {"value": value}
        if metadata:
            body["metadata"] = dict(metadata)
        return self._request("PUT", f"/api/v1/secrets/{name}", json_body=body).json()

    def list_secrets(self) -> list[dict]:
        return self._request("GET", "/api/v1/secrets").json().get("secrets", [])

    def delete_secret(self, name: str) -> None:
        self._request("DELETE", f"/api/v1/secrets/{name}")

    # ---- hosts ------------------------------------------------------------

    def get_host(self, name: str) -> dict:
        return self._request("GET", f"/api/v1/hosts/{name}").json()

    def list_hosts(self, tag: str = "") -> list[dict]:
        path = "/api/v1/hosts"
        if tag:
            path += f"?tag={tag}"
        return self._request("GET", path).json().get("hosts", [])

    def put_host(self, host: Mapping[str, Any]) -> None:
        name = host["name"]
        self._request("PUT", f"/api/v1/hosts/{name}", json_body=host)

    def delete_host(self, name: str) -> None:
        self._request("DELETE", f"/api/v1/hosts/{name}")

    # ---- exec / files -----------------------------------------------------

    def exec(self, host: str, command: str, *, timeout_sec: int = 0) -> ExecResult:
        body = {"host": host, "command": command, "timeout_sec": timeout_sec}
        return _parse_exec_result(self._request("POST", "/api/v1/exec", json_body=body).json())

    def exec_multi(
        self,
        selector: str,
        command: str,
        *,
        parallel: int = 0,
        timeout_sec: int = 0,
    ) -> list[MultiResult]:
        body = {
            "selector": selector,
            "command": command,
            "parallel": parallel,
            "timeout_sec": timeout_sec,
        }
        results = self._request("POST", "/api/v1/exec/multi", json_body=body).json().get("results", [])
        out: list[MultiResult] = []
        for r in results:
            res = None
            if r.get("result") is not None:
                res = _parse_exec_result(r["result"])
            out.append(MultiResult(host=r.get("host", ""), result=res, error=r.get("error")))
        return out

    def put(self, host: str, dst: str, src: io.IOBase | bytes | str, mode: int = 0) -> None:
        if isinstance(src, str):
            f: Any = open(src, "rb")  # noqa: SIM115 — we close in finally
            close = True
        elif isinstance(src, (bytes, bytearray)):
            f = io.BytesIO(bytes(src))
            close = True
        else:
            f = src
            close = False
        try:
            files = {"file": ("upload", f)}
            data: dict[str, Any] = {"host": host, "dst": dst}
            if mode:
                data["mode"] = format(mode, "o")
            self._request("POST", "/api/v1/files/put", files=files, data=data)
        finally:
            if close:
                f.close()

    def get(self, host: str, src: str, dst: io.IOBase | str) -> None:
        body = {"host": host, "src": src}
        resp = self._request("POST", "/api/v1/files/get", json_body=body, stream=True)
        if isinstance(dst, str):
            with open(dst, "wb") as f:
                for chunk in resp.iter_content(chunk_size=64 * 1024):
                    f.write(chunk)
        else:
            for chunk in resp.iter_content(chunk_size=64 * 1024):
                dst.write(chunk)

    # ---- audit ------------------------------------------------------------

    def audit_tail(self, limit: int = 100) -> list[dict]:
        return self._request("GET", f"/api/v1/audit?limit={limit}").json().get("records", [])

    def audit_verify(self) -> tuple[bool, str]:
        body = self._request("GET", "/api/v1/audit/verify").json()
        return bool(body.get("ok")), body.get("error", "")


# Module-level cached default client (lazy).

_default_client: Client | None = None


def default_client() -> Client:
    """Get / construct the singleton Client used by `hush.remote` and `hush.get`."""
    global _default_client
    if _default_client is None:
        _default_client = Client()
    return _default_client


def set_default_client(c: Client) -> None:
    """Override the default client (useful in tests)."""
    global _default_client
    _default_client = c


def _join_iter(parts: Iterable[str]) -> str:
    return " ".join(parts)
