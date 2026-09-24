#!/usr/bin/env bash
# Smoke-test a running Docker Compose deployment. Run after `docker compose up -d --build`.
set -euo pipefail

base_url="${API_MANAGER_URL:-http://localhost:8080}"
prometheus_url="${PROMETHEUS_URL:-http://localhost:9090}"
grafana_url="${GRAFANA_URL:-http://localhost:3000}"
tempo_url="${TEMPO_URL:-http://localhost:3200}"

check() {
  local name="$1" url="$2"
  printf 'Checking %-18s %s ... ' "$name" "$url"
  curl --fail --silent --show-error --max-time 10 "$url" >/dev/null
  echo ok
}

check "API liveness" "$base_url/health/live"
check "API readiness" "$base_url/health/ready"
check "Prometheus metrics" "$base_url/metrics"
check "Prometheus" "$prometheus_url/-/ready"
check "Grafana" "$grafana_url/api/health"
check "Tempo" "$tempo_url/ready"

if ! curl --fail --silent --show-error --max-time 10 "$base_url/metrics" | grep -q '^api_manager_up 1$'; then
  echo "API Manager Prometheus metric api_manager_up is unavailable" >&2
  exit 1
fi

if ! curl --fail --silent --show-error --max-time 10 "$prometheus_url/api/v1/rules" | grep -q 'APIManagerDown'; then
  echo "Prometheus did not load API Manager alert rules" >&2
  exit 1
fi

echo "Stack verification passed."
