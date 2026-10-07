# Appliance operations (alpha)

> **Status: alpha.** Contract `appliance-ops/v1alpha1`. Names, fields and enum
> values may change before the contract is agreed with its consumers. OAM's
> side is complete — plan, authorize, submit, follow, cancel — but **no real
> host adapter exists yet**: the only adapter is a fixture that executes
> nothing. Nothing here has been run against an Appliance.

An *Appliance* is an installation where OAM, its database and the gateway are
delivered and operated as one unit. Backing that unit up, restoring, updating,
rolling back or resetting it needs privileges on the host that OAM does not
have and should not have. A separate **host adapter** holds them; OAM
authorizes, records and reports.

A deployment that is not an Appliance has no host adapter. The API below is
still present there and says so, rather than returning `404`.

## Endpoints

All require authentication and the `appliance_read` capability, which every
role holds. Planning an operation, and authorizing, submitting, cancelling or
reconciling one, additionally require the capability for its type.

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
nothing on the host. A planned operation is then *submitted*, and OAM follows
the host adapter until it ends:

```
backup:    plan ────────────────► submit ──► QUEUED ► RUNNING ► VERIFYING ► SUCCEEDED
the rest:  plan ──► authorize ──► submit ──►   …
                    (password)    (challenge)
```

Only one operation can be active on an installation at a time — from
authorization (or, for a backup, submission) until it reaches a terminal
state.

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

### `POST /oam/v1/appliance/operations/{operation_id}/authorize`

Required before submitting anything with `requires_reauthentication: true`.

```http
POST /oam/v1/appliance/operations/{operation_id}/authorize
Content-Type: application/json

{ "password": "<the caller's current password>" }
```

```json
{ "challenge": "<64 hex characters>", "expires_at": "…", "operation_id": "…", "plan_hash": "…" }
```

- The challenge is returned once (`Cache-Control: no-store`). OAM keeps only
  its SHA-256. It is good for **one** submit of **this** operation and plan, by
  **this** user in **this** session (the token that authorized), on this
  installation.
- It expires after 5 minutes, or with the plan if that is sooner.
- Authorizing again replaces the previous challenge.
- The operation becomes `AWAITING_AUTHORIZATION` and occupies the
  installation until it is submitted, cancelled, or its plan expires.
- Everything that does not depend on the password is checked first, so a
  request that could never be authorized does not cost an attempt.
- A wrong password counts as a failed login: the same lockout, keyed by user
  and client address, with the same thresholds. A locked-out caller gets `429
  TOO_MANY_ATTEMPTS` and `Retry-After`, and cannot log in either until it
  lapses. The endpoint also shares the login endpoint's per-address rate
  limit, whose `429` is the API's ordinary body, not the envelope.
- A session whose token carries no `jti` (issued before tokens had one) cannot
  authorize: `403 REAUTHENTICATION_REQUIRED`. Logging in again resolves it.

| Status | `code` | Meaning |
|---|---|---|
| `401` | `REAUTHENTICATION_FAILED` | Wrong password. |
| `403` | `PERMISSION_DENIED`, `REAUTHENTICATION_REQUIRED` | |
| `409` | `AUTHORIZATION_NOT_REQUIRED` | The operation is a backup. |
| `409` | `OPERATION_CONFLICT` | Another operation is active; `recovery.operation_id` names it. |
| `409` | `OPERATION_STATE_INVALID` | Already submitted, or cancelled. |
| `410` | `PLAN_EXPIRED` | Plan again. |
| `429` | `TOO_MANY_ATTEMPTS` | Locked out. |

### `POST /oam/v1/appliance/operations/{operation_id}/submit`

```http
POST /oam/v1/appliance/operations/{operation_id}/submit
Content-Type: application/json

{ "plan_hash": "<the plan_hash that was reviewed>", "challenge": "<from authorize>" }
```

`challenge` is required exactly when the operation requires
reauthentication. The answer is `202` and the operation as it stands, usually
`QUEUED`; follow it with `GET`.

Submit takes no `Idempotency-Key`: it is idempotent on the operation.
Submitting an operation that was already submitted returns it unchanged, with
`202`, and starts nothing.

In one database transaction OAM consumes the challenge, moves the operation to
`QUEUED` and writes the audit record; only then does it ask the host adapter.
So:

- of any number of simultaneous submits presenting one challenge, one is
  accepted and the host starts one job;
- if the adapter cannot be reached, the submit still answers `202`: the
  operation is `QUEUED` with `host_generation: 0` and `stale: true`, and OAM
  delivers it when the adapter answers — also after OAM itself restarts;
- if the adapter refuses the job (the installation changed since planning),
  the operation is `FAILED` with the adapter's `error_code` and
  `error_origin: "host"`.

| Status | `code` | Meaning |
|---|---|---|
| `400` | `INVALID_REQUEST`, `CHALLENGE_REQUIRED` | |
| `403` | `PERMISSION_DENIED` | Including a role that lost the capability since planning. |
| `403` | `CHALLENGE_MISMATCH` | Unknown, or issued for another operation, plan, user or session. Deliberately one code for all of these. |
| `403` | `REAUTHENTICATION_REQUIRED` | The session cannot hold a challenge. |
| `409` | `PLAN_STALE` | `plan_hash` is not this operation's. |
| `409` | `CHALLENGE_CONSUMED` | |
| `409` | `OPERATION_CONFLICT` | Another operation is active; `recovery.operation_id` names it. |
| `409` | `OPERATION_STATE_INVALID` | Cancelled, or changed state while the request was in flight. |
| `410` | `PLAN_EXPIRED`, `CHALLENGE_EXPIRED` | |

### `POST /oam/v1/appliance/operations/{operation_id}/cancel`

No body. `202` and the operation.

- Before submission it always succeeds: the operation becomes `CANCELLED`
  with `error_code: "CANCELLED_BY_USER"`.
- After submission the host adapter decides. `cancellable` on the operation is
  its last word on whether it would agree; once the operation has passed
  `plan.irreversible_after_phase` the answer is `409
  OPERATION_NOT_CANCELLABLE`.
- If the adapter cannot be reached: `502 HOST_UNREACHABLE`, nothing changed.
- A submission the adapter never received is cancelled by OAM, and is then
  never delivered. Should OAM deliver it while the cancel is in flight, the
  answer is `409 OPERATION_STATE_INVALID` — never a cancellation that did not
  happen; ask again and the adapter decides.
- Cancelling a cancelled operation returns it; cancelling one that ended any
  other way is `409 OPERATION_STATE_INVALID`.

### `POST /oam/v1/appliance/operations/{operation_id}/reconcile`

No body. `200` and the operation. OAM reads the host adapter's journal for
every unfinished operation every 2 seconds on its own; this reads it for one
operation now. It is never needed for correctness and never causes anything
to be executed.

### States

| State | Set by | |
|---|---|---|
| `PLANNED` | OAM | Planned, not submitted. |
| `AWAITING_AUTHORIZATION` | OAM | A challenge was issued. Occupies the installation. |
| `QUEUED` | OAM, then host | Submitted. `host_generation: 0` means the adapter has not confirmed it yet. |
| `RUNNING`, `VERIFYING` | host | `phase` says where. |
| `COMPENSATING` | host | Failed past the point of no return; the host is undoing it. |
| `SUCCEEDED`, `FAILED`, `ROLLED_BACK`, `CANCELLED` | host, or OAM before submission | Terminal. `finished_at` is set. |
| `RECOVERY_REQUIRED` | host or OAM | Automation has ended and an operator must look. **Not terminal: it keeps the installation occupied**, and every action reports `unavailable_reason: "RECOVERY_REQUIRED"`. There is no API to clear it yet. |

Fields that describe how well OAM knows the state:

- `host_generation` — the generation of the adapter's journal entry this
  operation reflects. An entry is applied only if its generation is higher
  than the one stored, so a repeated or late answer changes nothing.
- `stale` — the adapter could not be read at the last attempt; `state` is the
  last one known. `reconciliation` is then `HOST_UNREACHABLE`.
- `reconciliation: "HOST_UNKNOWN"` with `error_code: "HOST_JOB_LOST"` — the
  adapter's journal no longer has an operation it had reported on. OAM does
  **not** submit it again, since it cannot know how far it got; the operation
  becomes `RECOVERY_REQUIRED`.
- `error_code` / `error_origin` — why it ended as it did, and whether OAM
  (`PLAN_EXPIRED`, `CANCELLED_BY_USER`, `HOST_JOB_LOST`) or the host said so.

### Audit trail

Every plan, authorization (granted or denied), submit (accepted or denied),
cancel request and state change is one row of `appliance_audit`: operation,
event, actor, request ID, client address, old and new state, and a small
`detail` object whose fields are fixed in code. It never contains a password,
a challenge, a token or archive content. Rows for what OAM learned from the
host have no actor. The trail is in the database only; there is no API to
read it yet.

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
  `WAIT_FOR_OPERATION`, `CONTACT_SUPPORT`, `NONE`. With `WAIT_FOR_OPERATION`,
  `recovery.operation_id` is the operation in the way.
- `operation_id` is present on errors from routes under one operation.

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

The adapter serves:

| | |
|---|---|
| `GET /v1/capabilities`, `GET /v1/identity` | What the installation is and offers. |
| `POST /v1/plans` | Validate a request. No side effects. |
| `POST /v1/jobs` | Execute a plan. **Idempotent on `operation_id`**: a job the journal already holds is returned as it stands and nothing is started. The request carries `installation_id` and `plan_hash`; the adapter refuses a job planned for another installation or a plan that no longer holds. |
| `GET /v1/jobs/{operation_id}` | The journal entry; `404` if there is none. |
| `POST /v1/jobs/{operation_id}/cancel` | Stop it if that is still possible; refuse with `422` if not. |

A journal entry is `{operation_id, state, phase, generation, cancellable,
error_code}`. `generation` starts at 1 and rises with every change. OAM only
ever asks; nothing is pushed to it, so the journal must survive an adapter
restart — a job OAM was told about and can no longer find becomes
`RECOVERY_REQUIRED`.

The adapter refuses a request it understood with `422` and a body of
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

A submitted job walks a fixed script — `QUEUED`, `RUNNING/prepare`, a
type-specific phase, `VERIFYING/verify`, `SUCCEEDED` — one step per `-step`
(default `2s`). `-outcome fail` ends it in `FAILED` after `prepare`;
`-outcome recovery` in `COMPENSATING` and then `RECOVERY_REQUIRED`. Its
journal is in memory: restarting the fixture loses it, which is itself a way
to exercise `HOST_JOB_LOST`.

A result obtained against the fixture shows how OAM behaves. It is not evidence
that an Appliance works.
