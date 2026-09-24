.PHONY: run test lint build docker-up docker-down validate verify-stack

run:
	go run ./cmd/server

test:
	go test ./...

lint:
	go vet ./...

build:
	CGO_ENABLED=0 go build -trimpath -o bin/api-manager ./cmd/server

docker-up:
	docker compose up --build

docker-down:
	docker compose down

validate:
	./scripts/validate.sh

verify-stack:
	./scripts/verify-stack.sh

.PHONY: game-discount-init game-discount-up game-discount-register game-discount-test

game-discount-init:
	python3 scripts/init-game-discount.py

game-discount-up: game-discount-init
	docker compose --env-file .env --env-file integrations/game-discount/.env -f compose.yaml -f compose.game-discount.yaml up -d --build

game-discount-register:
	python3 scripts/register-game-discount.py --publish

game-discount-test:
	python3 scripts/smoke-game-discount.py

.PHONY: production-check production-up

# Production never loads the development .env by default. Reverse proxy and
# external databases are operator-managed; no production data is touched here.
production-check:
	python3 scripts/preflight-production.py
	docker compose --env-file /dev/null -f compose.production.yaml config -q

production-up: production-check
	docker compose --env-file /dev/null -f compose.production.yaml up -d
