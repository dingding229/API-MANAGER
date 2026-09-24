#!/usr/bin/env bash
set -euo pipefail

go test -race ./...
go vet ./...
go build ./cmd/server
python3 -m json.tool configs/grafana/dashboards/api-manager.json >/dev/null
docker compose config >/dev/null
echo 'Local validation passed.'
