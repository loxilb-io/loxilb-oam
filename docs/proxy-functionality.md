# LoxiLB Gateway Proxy

## Overview

`loxilb-oam` proxies the LoxiLB instance API, so clients call managed instances
through the OAM server rather than reaching each instance directly. This puts
every gateway call behind OAM authentication, role-based authorization, rate
limiting, and audit logging, and means clients only need to know one endpoint.

### Direct access
```
Client → LoxiLB Instance API (https://loxilb-host:11111/netlox/v1/...)
```

### Proxied access
```
Client → loxilb-oam proxy → LoxiLB Instance API
```

The instance-facing hop uses TLS with certificate verification when the instance
is registered with an `https://` endpoint — see
[instance-tls.md](instance-tls.md).

## Proxy URL Structure

All LoxiLB API calls can now be made through the OAM proxy using the following URL pattern:

```
/oam/loxilbs/{instance_id}/netlox/v1/{loxilb_api_path}
```

### Examples:

1. **Get Load Balancer Services:**
   ```
   GET /oam/loxilbs/1/netlox/v1/config/loadbalancer/all
   ```

2. **Create Load Balancer:**
   ```
   POST /oam/loxilbs/1/netlox/v1/config/loadbalancer
   Content-Type: application/json
   {...load balancer config...}
   ```

3. **Get System Status:**
   ```
   GET /oam/loxilbs/1/netlox/v1/status/device
   ```

4. **Delete Route:**
   ```
   DELETE /oam/loxilbs/1/netlox/v1/config/route/destinationIPNet/192.168.1.0/24
   ```

## Authentication and authorization

- **Every** proxy request requires OAM authentication (a `Bearer` token from
  `POST /oam/login`). Unauthenticated requests get `401`.
- Authorization is then gated **by HTTP method**:

  | Methods | Required capability | Roles allowed |
  |---------|--------------------|---------------|
  | `GET`, `HEAD`, `OPTIONS` | none beyond authentication | `admin`, `operator`, `viewer` |
  | `POST`, `PUT`, `PATCH`, `DELETE`, … | `gateway_write` | `admin`, `operator` |

  A `viewer` attempting any mutating call receives `403`. See
  `RequireGatewayCapability` in `internal/middleware/rbac.go`.
- The proxy authenticates *to* the LoxiLB instance only at the transport layer
  (TLS); it does not add application credentials to the forwarded request.
- Access is not scoped per instance: a role that may write can write to **any**
  registered instance.

## Rate limiting

Proxy requests are rate-limited per client IP — 50 requests/second sustained
with a burst of 100 (`ProxyRateLimitRPS` / `ProxyRateLimitBurst` in
`internal/config/constants.go`). Exceeding it returns `429 Too Many Requests`.
The credential endpoints (`/oam/login`, `/oam/setup/*`) have a much tighter,
separate budget.

## Features

### Request/Response Logging
All proxy requests are logged with the following information:
- Timestamp
- User ID (if available)
- LoxiLB instance ID
- HTTP method
- Original and target URLs
- Request size
- Response status code
- Response time in milliseconds
- Error details (if any)

### Error Handling
The proxy returns appropriate HTTP status codes:
- `400 Bad Request` — invalid instance ID or missing path
- `401 Unauthorized` — missing, expired, or revoked token
- `403 Forbidden` — the role lacks `gateway_write` for a mutating method
- `404 Not Found` — LoxiLB instance not registered
- `429 Too Many Requests` — per-IP rate limit exceeded
- `502 Bad Gateway` — LoxiLB instance unreachable
- `504 Gateway Timeout` — request timeout (10 seconds)

### Supported HTTP Methods
All methods are forwarded (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`,
`OPTIONS`), subject to the method-based authorization above.

## Configuration

### Timeouts
- Request timeout: 10 seconds
- Connection timeout: 10 seconds

### Request Limits
- No size limits are imposed on request/response bodies by the proxy itself
- Headers are forwarded transparently (excluding hop-by-hop headers)

## Usage Examples

### Using curl:

1. **First, authenticate with OAM:**
   ```bash
   TOKEN=$(curl -s -X POST http://localhost:8080/oam/login \
     -H "Content-Type: application/json" \
     -d '{"username":"admin","password":"<your-admin-password>"}' | jq -r .token)
   ```

2. **Get all load balancers from instance 1:**
   ```bash
   curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8080/oam/loxilbs/1/netlox/v1/config/loadbalancer/all
   ```

3. **Create a new load balancer:**
   ```bash
   curl -X POST -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"serviceArguments": {...}}' \
     http://localhost:8080/oam/loxilbs/1/netlox/v1/config/loadbalancer
   ```

### Using JavaScript/TypeScript:

```javascript
const oamBaseUrl = 'http://localhost:8080/oam';
const token = 'your-jwt-token';

// Get load balancers from instance 1
const response = await fetch(`${oamBaseUrl}/loxilbs/1/netlox/v1/config/loadbalancer/all`, {
  headers: {
    'Authorization': `Bearer ${token}`
  }
});

const loadBalancers = await response.json();
```

## Benefits

1. **Centralized Access Control**: All LoxiLB access goes through OAM authentication
2. **Logging and Monitoring**: All API calls are logged for audit and debugging
3. **Simplified Client Code**: Clients only need to know about OAM endpoints
4. **Future Extensibility**: Easy to add features like rate limiting, caching, or request transformation
5. **Consistent Error Handling**: Standardized error responses across all LoxiLB instances

## Migration Guide

### For existing clients:

**Old way:**
```javascript
// Direct LoxiLB API call
fetch('https://loxilb-host:11111/netlox/v1/config/loadbalancer/all')
```

**New way:**
```javascript
// Through OAM proxy
fetch('http://oam-host:8080/oam/loxilbs/1/netlox/v1/config/loadbalancer/all', {
  headers: { 'Authorization': 'Bearer ' + oamToken }
})
```

The API paths and request/response formats remain exactly the same - only the base URL and authentication method change.
