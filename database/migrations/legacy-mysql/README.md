# Legacy MySQL migrations (historical — do not run)

These files are the incremental schema history of loxilb-oam **while it ran on
MySQL**. They are retained for provenance: they explain *why* the current schema
looks the way it does (the RBAC role widening, the withdrawn OAuth columns, the
config-export table that snapshots replaced).

They are **not applicable to a PostgreSQL deployment** and are not translated:

- They are written in MySQL dialect (`USE loxioam`, `PREPARE`/`EXECUTE`,
  `MODIFY COLUMN`, `ON DUPLICATE KEY UPDATE`, `information_schema.statistics`).
- Most of them describe transitions that can never occur on PostgreSQL. A fresh
  PostgreSQL database has never had `oauth_provider` columns for `005` to drop,
  nor a `config_exports` table for `004` to remove.

The current schema is defined in one place:

    database/init/00-init-complete.sql

That file is the PostgreSQL baseline and already reflects the end state of every
migration listed here. New schema changes should be added as forward migrations
alongside it, in PostgreSQL dialect.

## Migrating data off an existing MySQL deployment

The PostgreSQL switch is a **breaking change** — there is no in-place upgrade
path in this repository. An existing MySQL deployment must be treated as a
separate database: stand up the PostgreSQL stack fresh, then move any data you
need across with a tool such as [pgloader](https://pgloader.io/). Note that
`instance_snapshots.snapshot_blob` (`MEDIUMBLOB` → `BYTEA`) and the former
`ENUM` columns need explicit type handling if you do.
