<p align="center">
  <img src="./.github/assets/banner.png" alt="Hush" width="100%">
</p>

<p align="center">
  <strong>A zero-trust credential & access broker for humans, scripts, and AI agents.</strong><br>
  <sub>One binary. No raw passwords leaving the vault. Drop-in for paramiko.</sub>
</p>

<p align="center">
  <a href="https://github.com/shuakami/hush/actions/workflows/ci.yml"><img src="https://github.com/shuakami/hush/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/shuakami/hush/releases/latest"><img src="https://img.shields.io/github/v/release/shuakami/hush?label=release&color=7c3aed&style=flat" alt="Release"></a>
  <a href="https://github.com/shuakami/hush/blob/main/LICENSE"><img src="https://img.shields.io/github/license/shuakami/hush?color=7c3aed&style=flat" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/shuakami/hush"><img src="https://goreportcard.com/badge/github.com/shuakami/hush" alt="Go Report"></a>
  <a href="https://github.com/shuakami/hush/stargazers"><img src="https://img.shields.io/github/stars/shuakami/hush?color=7c3aed&style=flat" alt="Stars"></a>
</p>

<p align="center">
  <a href="#install">Install</a> ·
  <a href="#quickstart">Quickstart</a> ·
  <a href="#how-it-works">How it works</a> ·
  <a href="#why-hush">Why</a> ·
  <a href="#cli-reference">CLI</a> ·
  <a href="#python-sdk">SDK</a>
</p>

---

Stop pasting root passwords into your scripts.

Hush is a single binary that holds your credentials in an encrypted vault and **executes commands on your behalf** — Python scripts, CI jobs, and AI agents call Hush, Hush logs in, runs the work, and returns the result. The raw password never reaches the caller.

```python
# Before — plaintext password lives in your repo, forever
ssh = paramiko.SSHClient()
ssh.set_missing_host_key_policy(paramiko.AutoAddPolicy())
ssh.connect("hk1.example.com", port=22, username="root",
            password="super-secret-root-password")

# After — credential lives in Hush vault, paramiko shim retrieves on connect
import hush.paramiko_compat as paramiko
ssh = paramiko.SSHClient()
ssh.connect("hk1")
```

That's the entire migration. 64 paramiko call sites become one import change.

---

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

## Quickstart

```bash
# 1. Initialize — generates KEK + admin token, prints the token to stdout
hush bootstrap

# 2. Save a credential
echo 'super-secret-root-password' | hush secret set hk1-root-password --stdin

# 3. Register a host
hush host add hk1 \
  --transport ssh \
  --address 10.0.0.10 --port 22 --user root \
  --auth-kind password --auth-secret hk1-root-password \
  --tag prod

# 4. Run something — credential never appears anywhere
hush exec hk1 -- "systemctl status nginx"
hush cp ./build.tar.gz hk1:/opt/app/
hush ssh hk1                                      # interactive PTY

# 5. Run on every "prod" host in parallel
hush exec --tag prod -- "uptime"

# 6. Verify the audit chain hasn't been tampered with
hush audit verify
```

## How it works

```
                ┌──────────────┐
   you / agent ─▶  hush CLI    ──── HTTP + token ────┐
                └──────────────┘                     ▼
                                          ┌──────────────────┐
                                          │   hush server    │
                                          │ ┌──────────────┐ │
                                          │ │ vault (AES)  │ │
                                          │ │ inventory    │ │
                                          │ │ audit chain  │ │
                                          │ └──────────────┘ │
                                          └────────┬─────────┘
                                                   │ SSH (password / key
                                                   │ / jump-chain / sdjz-relay)
                                                   ▼
                                              target hosts
```

- **Vault** — AES-256-GCM with envelope encryption (per-secret DEK wrapped by KEK). KEK source is pluggable: env, file, or KMS (roadmap).
- **Broker** — agents call `exec(host, cmd)` and get back stdout, stderr, exit code. The raw credential is never returned to the caller.
- **Audit** — every read, exec, put, get is appended to a hash-chained log. `hush audit verify` walks the chain and detects any mutation.
- **Transports** — SSH password, SSH key, SSH with jump host, and `sdjz-relay` (curl-based temporary tunnels) — same `hush exec hk1 …` interface for all four.

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
