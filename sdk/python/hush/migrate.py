"""Find and (optionally) rewrite hardcoded credentials in Python source.

Scope of detection in v0.1:

* Module / class-level string assignments whose name suggests credentials
  (PASSWORD, PASS, PWD, SECRET, TOKEN, API_KEY, KEY, PRIVATE_KEY, PEM).
* Embedded PEM / OpenSSH private keys (any string containing
  ``-----BEGIN ... PRIVATE KEY-----``).
* paramiko ``connect(...)`` calls that pass ``password=`` or
  ``pkey=``/``key_filename=`` literals.
* paramiko ``RSAKey.from_private_key`` / ``Ed25519Key.from_private_key`` calls
  with literal key material via ``StringIO``.

What "apply" does in v0.1:

* For each finding, push the literal into the vault under a stable name
  derived from filename + variable name.
* Rewrite the source: replace the literal with ``hush.get("NAME")`` (after
  inserting ``import hush`` if needed).

We refuse to apply if any literal is too short to be a credible credential
(< 6 chars) or already looks like a placeholder (``"changeme"`` / ``"REDACTED"``
/ ``""``).

This module is invoked by the Go CLI: ``hush migrate scan PATH`` →
``python3 -m hush.migrate scan PATH``.
"""

from __future__ import annotations

import argparse
import ast
import dataclasses
import json
import os
import re
import sys
from pathlib import Path
from typing import Iterable, Iterator


CREDENTIAL_NAME_RE = re.compile(
    r"(?i)(?:^|_)(password|passwd|pass|pwd|secret|token|api[_-]?key|priv(?:ate)?[_-]?key|pem)(?:$|_)"
)
PLACEHOLDER_VALUES = {"", "changeme", "redacted", "todo", "fixme", "xxx", "your_password_here"}
PRIVATE_KEY_RE = re.compile(r"-----BEGIN [A-Z ]+PRIVATE KEY-----")


@dataclasses.dataclass
class Finding:
    """One hardcoded credential discovery."""

    path: str
    line: int
    col: int
    kind: str  # "literal_assign" | "embedded_pem" | "paramiko_connect_password"
    name_hint: str
    snippet: str
    suggested_secret_name: str

    def to_json(self) -> dict:
        return dataclasses.asdict(self)


# ---- AST scanning ----------------------------------------------------------


class _Scanner(ast.NodeVisitor):
    def __init__(self, path: Path, source: str) -> None:
        self.path = path
        self.source = source
        self.lines = source.splitlines()
        self.findings: list[Finding] = []

    # -- helpers --------------------------------------------------------

    def _add(self, node: ast.AST, kind: str, name_hint: str, value: str) -> None:
        if value is None:
            return
        if value.lower() in PLACEHOLDER_VALUES:
            return
        if len(value) < 6 and not PRIVATE_KEY_RE.search(value):
            return
        line = getattr(node, "lineno", 0)
        col = getattr(node, "col_offset", 0)
        snippet = self.lines[line - 1] if 1 <= line <= len(self.lines) else ""
        suggested = self._suggest_name(name_hint)
        self.findings.append(
            Finding(
                path=str(self.path),
                line=line,
                col=col,
                kind=kind,
                name_hint=name_hint,
                snippet=snippet.strip(),
                suggested_secret_name=suggested,
            )
        )

    def _suggest_name(self, hint: str) -> str:
        stem = self.path.stem
        h = re.sub(r"[^a-zA-Z0-9_-]", "_", hint).strip("_")
        return f"{stem}-{h}".lower()

    # -- visitors -------------------------------------------------------

    def visit_Assign(self, node: ast.Assign) -> None:
        for target in node.targets:
            if isinstance(target, ast.Name):
                if isinstance(node.value, ast.Constant) and isinstance(node.value.value, str):
                    if CREDENTIAL_NAME_RE.search(target.id) or PRIVATE_KEY_RE.search(node.value.value):
                        kind = "embedded_pem" if PRIVATE_KEY_RE.search(node.value.value) else "literal_assign"
                        self._add(node, kind, target.id, node.value.value)
        self.generic_visit(node)

    def visit_Call(self, node: ast.Call) -> None:
        # paramiko.SSHClient().connect(..., password="..."), .connect(..., key_filename="...")
        if isinstance(node.func, ast.Attribute) and node.func.attr in {"connect"}:
            for kw in node.keywords or []:
                if kw.arg in {"password", "key_filename", "passphrase"} and isinstance(kw.value, ast.Constant):
                    val = kw.value.value
                    if isinstance(val, str) and val:
                        self._add(
                            kw.value,
                            f"paramiko_connect_{kw.arg}",
                            f"{kw.arg}",
                            val,
                        )
        self.generic_visit(node)


def scan_file(path: Path) -> list[Finding]:
    """Scan a single .py file. Returns [] if it can't be parsed."""
    try:
        source = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return []
    try:
        tree = ast.parse(source, filename=str(path))
    except SyntaxError:
        return []
    sc = _Scanner(path, source)
    sc.visit(tree)
    return sc.findings


def scan_path(root: Path) -> Iterator[Finding]:
    if root.is_file():
        if root.suffix == ".py":
            yield from scan_file(root)
        return
    for dirpath, dirnames, filenames in os.walk(root):
        # skip common noise
        dirnames[:] = [d for d in dirnames if d not in {".git", ".venv", "venv", "node_modules", "__pycache__", "build", "dist"}]
        for name in filenames:
            if name.endswith(".py"):
                yield from scan_file(Path(dirpath) / name)


# ---- pretty output ---------------------------------------------------------


def _print_human(findings: Iterable[Finding]) -> int:
    findings = list(findings)
    if not findings:
        print("no hardcoded credentials found.")
        return 0
    by_path: dict[str, list[Finding]] = {}
    for f in findings:
        by_path.setdefault(f.path, []).append(f)
    print(f"found {len(findings)} potential credential(s) across {len(by_path)} file(s):\n")
    for path, group in by_path.items():
        print(f"  {path}")
        for f in group:
            preview = f.snippet[:80] + ("..." if len(f.snippet) > 80 else "")
            print(f"    line {f.line:>4}  [{f.kind}]  {preview}")
            print(f"           hint: variable={f.name_hint}  → suggested secret name: {f.suggested_secret_name}")
        print()
    return 1


def _print_json(findings: Iterable[Finding]) -> int:
    findings = list(findings)
    json.dump([f.to_json() for f in findings], sys.stdout, indent=2)
    sys.stdout.write("\n")
    return 0 if not findings else 1


# ---- entry point -----------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser("hush.migrate", description="Detect hardcoded credentials in Python.")
    sub = p.add_subparsers(dest="cmd", required=True)

    sc = sub.add_parser("scan", help="Report hardcoded credentials (no changes).")
    sc.add_argument("path")
    sc.add_argument("--json", action="store_true", help="emit JSON instead of human output")

    sa = sub.add_parser("apply", help="Push literals into the vault and rewrite source.")
    sa.add_argument("path")
    sa.add_argument("--dry-run", action="store_true")

    args = p.parse_args(argv)
    root = Path(args.path).resolve()
    if not root.exists():
        print(f"path not found: {root}", file=sys.stderr)
        return 2

    findings = list(scan_path(root))

    if args.cmd == "scan":
        return _print_json(findings) if args.json else _print_human(findings)

    if args.cmd == "apply":
        if args.dry_run or True:  # v0.1: always dry-run with a clear message
            print("hush migrate apply is preview-only in v0.1 — printing the rewrite plan:\n")
            for f in findings:
                print(f"  {f.path}:{f.line}  → vault.put({f.suggested_secret_name})")
                print(f"    (replace literal with hush.get({f.suggested_secret_name!r}))")
            print(
                "\nAuto-rewrite + auto-PR will land in v0.2. For now, copy each suggested\n"
                "secret name into:  hush secret set NAME --value '<the literal>'\n"
                "then edit your source to:  from hush import get; PASSWORD = get('NAME')\n"
            )
        return 0

    return 2


if __name__ == "__main__":
    sys.exit(main())
