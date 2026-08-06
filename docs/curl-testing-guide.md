# Testing the LoxiLB Gateway Proxy with curl

Hands-on walkthrough of the proxy endpoints. For what the proxy does and how it
is authorized, see [proxy-functionality.md](proxy-functionality.md).

## Prerequisites

1. A running `loxilb-oam` server (see
   [DEPLOYMENT.md](../DEPLOYMENT.md) or
   [deployment-compose.md](deployment-compose.md)).
2. `jq` installed.
3. The bootstrap `admin` password (`OAM_DEFAULT_ADMIN_PASSWORD`).
4. Optionally, a reachable LoxiLB instance. Without one, proxied calls return
   `502` — which is still enough to exercise auth, RBAC, and routing.

> **Two things trip people up.** User creation is **admin-only** — there is no
> unauthenticated signup, so you must log in as `admin` first. And every
> password must satisfy the account policy: **≥9 characters with an uppercase
> letter, a lowercase letter, a digit and a special character; no character
> three times in a row; not equal to the username.**

## Step-by-Step Testing Guide

### 1. Start the server

```bash
cd /path/to/loxilb-oam
make build
./loxilb-oam -port=8080     # DB_* / OAM_* env must already be set
```

Or, if it is already running under Compose, just confirm it:

```bash
curl -s http://localhost:8080/oam/health
```

### 2. Log in as admin

```bash
ADMIN_TOKEN=$(curl -s -X POST http://localhost:8080/oam/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "<OAM_DEFAULT_ADMIN_PASSWORD>"
  }' | jq -r '.token')

echo "Admin token: ${ADMIN_TOKEN:0:20}..."
```

### 3. Create a test user (optional — requires admin)

`POST /oam/users` requires the `admin` role. Pick a role for the new account:
`admin`, `operator` (can drive the gateway), or `viewer` (read-only).

```bash
curl -X POST http://localhost:8080/oam/users \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "username": "testuser",
    "email": "test@example.com",
    "password": "TestPass1!",
    "role": "operator"
  }'
```

Then log in as that user to get its token:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/oam/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "testuser",
    "password": "TestPass1!"
  }' | jq -r '.token')
```

To test with admin privileges instead, just use `TOKEN=$ADMIN_TOKEN`.

### 4. Register a LoxiLB instance (requires admin)

Registering, updating, and deleting instances all require the `admin` role —
`operator` has read access only. Use `$ADMIN_TOKEN` here.

```bash
curl -X POST http://localhost:8080/oam/loxilbs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{
    "name": "test-loxilb",
    "host": "127.0.0.1",
    "port": "11111",
    "protocol": "http",
    "description": "Test LoxiLB instance",
    "version": "v1",
    "cimage": "ghcr.io/loxilb-io/loxilb",
    "ctag": "latest"
  }'
```

Field rules worth knowing — the body is fully validated, and a violation is
`400` naming the offending `field`:

| Field | Rule |
|-------|------|
| `name` | **required**; letters, digits, `.`, `-`, `_`; must start alphanumeric; ≤63 chars; unique |
| `host` | **required**; hostname, IPv4 literal, or **bracketed** IPv6 (`[2001:db8::1]`) |
| `port` | **required**; 1–65535 |
| `protocol` | **required**; exactly `http` or `https`. It is deliberately not defaulted |
| `version` | defaults to `v1`; a plain path segment |
| `cimage` / `ctag` | OCI image reference and tag, kept in separate fields — do not put `:tag` in `cimage` |

The derived endpoint is `{protocol}://{host}:{port}/netlox/{version}`. Both the
name and that endpoint are unique: a collision returns **`409 Conflict`**, not
`400`.

### 5. Get the instance ID

Reads are open to any authenticated role, so either token works.

```bash
INSTANCE_ID=$(curl -s -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/oam/loxilbs | jq -r '.[0].id')

echo "Instance ID: $INSTANCE_ID"
```

### 6. Test Proxy Requests

#### Get LoxiLB Metadata
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/oam/loxilbs/$INSTANCE_ID/netlox/v1/meta
```

#### Get All Load Balancers
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer/all
```

#### Get Device Status
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/oam/loxilbs/$INSTANCE_ID/netlox/v1/status/device
```

#### Create a Load Balancer (POST — requires `gateway_write`)

Mutating proxy calls need the `gateway_write` capability (`admin` or
`operator`). A `viewer` token gets `403` here while the `GET`s above still work.

```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "serviceArguments": {
      "externalIP": "192.168.1.100",
      "port": 80,
      "protocol": "tcp",
      "endpoints": [
        {
          "endpointIP": "10.0.0.1",
          "targetPort": 8080,
          "weight": 1
        }
      ]
    }
  }' \
  http://localhost:8080/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer
```

## Complete Test Script

Save and run. It logs in as the bootstrap admin, registers a throwaway
instance, exercises the proxy, and cleans up.

```bash
#!/bin/bash
# Requires: jq, and OAM_ADMIN_PASSWORD set to OAM_DEFAULT_ADMIN_PASSWORD.

OAM_HOST="${OAM_HOST:-localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASSWORD="${OAM_ADMIN_PASSWORD:?set OAM_ADMIN_PASSWORD to the bootstrap admin password}"

echo "=== Testing the LoxiLB gateway proxy ==="

# 1. Log in as admin (user creation and instance registration are admin-only)
echo "1. Logging in as $ADMIN_USER..."
TOKEN=$(curl -s -X POST http://$OAM_HOST/oam/login \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASSWORD\"}" | jq -r '.token')

if [ "$TOKEN" = "null" ] || [ -z "$TOKEN" ]; then
    echo "❌ Failed to get authentication token"
    exit 1
fi

echo "✅ Got token: ${TOKEN:0:20}..."

# 2. Register a LoxiLB instance. protocol is required; name and the derived
#    endpoint are unique, so a rerun without cleanup returns 409.
echo "2. Registering LoxiLB instance..."
INSTANCE_RESPONSE=$(curl -s -X POST http://$OAM_HOST/oam/loxilbs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "name": "test-proxy-instance",
    "host": "127.0.0.1",
    "port": "11111",
    "protocol": "http",
    "description": "Test proxy instance",
    "version": "v1",
    "cimage": "ghcr.io/loxilb-io/loxilb",
    "ctag": "latest"
  }')

INSTANCE_ID=$(echo "$INSTANCE_RESPONSE" | jq -r '.id')
if [ "$INSTANCE_ID" = "null" ] || [ -z "$INSTANCE_ID" ]; then
    echo "❌ Failed to register instance: $INSTANCE_RESPONSE"
    exit 1
fi
echo "✅ Registered instance with ID: $INSTANCE_ID"

# 3. Test proxy requests
echo "3. Testing proxy requests..."

echo "   - Testing metadata endpoint..."
RESPONSE=$(curl -s -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/meta)

HTTP_CODE="${RESPONSE: -3}"
if [ "$HTTP_CODE" = "200" ]; then
    echo "   ✅ Metadata request successful (200)"
elif [ "$HTTP_CODE" = "502" ]; then
    echo "   ⚠️ LoxiLB instance not reachable (502) - Expected if no LoxiLB running"
else
    echo "   ❌ Unexpected response code: $HTTP_CODE"
fi

echo "   - Testing load balancer endpoint..."
RESPONSE=$(curl -s -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer/all)

HTTP_CODE="${RESPONSE: -3}"
if [ "$HTTP_CODE" = "200" ]; then
    echo "   ✅ Load balancer request successful (200)"
elif [ "$HTTP_CODE" = "502" ]; then
    echo "   ⚠️ LoxiLB instance not reachable (502) - Expected if no LoxiLB running"
else
    echo "   ❌ Unexpected response code: $HTTP_CODE"
fi

# 4. Test error cases
echo "4. Testing error cases..."

echo "   - Testing invalid instance ID..."
RESPONSE=$(curl -s -w "%{http_code}" \
  -H "Authorization: Bearer $TOKEN" \
  http://$OAM_HOST/oam/loxilbs/99999/netlox/v1/meta)

HTTP_CODE="${RESPONSE: -3}"
if [ "$HTTP_CODE" = "404" ]; then
    echo "   ✅ Invalid instance ID correctly returns 404"
else
    echo "   ❌ Expected 404, got: $HTTP_CODE"
fi

echo "   - Testing without authentication..."
RESPONSE=$(curl -s -w "%{http_code}" \
  http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/meta)

HTTP_CODE="${RESPONSE: -3}"
if [ "$HTTP_CODE" = "401" ]; then
    echo "   ✅ Unauthenticated request correctly returns 401"
else
    echo "   ❌ Expected 401, got: $HTTP_CODE"
fi

# 5. Cleanup
echo "5. Cleaning up..."
curl -s -X DELETE http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID \
  -H "Authorization: Bearer $TOKEN" > /dev/null
echo "✅ Deleted test instance"

echo "=== Test completed ==="
```

## Manual Testing Commands

### Quick Test Commands
```bash
# Set variables
export OAM_HOST="localhost:8080"
export TOKEN="your-jwt-token-here"
export INSTANCE_ID="1"

# Test different endpoints
curl -H "Authorization: Bearer $TOKEN" http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/meta
curl -H "Authorization: Bearer $TOKEN" http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer/all
curl -H "Authorization: Bearer $TOKEN" http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/status/device
curl -H "Authorization: Bearer $TOKEN" http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/route/all
```

### POST Request Example
```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"serviceArguments": {"externalIP": "192.168.1.100", "port": 80, "protocol": "tcp"}}' \
  http://$OAM_HOST/oam/loxilbs/$INSTANCE_ID/netlox/v1/config/loadbalancer
```

## Expected Responses

### Success (200)
If LoxiLB is running and reachable, you'll get the actual LoxiLB API response.

### Bad Gateway (502)
```json
{"error": "LoxiLB instance unreachable"}
```

### Not Found (404)
```json
{"error": "LoxiLB instance not found"}
```

### Unauthorized (401)
```json
{"error": "unauthorized"}
```

### Gateway Timeout (504)
```json
{"error": "Request timeout"}
```

## Troubleshooting

| Code | Cause / fix |
|------|-------------|
| `400 Bad Request` | On instance registration, the body failed validation — the response names the offending `field`. `protocol` is required and must be `http` or `https`. |
| `401 Unauthorized` | Token missing, malformed, expired, or revoked by a logout. Log in again. |
| `403 Forbidden` | Your role lacks the capability: mutating proxy calls need `gateway_write` (`admin`/`operator`), and instance registration needs `admin`. |
| `404 Not Found` | The instance ID is not registered. List them with `GET /oam/loxilbs`. |
| `409 Conflict` | The instance name or its derived endpoint is already taken. Delete the old row or pick a different name/host/port. |
| `429 Too Many Requests` | Per-IP rate limit — 50 rps on the proxy, much tighter on `/oam/login`. Back off and retry. |
| `502 Bad Gateway` | The LoxiLB instance is not running or unreachable. Expected when testing without one. |
| `504 Gateway Timeout` | The instance did not respond within 10 seconds. |

If you enabled TLS to instances, a `502` may also mean certificate
verification failed — check `OAM_INSTANCE_CA_BUNDLE` and the certificate SANs
against the registered host ([instance-tls.md](instance-tls.md)).

## Notes

- Replace `localhost:8080` with your actual OAM server address. Behind the
  management-plane bundle's Caddy edge, the API is at `/api/oam/...` rather than
  `/oam/...`.
- Without a running LoxiLB instance you get `502` on proxied calls — expected,
  and still a valid test of auth, RBAC, and routing.
- All proxy requests are logged; check the OAM logs (`GET /oam/logs` or
  `docker compose logs -f oam-loxilb`) when debugging.
