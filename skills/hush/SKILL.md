---
name: hush
description: Use Hush to run shell commands, copy files, and read secrets on remote hosts without ever handling raw passwords or private keys. Hush stores credentials in an encrypted vault and connects on the agent's behalf via SSH (password / key / jump-chain) or `sdjz-relay` tunnels. Invoke this skill any time the task requires SSH-ing into a server, deploying a build, restarting a service, tailing a log, transferring a file, or fetching a credential by name.
license: MIT
---

# hush

Hush is a credential broker. The agent calls a `hush` CLI subcommand; Hush resolves the named host, fetches the credential from its vault, opens the connection, executes the work, and returns stdout / stderr / exit code. The credential should not appear in the agent's memory, environment, chat transcript, logs, commit messages, or shell history.

Mental model:

1. `hush server` owns the encrypted vault, host inventory, transports, and audit log.
2. `hush bootstrap` creates the first admin API key. On a local machine it writes `~/.hush/config` automatically.
3. The CLI reads `~/.hush/config`, or `HUSH_ENDPOINT` + `HUSH_API_KEY`, then asks the server to do work.
4. Host records reference secret names, not plaintext credentials.
5. `hush exec` and `hush cp` use those host records; the agent does not manually SSH or handle raw passwords.

## When to use

Reach for this skill when **any** of the following are true:

- The task says "ssh into …", "log into …", "deploy …", "restart …", "tail the log on …", "scp …", "rsync …".
- The task references a named host without telling the agent the IP, port, or password.
- The task asks for a value that smells like a secret (`*_PASSWORD`, `*_TOKEN`, `*_API_KEY`, `*_PRIVATE_KEY`, database URL).
- The task touches a Python script that imports `paramiko`, `fabric`, or `asyncssh`.

Do **not** invent host names, passwords, or IPs. If the agent does not know which host to use, list them first (`hush host ls`) and ask the user to confirm.

## Default workflow for agents

Start here when the user asks to "check the server", "SSH", "deploy", "copy a file", or "use the saved credential":

```bash
hush doctor
hush host ls
hush doctor --host NAME
hush exec NAME -- "hostname && whoami && uname -a"
```

Only after these read-only checks pass should the agent run write commands such as package installs, service restarts, deploy scripts, uploads, or edits under `/etc`, `/opt`, `/var`, or application directories.

Use `hush exec NAME -- "COMMAND"` for automation. Use `hush ssh NAME` only for a real human interactive shell.

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

Every command honors `HUSH_ENDPOINT` and `HUSH_API_KEY` env vars.

Fresh local machine:

```bash
hush bootstrap
hush server
hush doctor
```

`hush bootstrap` mints an admin API key and writes `~/.hush/config` at mode `0600`, so the CLI can run immediately without `hush login`.

Headless or different client machine:

```bash
hush bootstrap --no-save
hush login --endpoint http://127.0.0.1:8443 --api-key '<api-key>'
```

Use `--no-save` only when the key must be moved to another machine or injected into CI. Do not paste the key into chat or logs. If `hush server` starts on an empty data directory, it also bootstraps using the same config-writing logic; set `HUSH_NO_SAVE_CONFIG=1` to keep headless behavior.

## Common patterns

### 1. Register an SSH password host without exposing the password

```bash
printf '%s' '<password>' | hush secret set NAME-root-password --stdin
hush host add NAME --transport ssh --address 10.0.0.10 --port 22 --user root \
  --auth-kind password --auth-secret NAME-root-password --tag prod
hush doctor --host NAME
```

The secret name is safe to mention. The secret value is not.

### 2. Register an SSH key host

```bash
hush secret set NAME-ed25519 --from-file ~/.ssh/id_ed25519
hush host add NAME --transport ssh --address 10.0.0.10 --user root \
  --auth-kind key --auth-secret NAME-ed25519 --tag prod
hush doctor --host NAME
```

### 3. Run a remote command and act on its output

```bash
hush exec NAME -- "systemctl is-active nginx"
```

Exit code 0 means active. The agent should branch on the exit code, not on the stdout text.

### 4. Run the same command on every prod host in parallel

```bash
hush exec --tag prod -- "df -h /"
```

Output is grouped per host with a header line `=== <host> (exit=N) ===`. If any host fails, the overall exit code is non-zero; the agent should still parse per-host blocks.

### 5. Deploy a build artifact

```bash
hush cp ./build.tar.gz NAME:/opt/app/build.tar.gz
hush exec NAME -- "cd /opt/app && tar xf build.tar.gz && systemctl restart app"
```

On Windows, local paths such as `C:\Users\me\build.tar.gz` are treated as local paths, not as `HOST:/path` prefixes.

### 6. Read a secret into a child process without logging it

```bash
DB_URL=$(hush get NAME-db-url)
psql "$DB_URL" -c 'select 1'
```

Never `echo "$DB_URL"`. Never paste a `hush get` result into a chat message. Prefer `hush secret ls` when only the secret name or existence is needed.

### 7. Replace `paramiko.connect(...)` in a Python script

```python
# before
ssh = paramiko.SSHClient()
ssh.connect("10.0.0.10", username="root", password="...")

# after - one line changes
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("NAME")
```

The shim accepts the same `SSHClient` / `SFTPClient` API surface, so call-site code (`exec_command`, `open_sftp`, `put`, `get`) is unchanged.

## Diagnosis ladder

Use this order before assuming Hush is broken:

```bash
hush doctor
hush host ls
hush host show NAME
hush doctor --host NAME
hush exec NAME -- "hostname && whoami"
```

`hush doctor --host NAME` checks local config, server health, API auth, inventory access, host existence, and a minimal remote `true` command. It does not print secrets. If this fails, fix the first failing row before running deploy or recovery commands.

## Failure modes

| Symptom                                         | What it means                                                | What the agent should do                        |
| ----------------------------------------------- | ------------------------------------------------------------ | ----------------------------------------------- |
| `host "X" not found`                         | The name is not registered.                                  | Run `hush host ls`, ask the user.               |
| `endpoint not set` / `api key not set`       | `~/.hush/config` is missing or env vars are absent.          | Run `hush bootstrap` locally, or `hush login` with a provided key; do not prompt for SSH passwords. |
| `connect: i/o timeout` / `permission denied` | The transport is down or the credential is stale.            | Run `hush doctor --host NAME`; do not retry more than 3 times. |
| unclear local/host setup failure             | Need one command that checks config, auth, inventory, host.  | Run `hush doctor --host NAME`.                  |
| `audit verify: chain broken at record N`     | Audit log was mutated after the fact.                        | **Stop.** Surface to the user immediately.      |
| `exec returned non-zero`                     | The remote command failed.                                   | Re-emit stderr to the user, propose next step.  |
| `sdjz-relay` host cannot connect             | Relay token, relay URL, or tunnel state is wrong.            | Check the host's relay metadata and referenced relay secret name; do not treat it as normal SSH. |

## Boundaries

- **Never** print the value returned by `hush get` to chat, logs, or commit messages.
- **Never** use `hush secret set --value` with a real credential unless there is no safer input path; prefer `--stdin` or `--from-file`.
- **Never** call `hush secret set` to store a value the user has not explicitly asked to be stored.
- **Never** invoke `hush ssh NAME` (interactive PTY) inside an automated context — there is no human to type. Use `hush exec` instead.
- **Never** delete vault entries (`hush secret rm`) without an explicit request from the user; secret deletion is irreversible without a backup.
- If a command would write to a production host (`/etc/`, `/opt/`, `/var/`, anything under `systemctl`) and the user has not pre-approved this, ask first.

Ask the user only when:

- `hush host ls` cannot identify the intended host.
- `hush doctor --host NAME` proves the stored credential is missing or stale.
- A write operation is needed and the user has not approved that class of change.

## Installing this skill

The agent operator can install this skill into their tool by copying the `skills/hush/` directory into the appropriate location for their runtime — e.g. `.claude/skills/hush/` for Claude Code, `~/.config/claude/skills/hush/` for Claude Desktop, or any path their agent loads skills from. The directory is self-contained.
