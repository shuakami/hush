---
name: hush
description: Use Hush to run shell commands, copy files, and read secrets on remote hosts without ever handling raw passwords or private keys. Hush stores credentials in an encrypted vault and connects on the agent's behalf via SSH (password / key / jump-chain) or `sdjz-relay` tunnels. Invoke this skill any time the task requires SSH-ing into a server, deploying a build, restarting a service, tailing a log, transferring a file, or fetching a credential by name.
license: MIT
---

# hush

Hush is a credential broker. The agent calls a `hush` CLI subcommand; Hush resolves the named host, fetches the credential from its vault, opens the connection, executes the work, and returns stdout / stderr / exit code. The credential never appears in the agent's memory, environment, or output.

## When to use

Reach for this skill when **any** of the following are true:

- The task says "ssh into …", "log into …", "deploy …", "restart …", "tail the log on …", "scp …", "rsync …".
- The task references a named host (e.g. `hk1`, `prod-1`, `nmg-mac`) without telling the agent the IP, port, or password.
- The task asks for a value that smells like a secret (`*_PASSWORD`, `*_TOKEN`, `*_API_KEY`, `*_PRIVATE_KEY`, database URL).
- The task touches a Python script that imports `paramiko`, `fabric`, or `asyncssh`.

Do **not** invent host names, passwords, or IPs. If the agent does not know which host to use, list them first (`hush host ls`) and ask the user to confirm.

## Available commands

```
hush host ls                            list every host known to the broker
hush host show NAME                     show transport, address, auth kind, tags
hush doctor [--host NAME]               check config, server, auth, inventory, and host

hush exec  NAME -- CMD [ARGS...]        run a single command, stream output
hush exec  --tag TAG -- CMD             run on every host carrying TAG, in parallel
hush ssh   NAME                         interactive PTY (only when a human is present)
hush cp    SRC NAME:/dst                upload a local file (sftp under the hood)
hush cp    NAME:/src ./dst              download a remote file

hush get   SECRET_NAME                  read a vault secret to stdout (use sparingly)
hush secret ls                          list secret names (values are never printed)

hush audit tail [-n 50]                 inspect recent broker activity
hush audit verify                       confirm the audit chain has not been mutated
```

Every command honours `HUSH_ENDPOINT` and `HUSH_API_KEY` env vars. On a fresh box, run `hush bootstrap`: it mints an admin API key and writes `~/.hush/config` automatically, so the next `hush exec` Just Works. Do **not** ask the user for the host password.

## Common patterns

### 1. Run a remote command and act on its output

```bash
hush exec hk1 -- "systemctl is-active nginx"
```

Exit code 0 means active. The agent should branch on the exit code, not on the stdout text.

### 2. Run the same command on every prod host in parallel

```bash
hush exec --tag prod -- "df -h /"
```

Output is grouped per host with a header line `=== <host> (exit=N) ===`. If any host fails, the overall exit code is non-zero; the agent should still parse per-host blocks.

### 3. Deploy a build artifact

```bash
hush cp ./build.tar.gz hk1:/opt/app/build.tar.gz
hush exec hk1 -- "cd /opt/app && tar xf build.tar.gz && systemctl restart app"
```

### 4. Read a secret into a child process **without** logging it

```bash
DB_URL=$(hush get prod-db-url)
psql "$DB_URL" -c 'select 1'
```

Never `echo "$DB_URL"`. Never paste a `hush get` result into a chat message.

### 5. Replace `paramiko.connect(...)` in a Python script

```python
# before
ssh = paramiko.SSHClient()
ssh.connect("hk1.example.com", username="root", password="…")

# after — one line changes
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("hk1")
```

The shim accepts the same `SSHClient` / `SFTPClient` API surface, so call-site code (`exec_command`, `open_sftp`, `put`, `get`) is unchanged.

## Failure modes

| Symptom                                         | What it means                                                | What the agent should do                        |
| ----------------------------------------------- | ------------------------------------------------------------ | ----------------------------------------------- |
| `host "X" not found`                            | The name is not registered.                                  | Run `hush host ls`, ask the user.               |
| `endpoint not set` / `api key not set`          | `~/.hush/config` is missing or `HUSH_ENDPOINT` not exported. | Run `hush bootstrap`; do not prompt for passwords. |
| `connect: i/o timeout` / `permission denied`    | The transport is down or the credential is stale.            | Tell the user; do not retry > 3 times.          |
| unclear local/host setup failure                 | Need one command that checks config, auth, inventory, host.  | Run `hush doctor --host NAME`.                  |
| `audit verify: chain broken at record N`        | Audit log was mutated after the fact.                        | **Stop.** Surface to the user immediately.      |
| `exec returned non-zero`                        | The remote command failed.                                   | Re-emit stderr to the user, propose next step.  |

## Boundaries

- **Never** print the value returned by `hush get` to chat, logs, or commit messages.
- **Never** call `hush secret set` to store a value the user has not explicitly asked to be stored.
- **Never** invoke `hush ssh NAME` (interactive PTY) inside an automated context — there is no human to type. Use `hush exec` instead.
- **Never** delete vault entries (`hush secret rm`) without an explicit request from the user; secret deletion is irreversible without a backup.
- If a command would write to a production host (`/etc/`, `/opt/`, `/var/`, anything under `systemctl`) and the user has not pre-approved this, ask first.

## Installing this skill

The agent operator can install this skill into their tool by copying the `skills/hush/` directory into the appropriate location for their runtime — e.g. `.claude/skills/hush/` for Claude Code, `~/.config/claude/skills/hush/` for Claude Desktop, or any path their agent loads skills from. The directory is self-contained.
