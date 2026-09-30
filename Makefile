IMAGE ?= api-manager:local

.PHONY: docker-build stack-up stack-down production-check production-up production-down production-update

docker-build:
	docker build -t $(IMAGE) .

stack-up:
	docker compose up -d

stack-down:
	docker compose down

# Production uses the Docker Hub latest tag with pull_policy always, external PostgreSQL/Redis,
# file-backed secrets, and an operator-managed reverse proxy.
production-check:
	python3 scripts/preflight-production.py
	docker compose --env-file /dev/null -f compose.production.yaml config -q

production-up: production-check
	docker compose --env-file /dev/null -f compose.production.yaml up -d --force-recreate api-manager

production-update: production-check
	docker compose --env-file /dev/null -f compose.production.yaml pull api-manager
	docker compose --env-file /dev/null -f compose.production.yaml up -d --force-recreate api-manager

production-down:
	docker compose --env-file /dev/null -f compose.production.yaml down
