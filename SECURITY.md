# Security model

Litescope lets AI agents operate on real databases. That is only safe if the
tool is built to assume the caller can make mistakes. This page describes the
controls that make agent access safe by default, and — just as important —
what Litescope does **not** protect against, so you can reason about the
boundary honestly.

## Design principles

1. **Read-only by default.** The write tools (`litescope_query_write`,
   `litescope_migrate_apply`, `litescope_write_undo`, `litescope_autopilot`,
   snapshot/restore, D1 create/delete) are not registered at all unless the
   server is started with `--allow-writes`. An agent connected to a default
   server can inspect and query, but cannot change or delete anything.
2. **Dry-run before commit.** Every guarded write defaults to `apply=false`:
   the statement is measured (exact `rows_affected`, and a `blast_radius_diff`)
   without being committed. For D1 the measurement runs on a pulled copy, so
   production is never touched during a dry-run.
3. **No write without an undo point.** When you do commit, Litescope captures a
   rewind point *before* the write — a local file snapshot, or a Cloudflare D1
   Time Travel bookmark — and returns it as a `rewind_token`. If that undo
   point cannot be captured, the write is refused rather than executed
   irreversibly.
4. **Undo is bound to its target.** A `rewind_token` is tagged with its
   provider and the exact database it was minted for. Passing a token to
   `litescope_write_undo` with a different source (or a local token to a D1
   source, or vice-versa) is rejected — an agent cannot restore the wrong
   database by mixing up tokens.

## What each layer actually protects

| Control | Protects against | Does **not** protect against |
|---|---|---|
| `--allow-writes` gate | Accidental or unauthorized mutation by a read-only agent | A write-enabled server — writes are then possible by design |
| Read-tool mutation guard | `litescope_query` being used to mutate (statements whose leading keyword is `INSERT`/`UPDATE`/`DELETE`/`DROP`/`CREATE`/`ALTER`/`REPLACE`/`TRUNCATE`/`ATTACH` are rejected) | Deliberately obfuscated SQL — this is a convenience guard, not a SQL firewall. Real mutation still requires the write tools |
| Dry-run + blast-radius diff | Committing a write whose impact you didn't see first | A user who reviews the diff and approves a destructive change anyway |
| Auto rewind point + `write_undo` | Permanent, unrecoverable data loss from a bad write | Data written *after* the undo point that a rewind would also roll back (Time Travel restores whole-database state) |
| Token → source binding | Restoring the wrong database with a stale/foreign token | — |

Litescope executes the SQL an agent authors. It is **not** a WAF and does not
sanitize application input — it makes agent-authored operations *measurable and
reversible*, which is a different (and, for this use case, more useful)
guarantee than input sanitization.

## Credentials

- Cloudflare (`CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`) and Turso
  credentials are read from environment variables or the DSN. They are never
  written to logs or tool output.
- Scope the Cloudflare API token to the minimum needed: **D1 read** for
  inspection, **D1 edit** additionally if you enable writes and Time Travel.
- Prefer per-environment tokens. A read-only token plus a default (no
  `--allow-writes`) server gives an agent a genuinely read-only surface even if
  the token were leaked.

## HTTP transport

The stdio transport is local and inherits your shell's trust boundary. The
optional Streamable HTTP transport adds network exposure and is hardened
accordingly:

- **Bearer token** — when configured, every request must present
  `Authorization: Bearer <token>`; missing/wrong tokens get `401`.
- **Origin allowlist** — browser requests with a disallowed `Origin` are
  rejected (DNS-rebinding protection). Localhost origins and non-browser
  clients (no `Origin` header) are permitted; any other origin must be
  explicitly allowlisted.

Do not expose the HTTP transport publicly without both a bearer token and a
tight origin allowlist, behind TLS.

## Reporting a vulnerability

Email **croc100100@gmail.com** with details and reproduction steps. Please do
not open a public issue for a security report. We aim to acknowledge within a
few days.
