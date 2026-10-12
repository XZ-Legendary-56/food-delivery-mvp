.PHONY: up down logs build test test-integration lint check migrate-up migrate-down migrate-status seed generate docs

# Host-side defaults for talking to the Compose postgres from outside Docker
# (make migrate-up, make seed). 5434 because 5432/5433 are commonly already
# taken by native Postgres installs; see docker-compose.yml. Override any of
# these on the command line, e.g. `make migrate-up DB_PORT=5432`.
DB_HOST ?= localhost
DB_PORT ?= 5434
DB_USER ?= food_delivery
DB_PASSWORD ?= food_delivery
DB_NAME ?= food_delivery
DB_SSLMODE ?= disable
db_env = DB_HOST=$(DB_HOST) DB_PORT=$(DB_PORT) DB_USER=$(DB_USER) DB_PASSWORD=$(DB_PASSWORD) DB_NAME=$(DB_NAME) DB_SSLMODE=$(DB_SSLMODE)

up:
	docker compose up --build

down:
	docker compose down -v

logs:
	docker compose logs -f

build:
	go build ./...
	go build venue-pasta-roma/...

test:
	go test ./...
	go test venue-pasta-roma/...

# Needs Docker: spins up a real Postgres per test via testcontainers-go.
# Gated behind the "integration" build tag so plain `make test` (and plain
# `go test ./...`) never does this implicitly.
test-integration:
	go test -tags=integration ./test/integration/... -v

migrate-up:
	$(db_env) go run ./cmd/migrate up

migrate-down:
	$(db_env) go run ./cmd/migrate down

migrate-status:
	$(db_env) go run ./cmd/migrate status

seed:
	$(db_env) go run ./cmd/seed

# Regenerates the HTTP server code from api/openapi/*.yaml (oapi-codegen,
# strict-server mode) and the usecase-layer test mocks (go.uber.org/mock).
# Run this every time you edit an .yaml spec or a ports.go file — both
# internal/generated and every mock_test.go are machine-written and get
# overwritten, never hand-edited.
generate:
	go tool oapi-codegen -config api/openapi/publicapi.cfg.yaml api/openapi/public.yaml
	go tool oapi-codegen -config api/openapi/partnerapi.cfg.yaml api/openapi/partner.yaml
	go tool oapi-codegen -config api/openapi/partnerclient.cfg.yaml api/openapi/partner.yaml
	go tool mockgen -source=internal/usecase/catalog/ports.go -destination=internal/usecase/catalog/mock_test.go -package=catalog_test
	go tool mockgen -source=internal/usecase/order/ports.go -destination=internal/usecase/order/mock_test.go -package=order_test
	go tool mockgen -source=internal/usecase/txmanager.go -destination=internal/usecase/order/mock_txmanager_test.go -package=order_test
	go tool mockgen -source=internal/usecase/partner/ports.go -destination=internal/usecase/partner/mock_test.go -package=partner_test
	go tool mockgen -source=internal/usecase/txmanager.go -destination=internal/usecase/partner/mock_txmanager_test.go -package=partner_test
	go tool mockgen -source=internal/usecase/outbox/ports.go -destination=internal/usecase/outbox/mock_test.go -package=outbox_test
	go tool mockgen -source=internal/adapter/webhook/publisher.go -destination=internal/adapter/webhook/mock_test.go -package=webhook_test

# Full linter set from docs/PLAN.md 10.1, run separately per module since each
# has its own .golangci.yml (the two are independent Go modules joined only
# by go.work, and golangci-lint lints one module tree at a time).
lint:
	golangci-lint run --build-tags=integration ./...
	cd external/venue-pasta-roma && golangci-lint run --build-tags=integration ./...

# Renders every .puml under docs/ (c4, cjm, erd) to PNG in docs/img/, via
# the official plantuml/plantuml image — no local Java/PlantUML install
# needed. -tpng picks PNG explicitly. -o must be an ABSOLUTE container path
# (/docs/img): a relative -o is resolved per source file's own directory,
# which would scatter output into docs/c4/img and docs/cjm/img instead of
# one shared docs/img.
docs:
	docker run --rm -v "$(CURDIR)/docs:/docs" -w /docs plantuml/plantuml -tpng -o /docs/img c4 cjm erd.puml

# docs/PLAN.md 11: lint + test + a check that generated code (oapi-codegen,
# mockgen) is not stale. Regenerates first so a forgotten `make generate`
# after editing an .yaml spec or a ports.go file fails loudly here instead
# of silently shipping outdated generated code.
check: generate
	@diffout="$$(git status --porcelain)"; \
	if [ -n "$$diffout" ]; then \
		echo "generated code is stale — run 'make generate' and commit the result:"; \
		echo "$$diffout"; exit 1; \
	fi
	$(MAKE) lint
	$(MAKE) test
