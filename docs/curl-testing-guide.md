# Testing OAM-LoxiLB Proxy with curl

## Prerequisites
1. Start the OAM-LoxiLB server
2. Have a LoxiLB instance configured in the database
3. Have a user account created

## Step-by-Step Testing Guide

### 1. Start the OAM-LoxiLB Server
```bash
cd /path/to/loxilb-oam
./oam-loxilb --port=8080
```

### 2. Create a User (if you don't have one)
```bash
curl -X POST http://localhost:8080/oam/users \
  -H "Content-Type: application/json" \
  -d '{
    "username": "testuser",
    "email": "test@example.com",
    "password": "testpass123"
  }'
```

### 3. Login and Get Authentication Token
```bash
# Login and extract token
TOKEN=$(curl -s -X POST http://localhost:8080/oam/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "testuser", 
    "password": "testpass123"
  }' | jq -r '.token')

echo "Token: $TOKEN"
```

### 4. Create a LoxiLB Instance (if you don't have one)
```bash
curl -X POST http://localhost:8080/oam/loxilbs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "name": "test-loxilb",
    "host": "127.0.0.1",
    "port": "11111",
    "description": "Test LoxiLB instance",
    "version": "v1",
    "cimage": "loxilb",
    "ctag": "latest"
  }'
```

### 5. Get LoxiLB Instance ID
```bash
# List all instances and get the ID
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

#### Create a Load Balancer (POST request)
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

Here's a complete test script you can save and run:

```bash
#!/bin/bash

# Configuration
OAM_HOST="localhost:8080"
USERNAME="testuser"
PASSWORD="testpass123"

echo "=== Testing OAM-LoxiLB Proxy ==="

# 1. Create user
echo "1. Creating user..."
curl -s -X POST http://$OAM_HOST/oam/users \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$USERNAME\",\"email\":\"test@example.com\",\"password\":\"$PASSWORD\"}" > /dev/null

# 2. Login and get token
echo "2. Logging in..."
TOKEN=$(curl -s -X POST http://$OAM_HOST/oam/login \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$USERNAME\",\"password\":\"$PASSWORD\"}" | jq -r '.token')

if [ "$TOKEN" = "null" ] || [ -z "$TOKEN" ]; then
    echo "❌ Failed to get authentication token"
    exit 1
fi

echo "✅ Got token: ${TOKEN:0:20}..."

# 3. Create LoxiLB instance
echo "3. Creating LoxiLB instance..."
INSTANCE_RESPONSE=$(curl -s -X POST http://$OAM_HOST/oam/loxilbs \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "name": "test-proxy-instance",
    "host": "127.0.0.1",
    "port": "11111",
    "description": "Test proxy instance",
    "version": "v1",
    "cimage": "loxilb",
    "ctag": "latest"
  }')

INSTANCE_ID=$(echo "$INSTANCE_RESPONSE" | jq -r '.id')
echo "✅ Created instance with ID: $INSTANCE_ID"

# 4. Test proxy requests
echo "4. Testing proxy requests..."

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

# 5. Test error cases
echo "5. Testing error cases..."

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

# 6. Cleanup
echo "6. Cleaning up..."
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

1. **401 Unauthorized**: Check if your token is valid and properly formatted
2. **404 Not Found**: Verify the instance ID exists in the database
3. **502 Bad Gateway**: LoxiLB instance is not running or unreachable
4. **504 Gateway Timeout**: LoxiLB instance is taking too long to respond

## Notes

- Replace `localhost:8080` with your actual OAM server address
- Replace instance ID with your actual LoxiLB instance ID
- If you don't have a LoxiLB instance running, you'll get 502 errors, which is expected
- The proxy will log all requests, so check the OAM logs for debugging
