# Running loxilb-oam Against a Database

`loxilb-oam` stores all of its state in MySQL 8.x or later. This guide covers
the two ways to provide that database when you are **not** using a bundled
Compose stack (which provisions and initializes MySQL for you):

1. [MySQL in a container](#1-mysql-in-a-container) — the quickest path for
   development against a locally built binary.
2. [An existing / external MySQL](#2-an-existing-or-external-mysql) — point the
   service at a database you already run.

Then, optionally, [TLS to the database](#3-tls-to-the-database).

> **Just want the service running?** Use one of the Compose paths instead — they
> create, initialize, and wire the database automatically:
> - Full management plane (UI + API + MySQL behind a TLS edge):
>   [deployment-compose.md](deployment-compose.md)
> - API + MySQL only, over HTTP: `docker compose up -d` from the repository root
>   (see [DEPLOYMENT.md](../DEPLOYMENT.md)).

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

## 1. MySQL in a container

Start just the database from the repository root — the root
`docker-compose.yml` mounts `database/init/` into the MySQL entrypoint, so the
schema is loaded automatically on first boot of an empty volume:

```bash
cp .env.example .env    # set MYSQL_ROOT_PASSWORD and DB_PASSWORD at minimum
docker compose up -d mysql
```

Confirm it initialized:

```bash
docker compose exec mysql sh -c \
  'exec mysql -uoamuser -p"$MYSQL_PASSWORD" loxioam -e "SHOW TABLES;"'
```

You should see all eleven tables listed above. Now build and run the service
against it:

```bash
make build

export OAM_JWT_SECRET="$(openssl rand -base64 48)"
export OAM_DEFAULT_ADMIN_PASSWORD='<bootstrap-admin-password>'
export DB_PASSWORD='<the value you put in .env>'

./loxilb-oam -db-host=127.0.0.1 -db-port=3306 -db-user=oamuser -db-name=loxioam -port=8080
```

The `-db-*` flags each default from the matching `DB_*` environment variable, so
if you exported the whole `DB_*` family you can simply run `./loxilb-oam`.
`make run` does the same thing using the Makefile's `DB_*` variables.

Verify: `curl http://localhost:8080/oam/health`.

### Reinitializing

The schema is only applied on first boot of an **empty** data volume. To start
over, destroy the volume:

```bash
docker compose down -v && docker compose up -d mysql
```

## 2. An existing or external MySQL

Create the database and an account for the service, then load the schema:

```bash
mysql -h <host> -u root -p <<'SQL'
CREATE DATABASE IF NOT EXISTS loxioam
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'oamuser'@'%' IDENTIFIED BY '<database-password>';
GRANT ALL PRIVILEGES ON loxioam.* TO 'oamuser'@'%';
FLUSH PRIVILEGES;
SQL

mysql -h <host> -u oamuser -p loxioam < database/init/00-init-complete.sql
```

Then point the service at it — either with flags or the `DB_*` environment
family:

```bash
export DB_HOST=<host> DB_PORT=3306 DB_USER=oamuser DB_NAME=loxioam
export DB_PASSWORD='<database-password>'
export OAM_JWT_SECRET="$(openssl rand -base64 48)"
export OAM_DEFAULT_ADMIN_PASSWORD='<bootstrap-admin-password>'

./loxilb-oam -port=8080
```

In the Compose bundles, set `DB_HOST` in `.env` to your external host — the
bundled `mysql` service is then unused.

### Upgrading an existing database

Apply any migrations newer than your database, in order:

```bash
for f in database/migrations/*.sql; do
  echo "applying $f"
  mysql -h "$DB_HOST" -u "$DB_USER" -p "$DB_NAME" < "$f"
done
```

Release notes call out when a release requires a migration.

## 3. TLS to the database

To encrypt the connection between the service and MySQL, enable `-ssl-option`
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

The certificates must come from your MySQL server's CA — generating them is a
MySQL administration task and is outside the scope of this guide (see the MySQL
manual, *Using Encrypted Connections*). If the service runs in a container,
mount the certificate directory in and use the in-container paths.

The same flags exist on the `reset_admin` tool; see
[cmd/reset_admin/README.md](../cmd/reset_admin/README.md).

> Database TLS is unrelated to the two other TLS hops in the system: the
> browser→edge hop ([deployment-compose.md](deployment-compose.md)) and the
> OAM→gateway-instance hop ([instance-tls.md](instance-tls.md)).

## Notes

- Ensure MySQL is reachable and healthy **before** starting the service; it
  retries a bounded number of times and then exits.
- The service refuses to start without `OAM_JWT_SECRET`,
  `OAM_DEFAULT_ADMIN_PASSWORD`, and a database password. See
  [DEPLOYMENT.md](../DEPLOYMENT.md) for the full configuration reference.
- On a fresh database the bootstrap `admin` account is created from
  `OAM_DEFAULT_ADMIN_PASSWORD`. That password must satisfy the account policy
  (≥9 characters with upper, lower, digit and special; no character three times
  in a row) or startup aborts. Change it at first login.

For the schema reference, see **[oam-db.md](oam-db.md)**.
