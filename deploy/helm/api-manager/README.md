# API Manager Helm Chart

The chart deploys the API Manager application. PostgreSQL, Redis, Tempo,
Prometheus and Grafana are intentionally external dependencies; configure their
endpoints in `values.yaml` or a private values file.

```bash
helm lint deploy/helm/api-manager -f values-production.yaml
helm template api-manager deploy/helm/api-manager -f values-production.yaml
helm upgrade --install api-manager deploy/helm/api-manager -n api-manager --create-namespace -f values-production.yaml
```

## Production hardening

Set `productionMode: true`, `env.REDIS_TLS_ENABLED: "true"`, `image.digest` to the full 64-hex `sha256:...`, `persistence.enabled: true`, and `existingSecret` to the name of a pre-created Secret with `ADMIN_TOKEN`, `USER_JWT_SECRET`, `CREDENTIAL_ENCRYPTION_KEY`, `POSTGRES_DSN`, `REDIS_PASSWORD`, and `METRICS_TOKEN`. Use PostgreSQL `sslmode=verify-full`. Do **not** put real secrets in Helm values or command arguments: release metadata retains them. Invalid production configurations fail chart rendering. `productionMode` disables admin Token access to `/admin/*` after first bootstrap, requires persistent DB/Redis and metrics authentication, and removes the default broad same-namespace egress allowance. Specify **only** approved database, Redis and telemetry destinations under `networkPolicy.extraEgress`. Production ingress defaults to **deny all**; explicitly allow the operator-managed reverse proxy and Prometheus using `networkPolicy.allowedIngress` (source namespace/pod selectors with TCP port 8080), or the API will not be reachable. The reverse proxy itself is not part of this chart. If tracing is enabled in production, set `env.OTEL_EXPORTER_OTLP_INSECURE: "false"` and use a TLS OTLP endpoint. For a private CA, set `trustedCA.secretName` to a Secret with CA PEMs, set `env.REDIS_TLS_CA_FILE=/etc/api-manager/ca/redis-ca.pem`, and include `sslrootcert=/etc/api-manager/ca/postgres-ca.pem` in the secret PostgreSQL DSN. Do not place private keys in this CA Secret.

When `serviceMonitor.enabled` and `productionMode` are both true, the ServiceMonitor refers to the `METRICS_TOKEN` Secret key. Grant the Prometheus instance permission to read that Secret in its namespace. Confirm TLS and probes in staging before routing production traffic. For operational details and backup requirements, read `docs/生产部署技术基线.md`.
