# PostgreSQL migrations

Schema changes made after the baseline (`database/init/00-init-complete.sql`,
schema version 1) live here, one file per version:

```
0002_add_operations.sql
0003_add_operation_audit.sql
```

- The name is `NNNN_lower_snake_case.sql`. `NNNN` is the schema version; it
  starts at `0002` and has no gaps.
- A file is applied once, in one transaction, by the server at startup (or by
  `loxilb-oam -migrate`). Statements that cannot run inside a transaction —
  `CREATE INDEX CONCURRENTLY`, `VACUUM` — are not supported.
- A released file is immutable. Its SHA-256 is recorded when it is applied and
  compared on every start; an edited file stops the server. Fix a mistake with
  a new migration.
- Migrations are forward-only. There are no down files: rolling a release back
  means restoring the database backup taken before the upgrade. A binary
  refuses to start against a schema newer than the one it was built with.
- Do not edit the baseline. `database/schema_test.go` pins its checksum.

See [docs/database-installation.md](../../../docs/database-installation.md) §2.
