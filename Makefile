# Common tasks. Everything runs in Docker, so only Docker is required.
.PHONY: env network up down logs seed sample-csv test test-unit test-integration lint check-openapi

env: ## create .env with fresh random secrets (won't overwrite)
	@test -f .env || ( cp .env.example .env && \
	  sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=$$(openssl rand -base64 48 | tr -d '\n')|; \
	    s|^SESSION_SECRET=.*|SESSION_SECRET=$$(openssl rand -base64 48 | tr -d '\n')|; \
	    s|^WEBHOOK_SECRET=.*|WEBHOOK_SECRET=$$(openssl rand -hex 24)|; \
	    s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=$$(openssl rand -base64 32 | tr -d '\n')|; \
	    s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$$(openssl rand -hex 16)|" .env && rm -f .env.bak && \
	  echo "wrote .env" )

network: ## the Docker network shared with company-scraper
	@docker network inspect company-data >/dev/null 2>&1 || docker network create company-data >/dev/null

up: env network ## build and start the whole stack
	docker compose up -d --build

down:
	docker compose down

logs:
	docker compose logs -f backend worker

seed: ## demo user, organization, mailbox, 200 leads, draft campaign
	docker compose exec backend /app/seed

sample-csv: ## write sample_leads.csv with 10,000 synthetic leads
	docker compose exec backend /app/seed -csv /tmp/sample_leads.csv -n 10000
	docker compose cp backend:/tmp/sample_leads.csv ./sample_leads.csv

GO = docker run --rm -v $(PWD)/backend:/src -v leadline-gomod:/go/pkg/mod -w /src golang:1.24-alpine

test: test-unit test-integration

test-unit:
	$(GO) go test ./...

test-integration: ## runs against throwaway Postgres + Valkey containers
	-docker network create leadline-it >/dev/null 2>&1
	docker run -d --rm --name leadline-it-pg --network leadline-it -e POSTGRES_PASSWORD=test -e POSTGRES_USER=test -e POSTGRES_DB=test postgres:17-alpine >/dev/null
	docker run -d --rm --name leadline-it-redis --network leadline-it valkey/valkey:8-alpine >/dev/null
	sleep 4
	docker run --rm --network leadline-it -v $(PWD)/backend:/src -v leadline-gomod:/go/pkg/mod -w /src \
	  -e TEST_DATABASE_URL="postgres://test:test@leadline-it-pg:5432/test?sslmode=disable" \
	  -e TEST_REDIS_URL="redis://leadline-it-redis:6379/0" golang:1.24-alpine go test -count=1 ./tests/... ; \
	  status=$$?; docker rm -f leadline-it-pg leadline-it-redis >/dev/null; exit $$status

check-openapi: ## every registered route is documented in docs/openapi.yaml
	python3 docs/check_openapi.py

lint: check-openapi
	$(GO) go vet ./...
	cd frontend && npx tsc --noEmit && npx eslint .
