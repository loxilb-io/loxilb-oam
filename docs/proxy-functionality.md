# LoxiLB Proxy Functionality

## Overview

The OAM-LoxiLB now includes proxy functionality that allows clients to make API calls to LoxiLB instances through the OAM server instead of calling LoxiLB instances directly.

## Architecture Change

### Before (Direct Access):
```
Client → LoxiLB Instance API (https://loxilb-host:11111/netlox/v1/...)
```

### After (Proxy Access):
```
Client → OAM-LoxiLB Proxy → LoxiLB Instance API
```

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

## Authentication

- All proxy requests require OAM authentication (Bearer token)
- The proxy forwards requests to LoxiLB instances without additional authentication
- Any authenticated OAM user can proxy to any LoxiLB instance

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
- `400 Bad Request` - Invalid instance ID or missing path
- `404 Not Found` - LoxiLB instance not found in database
- `502 Bad Gateway` - LoxiLB instance unreachable
- `504 Gateway Timeout` - Request timeout (10 seconds)

### Supported HTTP Methods
The proxy supports all HTTP methods:
- GET
- POST
- PUT
- DELETE
- PATCH

## Configuration

### Timeouts
- Request timeout: 10 seconds
- Connection timeout: 10 seconds

### Request Limits
- No size limits on request/response bodies
- Headers are forwarded transparently (excluding hop-by-hop headers)

## Usage Examples

### Using curl:

1. **First, authenticate with OAM:**
   ```bash
   TOKEN=$(curl -s -X POST http://localhost:8080/oam/login \
     -H "Content-Type: application/json" \
     -d '{"username":"admin","password":"password"}' | jq -r .token)
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
