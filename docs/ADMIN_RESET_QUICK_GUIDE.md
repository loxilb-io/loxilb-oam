# Admin Reset - Quick Reference Guide

This guide shows how to reset the administrator account in different deployment
scenarios.

Admin reset is a **local break-glass operation**. It is performed with the
`reset_admin` CLI (or the `scripts/reset-admin.sh` helper), which requires shell
access to the host or container. There is no unauthenticated HTTP reset endpoint
by design — it would be a critical security hole.

After a reset, the admin account returns to the bootstrap credentials
(`admin` / `OAM_DEFAULT_ADMIN_PASSWORD`). Change them immediately via
`POST /oam/setup/update-admin`, shown below. That endpoint needs no bearer
token — it authenticates on the current username and password carried in the
body, and is rate-limited per client IP — so it works even when you have no
usable session.

## Deployment-Specific Examples

### 1. Docker Compose Deployment

The bundled stack sets the `DB_*` connection vars in the container, so the reset
runs with no extra flags (container name: `oam-loxilb-app`):

```bash
# Option A: Using the helper script (inside the container)
docker exec -it oam-loxilb-app sh -c "./scripts/reset-admin.sh --confirm"

# Option B: Using the binary directly
docker exec -it oam-loxilb-app ./reset_admin --confirm
```

### 2. Kubernetes Deployment

> The Kubernetes manifests in `k8s/` are pre-release and not supported for this
> release (see [DEPLOYMENT.md](../DEPLOYMENT.md#kubernetes-deployment)). The
> commands below apply to any deployment that runs the OAM image in a Pod.

```bash
# Find the pod name
POD_NAME=$(kubectl get pods -n oam-loxilb -l app=oam-loxilb -o jsonpath='{.items[0].metadata.name}')

# Option A: Using the binary
kubectl exec -n oam-loxilb $POD_NAME -- ./reset_admin --confirm

# Option B: Using the helper script
kubectl exec -n oam-loxilb $POD_NAME -- sh -c "./scripts/reset-admin.sh --confirm"
```

### 3. Local Development

```bash
# Option A: Using go run
go run cmd/reset_admin/main.go --confirm

# Option B: Using the helper script
./scripts/reset-admin.sh --confirm
```

### 4. Production Server (Binary)

```bash
# If the reset_admin binary is in the same directory as loxilb-oam
./reset_admin --confirm

# Or via the helper script
./scripts/reset-admin.sh --confirm
```

## After Reset - Next Steps

### 1. Log In With the Bootstrap Credentials

```bash
curl -X POST http://localhost:8080/oam/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "<OAM_DEFAULT_ADMIN_PASSWORD>"
  }'
```

Response:

```json
{
  "token": "<jwt-token>",
  "message": "Login successful"
}
```

### 2. Update Credentials Immediately

```bash
curl -X POST http://localhost:8080/oam/setup/update-admin \
  -H "Content-Type: application/json" \
  -d '{
    "currentUsername": "admin",
    "currentPassword": "<OAM_DEFAULT_ADMIN_PASSWORD>",
    "newUsername": "youradmin",
    "newPassword": "YourSecurePassword123!",
    "newEmail": "admin@yourdomain.com",
    "confirmPassword": "YourSecurePassword123!"
  }'
```

Response:

```json
{
  "success": true,
  "message": "Admin credentials updated successfully",
  "newAccessToken": "<jwt-token>"
}
```

## Environment Variables

For non-default database configurations, set these environment variables before
running the reset:

```bash
export DB_USER=oamuser
export DB_PASSWORD=CHANGE_ME
export DB_HOST=127.0.0.1
export DB_PORT=5432
export DB_NAME=loxioam

# For SSL connections
export SSL_OPTION=true
export SSL_CA_CERT_FILE=./ssl/certs/root-ca.pem
export SSL_CA_CLIENT_CERT_FILE=./ssl/certs/client-cert.pem
export SSL_CA_CLIENT_KEY_FILE=./ssl/certs/client-key.pem

# Run the reset
./scripts/reset-admin.sh --confirm
```

## Automation Examples

### Scheduled Reset for Test Environments (cron)

```bash
# Add to crontab (runs every Sunday at 2 AM)
0 2 * * 0 /path/to/scripts/reset-admin.sh --confirm >> /var/log/admin-reset.log 2>&1
```

### Kubernetes CronJob

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: weekly-admin-reset
  namespace: oam-loxilb
spec:
  schedule: "0 2 * * 0"  # Every Sunday at 2 AM
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: reset-admin
            image: your-registry/oam-loxilb:latest
            command:
            - ./reset_admin
            - --confirm
            - --db-host=postgres-service
            - --db-port=5432
          restartPolicy: OnFailure
```

## Verification

After a reset, verify the operation:

```bash
# 1. Check logs
docker logs oam-loxilb-app | grep "Admin reset"

# Or in Kubernetes
kubectl logs -n oam-loxilb $POD_NAME | grep "Admin reset"

# 2. Verify login works
curl -X POST http://localhost:8080/oam/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "<OAM_DEFAULT_ADMIN_PASSWORD>"
  }'

# 3. Check setup status
curl http://localhost:8080/oam/setup/status
```

## Troubleshooting

### "Reset confirmation required"

Include the `--confirm` flag:

```bash
./reset_admin --confirm
```

### "Database connection failed"

```bash
# Check the database is running
docker ps | grep postgres
# Or
kubectl get pods -n oam-loxilb | grep postgres

# Check database connectivity
docker exec -it oam-loxilb-postgres psql -U oamuser -d loxioam -c "SELECT 1"
```

### Binary not found in Docker

If the `reset_admin` binary is missing, rebuild the image:

```bash
docker compose build --no-cache
```

## Security Considerations

1. **Restrict access**: reset requires host/container shell access — limit who has it.
2. **Audit logging**: monitor logs for reset operations.
3. **Change immediately**: always change the bootstrap credentials after a reset.
4. **Network security**: keep the management API behind a firewall.

## Related Documentation

- [CLI Tool Documentation](../cmd/reset_admin/README.md)
- [Main README](../README.md)
