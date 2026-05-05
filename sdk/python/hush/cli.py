"""Tiny standalone Python CLI ("hush-py") for environments without the Go binary.

Most users want the real Go `hush` binary. This is a polyfill so a `pip install
hush-cli` user can do `python -m hush get NAME` without leaving the venv.
"""

from __future__ import annotations

import argparse
import sys

from .client import Client, HushError


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser("hush-py", description="Tiny Python CLI for Hush.")
    sub = p.add_subparsers(dest="cmd", required=True)

    sg = sub.add_parser("get", help="Print a secret value.")
    sg.add_argument("name")

    se = sub.add_parser("exec", help="Run a command on a host.")
    se.add_argument("host")
    se.add_argument("command", nargs=argparse.REMAINDER)

    sub.add_parser("ls", help="List secrets.")
    sub.add_parser("hosts", help="List hosts.")

    args = p.parse_args(argv)
    c = Client()
    try:
        if args.cmd == "get":
            sys.stdout.write(c.get_secret(args.name))
            return 0
        if args.cmd == "exec":
            cmd = " ".join(args.command).lstrip("- ")
            res = c.exec(args.host, cmd)
            sys.stdout.buffer.write(res.stdout)
            sys.stderr.buffer.write(res.stderr)
            return res.exit_code
        if args.cmd == "ls":
            for s in c.list_secrets():
                print(s["name"], s.get("version", ""))
            return 0
        if args.cmd == "hosts":
            for h in c.list_hosts():
                print(h["name"], h.get("transport", ""), h.get("address", ""))
            return 0
    except HushError as e:
        print(f"error: {e.message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
