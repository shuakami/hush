<p align="center">
  <img src="./.github/assets/banner.png" alt="Hush" width="100%">
</p>

<p align="center">
  <a href="https://github.com/shuakami/hush/actions/workflows/ci.yml"><img src="https://github.com/shuakami/hush/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/shuakami/hush/releases/latest"><img src="https://img.shields.io/github/v/release/shuakami/hush?label=release&style=flat&color=111111" alt="Release"></a>
  <a href="https://github.com/shuakami/hush/blob/main/LICENSE"><img src="https://img.shields.io/github/license/shuakami/hush?style=flat&color=111111" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/shuakami/hush"><img src="https://goreportcard.com/badge/github.com/shuakami/hush" alt="Go Report"></a>
  <a href="https://github.com/shuakami/hush/stargazers"><img src="https://img.shields.io/github/stars/shuakami/hush?style=flat&color=111111" alt="Stars"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#getting-started">Getting started</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#cli-reference">CLI</a> ·
  <a href="#python-sdk">SDK</a> ·
  <a href="./skills/hush/SKILL.md">Agent skill</a>
</p>

Hush is a credential broker. It stores secrets in an encrypted vault and runs commands on remote hosts on the caller's behalf, so Python scripts, CI jobs, AI agents, and humans on the CLI never need to handle raw passwords or keys themselves.

It ships as a single Go binary with no external dependencies. Existing `paramiko`-based scripts can adopt Hush by changing one import; the CLI is shaped after `ssh` and `scp` so muscle memory carries over. Every read, exec, put, and get is appended to a hash-chained audit log that `hush audit verify` can validate end-to-end.

```python
# before
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect("hk1.example.com", port=22, username="root",
            password="super-secret-root-password")

# after
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("hk1")
```

The credential lives in the vault; the shim resolves the host name to a transport + auth pair at `connect` time. No other call-site changes are required.

## Install

```bash
# macOS / Linux — one-line installer
curl -fsSL https://github.com/shuakami/hush/releases/latest/download/install.sh | sh
```

```powershell
# Windows
iwr https://github.com/shuakami/hush/releases/latest/download/install.ps1 -useb | iex
```

```bash
# Or grab a pre-built binary directly
curl -L https://github.com/shuakami/hush/releases/latest/download/hush_linux_amd64.tar.gz | tar xz
```

```bash
# Or with Go
go install github.com/shuakami/hush/cmd/hush@latest
```

```bash
# Or Docker
docker run --rm -p 7777:7777 -v hush-data:/data ghcr.io/shuakami/hush:latest
```

```bash
# Python SDK
pip install git+https://github.com/shuakami/hush.git#subdirectory=sdk/python
```

## Getting started

The first run takes about a minute: pick a master key, start the broker, register a host, run a command. After that, everything is `hush exec`, `hush cp`, or one import line in your Python.

### 1. Pick a master key

Hush encrypts the vault with a key-encryption-key (KEK) that **never** lives in the database. By default the KEK comes from the environment variable `HUSH_MASTER_KEY`. Generate one and put it somewhere your shell will read on every login (`~/.zshrc`, `/etc/hush/env`, your secret manager, your `docker run -e`, …).

```bash
export HUSH_MASTER_KEY=$(openssl rand -base64 32)
```

If you prefer a file on disk, set `HUSH_KEK_KIND=file` and `HUSH_KEK_FILE=/etc/hush/master.key` instead. Lose this value and the vault becomes unreadable; back it up the same way you back up an SSH private key.

### 2. Start the broker

```bash
hush bootstrap        # mints admin api key + writes ~/.hush/config
hush server           # daemonise this (systemd, docker, tmux — your call)
```

`bootstrap` mints an admin API key and writes both the local endpoint (`http://127.0.0.1:8443`) and the key into `~/.hush/config` at mode `0600`. The CLI on this machine is now ready — there is no separate "log in" step, and no cloud account anywhere.

To bootstrap a server whose CLI lives on a different machine, pass `--no-save`; the key is printed to stdout and you can copy it across with `hush login --endpoint … --api-key …`. If `hush server` starts on a fresh data directory and finds no API keys, it self-bootstraps with the same logic, prints the key on stderr, and writes the config.

### 3. Register a host

```bash
echo 'super-secret-root-password' | hush secret set hk1-root-password --stdin

hush host add hk1 \
  --transport ssh \
  --address 10.0.0.10 --port 22 --user root \
  --auth-kind password --auth-secret hk1-root-password \
  --tag prod
```

`hk1` is now a name. The address, the port, and the credential pointer live in the inventory; the credential value lives in the vault. Anyone with an API key that has `host:exec` scope can reach `hk1` by name without ever seeing the password.

For a key-based host swap `--auth-kind password --auth-secret …` for `--auth-kind key --auth-secret hk2-private-key`, where `hk2-private-key` is a vault secret containing the PEM blob.

### 4. Use it

```bash
hush exec hk1 -- "systemctl status nginx"           # one host, one command
hush exec --tag prod -- "uptime"                    # every prod host, in parallel
hush cp ./build.tar.gz hk1:/opt/app/build.tar.gz    # upload
hush ssh hk1                                        # interactive shell, for humans
hush audit verify                                   # confirm the audit chain is intact
```

That is the entire daily-driver loop.

### 5. Wire it into a Python script

Pick the form that fits the script:

```python
# minimal change to existing paramiko code
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("hk1")
stdin, stdout, stderr = ssh.exec_command("systemctl status nginx")
```

```python
# or use the higher-level helper
from hush import remote
result = remote.exec("hk1", "systemctl status nginx")
print(result.exit_code, result.stdout)
```

The SDK reads the same `~/.hush/config` the CLI does, so once `hush bootstrap` has written that file the script needs no further configuration.

### 6. Wire it into an AI agent

The repo ships with an [Agent Skill](./skills/hush/SKILL.md) — a single directory the agent's runtime can load to learn when and how to call Hush. For Claude Code:

```bash
mkdir -p .claude/skills
cp -r skills/hush .claude/skills/hush
```

Other runtimes that support the open Agent Skill standard (Claude Desktop, Cursor, Aider, Devin via `AGENTS.md`) load the same file. Once installed, the agent will reach for `hush exec` instead of asking the user for SSH credentials.

## How it works

<p align="center">
  <img src="./.github/assets/architecture.png" alt="Hush architecture" width="100%">
</p>

Callers (operators, Python scripts, CI jobs, AI agents) talk to a single HTTP API on the broker. The exec engine resolves the named host through the inventory, fetches the credential from the vault, dispatches through the matching transport, and appends the result to the audit chain.

- **Vault** — AES-256-GCM with envelope encryption. Each secret has its own DEK, wrapped by a KEK sourced from env, file, or KMS (roadmap).
- **Inventory** — maps a name (`hk1`) to a transport, an auth method, and a set of tags. The same name resolves the same way for every caller.
- **Audit** — append-only, hash-chained log. `hush audit verify` walks the chain and refuses to validate if any record has been mutated.
- **Transports** — SSH password, SSH key, SSH through one or more jump hosts, and `sdjz-relay` (a curl-based temporary tunnel format). The caller never picks the transport; the inventory does.

## Why Hush

Existing tools each cover part of the problem:

| Category                | Examples                          | What they don't do                                                                  |
| ----------------------- | --------------------------------- | ----------------------------------------------------------------------------------- |
| Password manager        | 1Password · Bitwarden · sops      | Don't run the work for you — your script still receives the raw password           |
| Enterprise vault        | HashiCorp Vault · AWS Secrets Mgr | Heavy: cluster + sidecar + IAM tuning before you can deploy                         |
| Bastion / jump host     | Teleport · Boundary               | Audit SSH, but ignore the API keys baked into your `.py` files                      |
| Short-lived credentials | OIDC · SSH CA · smallstep         | Require infra changes; existing paramiko code doesn't benefit                       |

Hush packs the useful parts into one ~50 MB binary:

1. **Single binary, broker mode.** No cluster, no sidecar, no IAM. `hush exec` runs SSH for you; agents never see the password.
2. **Drop-in `paramiko` shim.** Change one import line and existing hardcoded paramiko calls go through Hush.
3. **Named hosts, multi-transport.** `hk1` is a name; the underlying transport can be password SSH, key SSH, jump-host chain, or your own relay tunnel — same call site.
4. **Code-level migration tool.** `hush migrate scan ./scripts/` walks your repo with Python AST, finds `PASSWORD = "..."`, embedded PEM keys, and `paramiko.connect()` kwargs, then generates a diff that pushes secrets into the vault and rewrites the source.
5. **Hash-chained audit.** `hush audit verify` detects log tampering — same primitive Sigstore Rekor uses for supply-chain proofs.
6. **CLI-first surface.** `hush exec hk1 -- cmd` is shaped like `ssh user@host cmd`. Anything an agent already knows about ssh transfers.

## CLI reference

```
hush bootstrap                         init server (KEK + admin token)
hush server                            run the broker daemon
hush login                             save endpoint + token to ~/.hush

hush get NAME                          read a secret value
hush secret  set | get | ls | rm       vault management
hush host    add | ls | show | rm      inventory management

hush exec  HOST  -- CMD                run command on host
hush exec  --tag TAG -- CMD            run on every host with tag, in parallel
hush ssh   HOST                        interactive PTY
hush cp    SRC HOST:/dst               copy file (sftp under the hood)

hush audit  tail | verify              read / verify the audit chain
hush apikey mint | ls                  scoped API keys for agents

hush migrate scan PATH                 find hardcoded credentials
hush migrate apply PATH                push to vault + rewrite source

hush completion bash|zsh|fish|powershell
hush mcp                               start MCP server (Claude Desktop, Devin, etc.)
```

## Python SDK

```python
from hush import remote

# Single host
result = remote.exec("hk1", "uname -a")
print(result.stdout, result.exit_code)

# Many hosts in parallel by tag
for r in remote.exec_many(tag="prod", cmd="systemctl is-active nginx"):
    print(r.host, r.exit_code)

# Or a paramiko drop-in:
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("hk1")
sftp = ssh.open_sftp()
sftp.put("./build.tar.gz", "/opt/app/build.tar.gz")
```

## Migration

```bash
$ hush migrate scan ./scripts/ssh/
found 11 potential credential(s) across 10 file(s):

  ./scripts/ssh/ssh_hk.py
    line   16  [literal_assign]  PASSWORD = "..."
           hint: variable=PASSWORD  → suggested secret name: ssh_hk-password

  ./scripts/ssh/ssh_panel_tunnel.py
    line   31  [embedded_pem]    ED25519_KEY = """-----BEGIN OPENSSH PRIVATE KEY-----..."""
           hint: variable=ED25519_KEY → suggested secret name: ssh_panel_tunnel-ed25519_key
  ...
```

`hush migrate apply` will:
1. Push every literal value into the vault under its suggested secret name.
2. Rewrite the source file to call `hush.get("...")` (or replace `import paramiko` with `import hush.paramiko_compat as paramiko`).
3. Open a PR with the diff for review.

## Security posture

- Server defaults to `127.0.0.1`; expose via mTLS or nginx + client certs only.
- API keys are scoped: `secret:read` · `secret:write` · `host:list` · `host:exec` · `host:put` · `host:get` · `audit:read` · `apikey:write` · `*`.
- Vault SQLite file is `0600`. Run server under an unprivileged user.
- KEK source is pluggable (env / file / KMS); rotate by re-wrapping every DEK.
- Approval webhooks for high-risk operations (Lark / Slack / Discord) — roadmap.

## Roadmap

**v0.1 — shipped**
- Vault (AES-256-GCM + envelope, env/file KEK)
- Host inventory + tags
- Transports: ssh password / ssh key / jump chain / sdjz-relay
- Exec broker (single + parallel) + put/get
- Append-only hash-chained audit log
- HTTP API + scoped API keys
- Python SDK + paramiko shim + migration scanner
- MCP server (stdio)
- Docker / docker-compose / GitHub Actions CI

**v0.2 — next**
- OpenSSH `ProxyCommand` integration (let `ssh hk1`, `ansible`, `fab` flow through Hush)
- SSH CA + short-lived certificates
- Policy engine (YAML / OPA-style)
- Approval webhooks for high-risk operations
- AWS / GCP / Aliyun KMS as KEK source
- OIDC / WebAuthn for human login
- GitHub Actions OIDC → token (no persistent secrets in CI)
- Postgres backend
- Web UI (embedded SvelteKit, single-binary delivery)
- `migrate apply` (auto-push to vault + rewrite source + open PR)

## License

MIT
