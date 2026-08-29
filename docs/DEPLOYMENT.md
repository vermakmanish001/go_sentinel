# Deploying GoSentinel

The dashboard aims a fleet of load generators at whatever host it is told to.
Deployed carelessly it is a traffic cannon with a web form, so this document
leads with the two controls that matter and treats everything else as detail.

## The two controls that matter

### 1. Restrict what can be targeted

```yaml
api:
  allowed_targets:
    - api.example.com
    - "*.staging.example.com"
```

An **empty list means any host** — convenient locally, unacceptable once the API
is reachable by anyone else. The server logs a warning at startup when the list
is empty; treat that warning as a blocker for a real deployment.

Patterns are hostnames. `*.example.com` matches subdomains only — it does **not**
match `example.com` itself, and never matches across a dot boundary, so
`api.example.com.evil.com` is rejected.

Cloud instance metadata (`169.254.169.254`, `metadata.google.internal`) is
refused even when the list is empty, because it is reachable from inside most
deployments and holds credentials. An explicit pattern overrides that, for the
case where testing your own metadata service is the actual goal.

### 2. Require a login

```yaml
api:
  auth_enabled: true      # the default
  session_ttl: 168h
```

With auth enabled and no accounts, the server **refuses to start** rather than
coming up unprotected. Create the first account either way:

```bash
# Interactive, or via GOSENTINEL_ADMIN_PASSWORD to keep it out of shell history
./bin/api --create-user alice
```

or let a fresh deployment bootstrap itself:

```yaml
api:
  bootstrap_user: admin
  bootstrap_password: "…"   # only used when the database has no users
```

Passwords are hashed with Argon2id. Sessions are random 256-bit tokens delivered
in an `HttpOnly`, `SameSite=Lax` cookie, and the database stores only a SHA-256
hash of each token, so a database leak yields no usable cookie.

`GOSENTINEL_API_AUTH_ENABLED=false` disables all of this. It exists for local
development on a loopback bind. Anything else is a mistake.

## TLS

Either terminate TLS at a reverse proxy, or serve it directly:

```yaml
api:
  tls_cert: /etc/gosentinel/tls.crt
  tls_key:  /etc/gosentinel/tls.key
```

Serving TLS directly also marks the session cookie `Secure`. Behind a proxy that
flag is not set automatically, since a `Secure` cookie is silently dropped over
plain HTTP and would make local development fail confusingly — set
`api.tls_cert`/`api.tls_key`, or ensure the proxy is the only route in.

## Configuration reference

Every key can be set in `configs/config.yaml` or as `GOSENTINEL_<SECTION>_<KEY>`.
List values accept comma-separated environment variables
(`GOSENTINEL_API_ALLOWED_TARGETS=a.example.com,b.example.com`).

| Key | Default | Purpose |
|---|---|---|
| `api.address` | `0.0.0.0` | Bind address — use `127.0.0.1` when unauthenticated |
| `api.port` | `8090` | HTTP port (8080 is the bundled httpbin target) |
| `api.orchestrator_url` | `localhost:50051` | gRPC address of the orchestrator |
| `api.db_path` | `gosentinel.db` | SQLite file: history, plans, users |
| `api.auth_enabled` | `true` | Require a session on every data endpoint |
| `api.session_ttl` | `168h` | Session lifetime |
| `api.bootstrap_user` / `_password` | — | Creates the first account on an empty database |
| `api.allowed_targets` | *(empty = any)* | Hosts a plan may point at |
| `api.tls_cert` / `api.tls_key` | — | Serve HTTPS directly |

## Persistence

Run history, saved plans and accounts live in one SQLite file. Back up
`api.db_path`; the Docker stack keeps it on the `api-data` volume. Samples
accumulate at one row per second per run — a 10-minute run is 600 rows, so
growth is driven by run count, not size.

## What is not handled yet

- **No roles.** Every account can run, stop and delete anything.
- **No rate limiting** on login. Put a proxy in front if the instance is
  internet-facing.
- **Workers are unauthenticated.** Anything that can reach a worker's gRPC port
  can drive it directly, bypassing the API entirely. Keep the worker and
  orchestrator ports on a private network; only the dashboard port should be
  reachable.
