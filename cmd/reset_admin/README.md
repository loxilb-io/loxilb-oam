# Admin Reset Tool

A local CLI for resetting the admin account to its configured bootstrap
credentials. It requires shell access to the host running the database, so it
cannot be triggered remotely.

## Purpose

Use `reset_admin` to restore admin access when:

- the admin password has been lost;
- admin access must be recovered after a misconfiguration;
- a development instance needs to be returned to a known state.

## Credentials after reset

The admin account is reset to:

- **Username**: `admin`
- **Password**: the value of `OAM_DEFAULT_ADMIN_PASSWORD` (there is no built-in default)
- **Email**: `admin@oam-loxilb.local`

The account is flagged as "must change on next login".

## Usage

The tool defaults its `-db-*` flags from the same `DB_*` environment surface as
the server. Inside a bundled Compose or Kubernetes deployment those vars
(`DB_HOST`, `DB_USER`, `DB_PASSWORD`, `DB_PORT`, `DB_NAME`) are already set, so no
extra flags are needed:

```bash
# Docker Compose (container name: oam-loxilb-app)
docker exec -it oam-loxilb-app ./reset_admin --confirm
```

Running it standalone, supply the DB connection via env or flags:

```bash
export OAM_DEFAULT_ADMIN_PASSWORD='<bootstrap-admin-password>'
export DB_PASSWORD='<database-password>'   # legacy alias: OAM_DB_PASSWORD

go run ./cmd/reset_admin --confirm
```

### With a custom database connection

```bash
go run ./cmd/reset_admin \
  --db-user=oamuser \
  --db-host=127.0.0.1 \
  --db-port=3306 \
  --db-name=loxioam \
  --confirm
```

Each `-db-*` flag defaults from the matching `DB_*` env var; the password comes
from `DB_PASSWORD` (legacy alias `OAM_DB_PASSWORD`) or `--db-password`. An
explicit flag always wins.

### With TLS

```bash
go run ./cmd/reset_admin \
  --ssl-option=true \
  --ssl-ca-cert-file=./ssl/certs/root-ca.pem \
  --ssl-ca-client-cert-file=./ssl/certs/client-cert.pem \
  --ssl-ca-client-key-file=./ssl/certs/client-key.pem \
  --confirm
```

### Building a binary

```bash
go build -o reset_admin ./cmd/reset_admin
./reset_admin --confirm
```

## Command-line flags

| Flag | Description | Default |
|------|-------------|---------|
| `--confirm` | **Required.** Confirm the reset operation | `false` |
| `--db-user` | Database username | `DB_USER` env, else `oamuser` |
| `--db-password` | Database password (legacy alias `OAM_DB_PASSWORD`) | `DB_PASSWORD` env |
| `--db-host` | Database host | `DB_HOST` env, else `127.0.0.1` |
| `--db-port` | Database port | `DB_PORT` env, else `3306` |
| `--db-name` | Database name | `DB_NAME` env, else `loxioam` |
| `--ssl-option` | Enable a TLS connection (`true`/`false`) | `false` |
| `--ssl-ca-cert-file` | CA certificate path | `./ssl/certs/root-ca.pem` |
| `--ssl-ca-client-cert-file` | Client certificate path | `./ssl/certs/client-cert.pem` |
| `--ssl-ca-client-key-file` | Client key path | `./ssl/certs/client-key.pem` |

## What it does

1. Connects to the database.
2. Resets the admin username, password, and email to the configured defaults
   (or creates the admin account if none exists).
3. Invalidates all existing admin session tokens.
4. Marks the credentials as "must change after next login".

## Security notes

- `--confirm` is mandatory to prevent accidental resets.
- All existing admin sessions are invalidated by the reset.
- Log in and change the password immediately afterwards.
- Restrict shell access to this tool in production; reset operations are logged.

## After a reset

Log in as `admin` with the configured `OAM_DEFAULT_ADMIN_PASSWORD`, then change
your credentials via the API:

```bash
curl -X POST http://localhost:8080/oam/setup/update-admin \
  -H "Content-Type: application/json" \
  -d '{
    "currentUsername": "admin",
    "currentPassword": "<OAM_DEFAULT_ADMIN_PASSWORD>",
    "newUsername": "myadmin",
    "newPassword": "<new-secure-password>",
    "newEmail": "admin@example.com",
    "confirmPassword": "<new-secure-password>"
  }'
```

## Troubleshooting

- **Database connection failed** — verify the database is running and the
  connection parameters (`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`) are correct.
- **Failed to reset admin account** — check that the database user has `UPDATE`
  privileges on the `users` table.
- **Missing `--confirm`** — intentional; add `--confirm` to proceed.

## Related

- Password policy: minimum 9 characters with upper, lower, number, and special.
- See the project README for user management and configuration.
