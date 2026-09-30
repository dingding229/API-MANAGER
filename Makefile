IMAGE ?= api-manager:local

.PHONY: docker-build init check up down update stack-up stack-down production-check production-up production-down production-update

docker-build:
	docker build -t $(IMAGE) .

init:
	python3 scripts/init-production-secrets.py

check:
	python3 scripts/preflight-production.py
	docker compose config -q

up: check
	docker compose up -d

down:
	docker compose down

update: check
	docker compose pull api-manager
	docker compose up -d --force-recreate api-manager

stack-up production-up: up
stack-down production-down: down
production-check: check
production-update: update
