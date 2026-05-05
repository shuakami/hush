# hush-cli (Python SDK)

Python client for [Hush](https://github.com/shuakami/hush) — the credential &
access broker that lets you delete every hardcoded password, SSH key, and
ProxyJump chain from your `.py` files.

```bash
pip install hush-cli
```

## Three ways to use it

### 1. The `remote` API — what you should use in new code

```python
from hush import remote

remote.exec("hk1", "systemctl status uapipro-server")
remote.put("hk2", "./worker", "/opt/uapipro/worker", mode=0o755)
remote.exec_many(tag="hk", cmd="uname -a")    # multi-host parallel
```

`remote.exec` returns a small `ExecResult` namedtuple — `(exit_code, stdout, stderr)`.

### 2. The paramiko shim — what you use for legacy code

If you have 12 paramiko-based scripts, you don't want to rewrite them. One-line replacement:

```python
import hush.paramiko_compat as paramiko       # only line that changes

ssh = paramiko.SSHClient()
ssh.connect("hk1")                            # no host/user/password kwargs needed
stdin, stdout, stderr = ssh.exec_command("ls /")
```

The shim resolves `"hk1"` against your Hush server, runs the command remotely
via the broker, and emulates paramiko's `(stdin, stdout, stderr)` interface
well enough for the common `connect → exec_command → read` pattern.

For the SFTP path (`paramiko.SSHClient().open_sftp().put(...)`), the shim wraps
your call in a `hush remote.put` round-trip.

### 3. `hush.get(NAME)` — pure secrets

```python
from hush import get
DATABASE_URL = get("DATABASE_URL")
```

The value is fetched from the vault at call time, audited, and cached
in-process. The string is never written to disk and never serialised into env
variables of child processes (unless you do that yourself).

## Authentication

The SDK reads `~/.hush/config` (written by `hush login`) and falls back to
these env vars:

```
HUSH_ENDPOINT          http://127.0.0.1:8443
HUSH_API_KEY           hush_xxxxxxxxxxxxxxxxxxxx
```

See the project README for the broker's design.
