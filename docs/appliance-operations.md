# Appliance operations (alpha)

> **Status: alpha.** Contract `appliance-ops/v1alpha1`. Names, fields and enum
> values may change before the contract is agreed with its consumers. Only
> status and capability discovery exist; **no Appliance action can be executed
> through OAM yet.**

An *Appliance* is an installation where OAM, its database and the gateway are
delivered and operated as one unit. Backing that unit up, restoring, updating,
rolling back or resetting it needs privileges on the host that OAM does not
have and should not have. A separate **host adapter** holds them; OAM
authorizes, records and reports.

A deployment that is not an Appliance has no host adapter. The API below is
still present there and says so, rather than returning `404`.

## Endpoints

Both require authentication and the `appliance_read` capability, which every
role holds.

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
- `recovery.action` is one of `RETRY`, `REAUTHENTICATE`, `CONTACT_SUPPORT`,
  `NONE`.

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

The adapter serves `GET /v1/capabilities` and `GET /v1/identity`.

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

A result obtained against the fixture shows how OAM behaves. It is not evidence
that an Appliance works.
