# Running loxilb-oam Against a Database

`loxilb-oam` stores all of its state in PostgreSQL 18 or later. This guide covers
the two ways to provide that database when you are **not** using a bundled
Compose stack (which provisions and initializes PostgreSQL for you):

1. [PostgreSQL in a container](#1-postgresql-in-a-container) — the quickest path for
   development against a locally built binary.
2. [An existing / external PostgreSQL](#2-an-existing-or-external-postgresql) — point the
   service at a database you already run.

Then, optionally, [TLS to the database](#3-tls-to-the-database).

> **Just want the service running?** Use one of the Compose paths instead — they
> create, initialize, and wire the database automatically:
> - Full management plane (UI + API + PostgreSQL behind a TLS edge):
>   [deployment-compose.md](deployment-compose.md)
> - API + PostgreSQL only, over HTTP: `docker compose up -d` from the repository root
>   (see [DEPLOYMENT.md](../DEPLOYMENT.md)).
> - Converged OAM + Gateway sharing one database:
>   [deployment-converged.md](deployment-converged.md).

## The schema is single-sourced

There is exactly one authoritative schema:

- **`database/init/00-init-complete.sql`** — the complete schema for a fresh
  database. It creates every table the service needs (`users`,
  `loxilb_instances`, `api_tokens`, `logs`, `alerts`, `acknowledgments`,
  `login_attempts`, `instance_snapshots`, `instance_snapshot_schedules`,
  `system_config`, `system_settings`), their indexes, and the seed rows.
- **`database/migrations/*.sql`** — numbered, incremental migrations for
  databases that already exist. Apply them in filename order.

Do not hand-write tables. A database missing the `role` column, the
`login_attempts` table, or the snapshot tables will fail RBAC checks, login
lockout, and snapshot operations at runtime.

Column-by-column reference: [oam-db.md](oam-db.md).

## 1. PostgreSQL in a container

Start just the database from the repository root — the root
`docker-compose.yml` mounts `database/init/` into the PostgreSQL entrypoint, so the
schema is loaded automatically on first boot of an empty volume:

```bash
cp .env.example .env    # set DB_PASSWORD at minimum
docker compose up -d postgres
```

Confirm it initialized:

```bash
docker compose exec postgres sh -c \
  'exec psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\\dt"'
```

You should see all eleven tables listed above. Now build and run the service
against it:

```bash
make build

export OAM_JWT_SECRET="$(openssl rand -base64 48)"
export OAM_DEFAULT_ADMIN_PASSWORD='<bootstrap-admin-password>'
export DB_PASSWORD='<the value you put in .env>'

./loxilb-oam -db-host=127.0.0.1 -db-port=5432 -db-user=oamuser -db-name=loxioam -port=8080
```

The `-db-*` flags each default from the matching `DB_*` environment variable, so
if you exported the whole `DB_*` family you can simply run `./loxilb-oam`.
`make run` does the same thing using the Makefile's `DB_*` variables.

Verify: `curl http://localhost:8080/oam/health`.

### Reinitializing

The schema is only applied on first boot of an **empty** data volume. To start
over, destroy the volume:

```bash
docker compose down -v && docker compose up -d postgres
```

## 2. An existing or external PostgreSQL

Create the database and an account for the service, then load the schema:

```bash
# PostgreSQL has no CREATE ... IF NOT EXISTS for roles or databases, so this
# is written to be re-runnable rather than to fail on a second pass.
psql -h <host> -U postgres <<'SQL'
SELECT 'CREATE DATABASE loxioam'
  WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'loxioam')\gexec
SQL

psql -h <host> -U postgres -d loxioam <<'SQL'
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'oamuser') THEN
    CREATE ROLE oamuser LOGIN PASSWORD '<database-password>';
  END IF;
END $$;
-- The service creates no objects at runtime, but it must own the ones the
-- schema creates, so load the schema AS oamuser (below) rather than granting
-- piecemeal afterwards.
ALTER DATABASE loxioam OWNER TO oamuser;
SQL

# Load the schema as the service account so it owns every table.
PGPASSWORD='<database-password>' psql -h <host> -U oamuser -d loxioam \
  -v ON_ERROR_STOP=1 -f database/init/00-init-complete.sql
```

Then point the service at it — either with flags or the `DB_*` environment
family:

```bash
export DB_HOST=<host> DB_PORT=5432 DB_USER=oamuser DB_NAME=loxioam
export DB_PASSWORD='<database-password>'
export OAM_JWT_SECRET="$(openssl rand -base64 48)"
export OAM_DEFAULT_ADMIN_PASSWORD='<bootstrap-admin-password>'

./loxilb-oam -port=8080
```

In the Compose bundles, set `DB_HOST` in `.env` to your external host — the
bundled `postgres` service is then unused.

### Converged shared database

Converged mode uses one PostgreSQL server and one `loxioam` database with three
schema tenants: OAM in `public`, Gateway API keys/quotas in `aigw`, and the
currently dormant Gateway management store in `aigw_mgmt`. Run the idempotent
bootstrap after authenticated PostgreSQL readiness:

```bash
cd deploy/compose
docker compose -f docker-compose.database.yml up -d postgres
docker compose -f docker-compose.database.yml run --rm gateway-db-bootstrap
```

For an existing volume, mounting another init file is insufficient because
`/docker-entrypoint-initdb.d` runs only on an empty data directory. The explicit
bootstrap command above is the fresh and existing-volume path. Back up the
database before adopting an existing volume into `loxilb-state`.

### Upgrading an existing database

Apply any migrations newer than your database, in order:

```bash
for f in database/migrations/*.sql; do
  echo "applying $f"
  PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -U "$DB_USER" -d "$DB_NAME" \
    -v ON_ERROR_STOP=1 -f "$f"
done
```

Release notes call out when a release requires a migration.

> There are currently no PostgreSQL migrations: `database/init/00-init-complete.sql`
> is the whole schema. The MySQL-era migrations are kept, unconverted, under
> `database/migrations/legacy-mysql/` for historical reference only — see the
> README there. There is **no in-place upgrade path from a MySQL deployment**;
> PostgreSQL support replaced MySQL outright.

## 3. TLS to the database

To encrypt the connection between the service and PostgreSQL, enable `-ssl-option`
and supply the client certificate material:

```bash
./loxilb-oam \
  -ssl-option=true \
  -ssl-ca-cert-file=./ssl/certs/root-ca.pem \
  -ssl-ca-client-cert-file=./ssl/certs/client-cert.pem \
  -ssl-ca-client-key-file=./ssl/certs/client-key.pem \
  -port=8080
```

The three paths above are the built-in defaults, so they can be omitted if your
files live there. `make run-ssl` wraps the same invocation using the Makefile's
`SSL_DB_*` variables.

The certificates must come from your PostgreSQL server's CA — generating them
is a PostgreSQL administration task and is outside the scope of this guide (see
the PostgreSQL manual, *Secure TCP/IP Connections with SSL*). Enabling this sets
`sslmode=verify-full`, so the server certificate must be valid for the hostname
in `DB_HOST`. If the service runs in a container,
mount the certificate directory in and use the in-container paths.

The same flags exist on the `reset_admin` tool; see
[cmd/reset_admin/README.md](../cmd/reset_admin/README.md).

> Database TLS is unrelated to the two other TLS hops in the system: the
> browser→edge hop ([deployment-compose.md](deployment-compose.md)) and the
> OAM→gateway-instance hop ([instance-tls.md](instance-tls.md)).

## Notes

- Ensure PostgreSQL is reachable and healthy **before** starting the service; it
  retries a bounded number of times and then exits.
- The service refuses to start without `OAM_JWT_SECRET`,
  `OAM_DEFAULT_ADMIN_PASSWORD`, and a database password. See
  [DEPLOYMENT.md](../DEPLOYMENT.md) for the full configuration reference.
- On a fresh database the bootstrap `admin` account is created from
  `OAM_DEFAULT_ADMIN_PASSWORD`. That password must satisfy the account policy
  (≥9 characters with upper, lower, digit and special; no character three times
  in a row) or startup aborts. Change it at first login.

For the schema reference, see **[oam-db.md](oam-db.md)**.
