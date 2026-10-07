# Appliance operations (alpha)

> **Status: alpha.** Contract `appliance-ops/v1alpha1`. Names, fields and enum
> values may change before the contract is agreed with its consumers. Status,
> capability discovery and *planning* exist; **no Appliance action can be
> executed through OAM yet** — a plan can be made and read, not submitted.

An *Appliance* is an installation where OAM, its database and the gateway are
delivered and operated as one unit. Backing that unit up, restoring, updating,
rolling back or resetting it needs privileges on the host that OAM does not
have and should not have. A separate **host adapter** holds them; OAM
authorizes, records and reports.

A deployment that is not an Appliance has no host adapter. The API below is
still present there and says so, rather than returning `404`.

## Endpoints

All require authentication and the `appliance_read` capability, which every
role holds. Planning additionally requires the capability for the operation
type.

### `GET /oam/v1/appliance/capabilities`

For each action — `backup`, `restore`, `update`, `rollback`, `reset`,
`diagnostics` — three independent answers:

| Field | Question |
|---|---|
| `supported` | Does the host adapter implement it, in a contract version OAM speaks? |
| `available` | Can it be executed right now? `unavailable_reason` says why not. |
| `permitted` | May *this caller's role* request it? |

`permitted` never depends on the other two, and the other two never depend on
who is asking. A client can therefore tell "you may not" from "it cannot be
done here" from "it cannot be done now".

| `unavailable_reason` | Meaning |
|---|---|
| `HOST_NOT_CONFIGURED` | This deployment has no host adapter. |
| `HOST_UNREACHABLE` | One is configured but did not answer, or rejected OAM's request. |
| `HOST_UNSUPPORTED` | The adapter does not implement this action. |
| `SCHEMA_MISMATCH` | The adapter speaks no contract version OAM does. |
| `OPERATION_IN_PROGRESS`, `RECOVERY_REQUIRED` | Reserved for when operations exist. |

`requires_reauthentication` is true for `restore`, `update`, `rollback` and
`reset`. `host_fixture: true` means the adapter is a test fixture and nothing it
reports describes a real installation.

```json
{
  "schema_version": "appliance-ops/v1alpha1",
  "host_configured": false,
  "host_fixture": false,
  "host_contract_versions": [],
  "actions": [
    { "action": "backup", "supported": false, "available": false,
      "unavailable_reason": "HOST_NOT_CONFIGURED", "permitted": true,
      "requires_reauthentication": false }
  ]
}
```

### `GET /oam/v1/appliance/status`

- `product` — model, installation ID, release version and digest, as reported
  by the host adapter. Absent when there is none.
- `components[]` — `oam`, `database`, and whatever the host adapter reports,
  each with `version`, `liveness`, `readiness`, `observed_at` and `stale`.
- `database` — OAM's schema version, the newest applied migration and when it
  was applied. `schema_version` is `0` when the schema is not tracked
  (`OAM_DB_MIGRATE=off` on a database the server never migrated).

Liveness and readiness are observed separately. A component that could not be
observed for this response is `"unknown"` with `stale: true`; it is never
reported as ready. Managed gateway instances are not listed yet.

## Operations

An operation is one backup, restore, update, rollback or reset. It is
*planned* first: the host adapter validates the request against the
installation and says what executing it would involve. Planning changes
nothing on the host.

### `POST /oam/v1/appliance/operations` — plan

```http
POST /oam/v1/appliance/operations
Idempotency-Key: <unique-per-operation>
Content-Type: application/json

{ "schema_version": "appliance-ops/v1alpha1", "type": "restore", "archive_ref": "backup-2026-10-01" }
```

| Field | |
|---|---|
| `type` | `backup`, `restore`, `update`, `rollback`, `reset`. |
| `archive_ref` | Required for `restore`, refused otherwise. Names an archive the host has admitted; never a path, and nothing is uploaded through OAM. |
| `target_release_ref` | Required for `update`, refused otherwise. |
| `note` | Optional free text, up to 500 characters. |

Unknown fields are refused.

`Idempotency-Key` (16–128 printable ASCII characters) is required and makes
the call safe to retry:

- the same key with the same request returns the operation made the first
  time, with `200` instead of `201`;
- the same key with a different request is `409 IDEMPOTENCY_KEY_REUSED`;
- keys are per user;
- a request that was refused creates nothing, so its key can be used again.

The response is the operation, in state `PLANNED`:

- `id` — a UUIDv7; IDs sort by creation time.
- `plan_hash` and `plan` — what the host established: `affected_resources`,
  release and archive digests, `compatibility`, and
  `irreversible_after_phase`, the point after which the operation could no
  longer be cancelled.
- `plan_expires_at` — 15 minutes after planning. An unsubmitted plan then
  becomes `CANCELLED` with `error_code: "PLAN_EXPIRED"`; plan again.
- `requires_reauthentication` — true for everything except `backup`.
- `host_fixture` — the plan came from a fixture adapter and describes nothing
  real.

| Status | `code` | Meaning |
|---|---|---|
| `400` | `INVALID_REQUEST`, `IDEMPOTENCY_KEY_INVALID`, `SCHEMA_MISMATCH` | The request is malformed. |
| `403` | `PERMISSION_DENIED` | The caller's role may not run this operation type. |
| `409` | `IDEMPOTENCY_KEY_REUSED` | |
| `422` | the host's code, e.g. `ARCHIVE_NOT_FOUND`; `origin: "host"` | The host adapter understood the request and refuses it. |
| `501` | `HOST_NOT_CONFIGURED` | This deployment has no host adapter. |
| `502` | `HOST_UNREACHABLE`; `origin: "host"` | The adapter did not answer. |

### `GET /oam/v1/appliance/operations/{operation_id}` and `GET /oam/v1/appliance/operations`

Every role may read operations. The plan — `plan`, `plan_hash` and `note` — is
returned only to callers whose role may run that operation type; for everyone
else the operation carries `redacted: true` and its state.

The list is newest first. `items` is always an array. Query parameters:
`limit` (1–100, default 20), `cursor` (the previous page's `next_cursor`),
`state`, `type`.

### States

`PLANNED` and `CANCELLED` are the only states an operation can be in today.
The contract reserves `AWAITING_AUTHORIZATION`, `QUEUED`, `RUNNING`,
`VERIFYING`, `SUCCEEDED`, `FAILED`, `COMPENSATING`, `ROLLED_BACK` and
`RECOVERY_REQUIRED` for when operations can be submitted.

## Errors

Failures on these endpoints use an envelope that extends the `{"error": "…"}`
body of the rest of the API:

```json
{
  "error": "Forbidden: your role does not permit this operation",
  "code": "PERMISSION_DENIED",
  "origin": "oam",
  "request_id": "5f2c9e0b7a1d4c3e8b6a0f12",
  "recovery": { "action": "NONE" }
}
```

- `code` is stable and never localized. Branch on it, not on `error`.
- `origin` is `oam`, `host` or `gateway`, and is repeated in the
  `X-Loxi-Error-Origin` header.
- `request_id` is the `X-Request-ID` the caller sent (letters, digits, `.`,
  `_`, `-`; at most 64 characters), or one OAM generated. It is returned in the
  `X-Request-ID` response header on success too.
- `recovery.action` is one of `RETRY`, `REAUTHENTICATE`, `REPLAN`,
  `CONTACT_SUPPORT`, `NONE`.

A request with no valid session is rejected before it reaches these endpoints
and keeps the API's ordinary `401` body.

## Permissions

| Capability | admin | operator | viewer |
|---|---|---|---|
| `appliance_read` | ✔ | ✔ | ✔ |
| `appliance_backup`, `appliance_restore`, `appliance_update`, `appliance_rollback`, `appliance_reset`, `appliance_diagnostics` | ✔ | — | — |

Each is granted on its own. Holding `config_write`, or any other existing
capability, implies none of them.

## Connecting a host adapter

| Variable | Meaning |
|---|---|
| `OAM_APPLIANCE_HOST_SOCKET` | Path of the Unix socket the adapter listens on. |
| `OAM_APPLIANCE_HOST_KEY_FILE` | File holding the request-signing key OAM and the adapter share; at least 32 bytes, surrounding whitespace ignored. |

Unset both and OAM is not an Appliance. Set only one, or point the key at a
file that cannot be read, and OAM refuses to start: an Appliance must not come
up believing it has no host adapter.

OAM calls the adapter; the adapter never calls OAM. Every request carries

```
X-Appliance-Timestamp: <unix seconds>
X-Appliance-Nonce:     <random, hex>
X-Appliance-Signature: v1=<hex HMAC-SHA256(key, method \n request-URI \n timestamp \n nonce \n hex(sha256(body)))>
```

The adapter rejects a request whose signature does not verify, whose timestamp
is more than 60 seconds from its clock, or whose nonce it has already seen.
Access to the socket file is the first line of defence; the signature is the
second.

Responses are not signed. OAM trusts whatever answers on the socket path, so
the directory holding the socket must be writable only by the adapter: a
process able to replace the socket could describe an installation that does not
exist. It could not make OAM's requests verify anywhere else, since it does not
hold the key.

The adapter serves `GET /v1/capabilities`, `GET /v1/identity` and
`POST /v1/plans`. It refuses a plan it understood with `422` and a body of
`{"code": "UPPER_SNAKE_CASE", "message": "…"}`; OAM relays the code and not the
message.

## Fixture host adapter

`cmd/appliance-host-fixture` implements the adapter protocol, including request
authentication, and **executes nothing**. It exists so OAM and its clients can
be developed before a real adapter does. Everything it returns is marked
`"fixture": true`, which OAM passes on as `host_fixture` and `product.fixture`.

```bash
head -c 48 /dev/urandom | base64 > /tmp/appliance.key
go run ./cmd/appliance-host-fixture \
  -socket /tmp/appliance.sock -key-file /tmp/appliance.key -available backup

OAM_APPLIANCE_HOST_SOCKET=/tmp/appliance.sock \
OAM_APPLIANCE_HOST_KEY_FILE=/tmp/appliance.key \
  ./loxilb-oam -port=8080
```

The fixture plans any action listed in `-available` against an invented
installation. An `archive_ref` or `target_release_ref` of `missing` is
rejected, to exercise the host-refusal path.

A result obtained against the fixture shows how OAM behaves. It is not evidence
that an Appliance works.
