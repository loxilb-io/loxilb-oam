# Gateway database bootstrap snapshot

`aigw-db-bootstrap.sql` is an exact snapshot of:

- repository: `loxilb-io/loxilb-inference-gateway`
- path: `scripts/aigw-db-bootstrap.sql`
- source commit: `ce6ef69e603afdc996c1392b278245425f4443e7`
- SHA-256: `569d940f0fdd995061b13a51e23434b428266e162e8cf162e5ba74cd1457a2f5`

The copy makes the released OAM deployment bundle self-contained. Do not edit
it independently. After a Gateway database-contract change, review the new SQL,
replace this snapshot, update the source identity and checksum, then run:

```bash
deploy/compose/scripts/check-aigw-bootstrap-drift.sh \
  ../loxilb-inference-gateway
```

The bootstrap provisions both Gateway role/schema pairs. Converged mode enables
only the `aigw` AI-key store; `aigw_mgmt` remains provisioned but dormant.
