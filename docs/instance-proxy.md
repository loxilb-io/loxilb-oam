# Instance proxy — error semantics

Everything the console does against a managed LoxiLB instance goes through
`/oam/loxilbs/{id}/netlox/*`, which OAM forwards to the instance's
`api_endpoint`. This page documents what a failure on that hop means, because
the answer is what an operator acts on.

## Why the distinction matters

A proxied request can fail for reasons that call for completely different
responses, and they used to be reported identically. Until 2026-09, every
failure of the outbound call — a timeout, a refused connection, a DNS failure,
a TLS rejection — was collapsed into one message and answered with:

```
502 {"error": "LoxiLB instance unreachable"}
```

That is a positive assertion that the instance is down. For a timeout it is
simply not established: an instance that accepts the connection and answers
slowly is reachable. Reporting it as unreachable sent operators to look at the
network for a problem that was not there.

It also made two console behaviours impossible to explain. The UI is fail-safe
on missing data, so a dropped `/config/meta` leaves the Add LB dialog with no
parameter schema — no fields, a disabled Create — and a dropped `/version`
leaves every AI-gateway field hidden. Neither is wrong on its own; both are
indefensible to someone who has just been told the instance is unreachable.

## The status contract

Failures are classified from the error itself, never from its text. String
matching is what let the old timeout branch quietly become unreachable code:
nothing fails when the strings drift apart, the branch just stops being taken.

| Status | `error` | When |
|---|---|---|
| `404` | `LoxiLB instance not found` | No instance with that id |
| `400` | `Failed to read request body` | The console's request body could not be read |
| `409` | *(the guard's own message)* | The rule collides with `OAM_RESERVED_ENDPOINTS` |
| `502` | `LoxiLB instance unreachable` | Connection refused, or a dial failure with no more specific cause |
| `502` | `LoxiLB instance address could not be resolved` | DNS lookup for the endpoint host failed |
| `502` | `TLS handshake with LoxiLB instance failed` | Reachable, but its certificate was not accepted — see [instance-tls.md](instance-tls.md) |
| `502` | `Connection to LoxiLB instance was reset` | The instance closed the connection before replying |
| `502` | `Incomplete response from LoxiLB instance` | It answered, then the body ended early |
| `502` | `Request to LoxiLB instance was cancelled` | The console hung up first — not the instance's fault |
| `503` | `Gateway service identity unavailable` | OAM has no usable outbound credential |
| `504` | `Request to LoxiLB instance timed out` | No answer within `OAM_PROXY_TIMEOUT` |
| `500` | `Proxy request failed` | Anything unclassified |

Every response carries `error`. Responses that have a cause to name also carry
an additive `detail` field — a curated classification, never the raw error.
The raw error, with the target URL, goes to the proxy log on the OAM host.

**`504` does not mean the instance is down.** It means OAM gave up waiting. If
a deployment's instances legitimately take longer on a large configuration,
raise `OAM_PROXY_TIMEOUT` (default `10s`) rather than reading the timeout as an
outage.

| Variable | Default | Effect |
|---|---|---|
| `OAM_PROXY_TIMEOUT` | `10s` | Per-request budget. Unparseable or non-positive values fall back to the default rather than disabling the timeout. |
