.PHONY: up down test fmt vet tidy logs migrate-up migrate-down sqlc k6 stress stress-quantity stress-seats stress-reset stress-seed assert

POSTGRES_DB ?= ticket_reservation_go
POSTGRES_USER ?= ticket_reservation_go
POSTGRES_PASSWORD ?= ticket_reservation_go
MIGRATE_DATABASE_URL := postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(POSTGRES_DB)?sslmode=disable
K6_BASE_URL ?= http://api:8080
K6_VUS ?= 100
K6_DURATION ?= 30s

up:
	docker compose up --build

down:
	docker compose down

test:
	docker compose exec api go test ./...

fmt:
	docker compose exec api go fmt ./...

vet:
	docker compose exec api go vet ./...

tidy:
	docker compose exec api go mod tidy

logs:
	docker compose logs -f api worker

migrate-up:
	docker compose run --rm migrate

migrate-down:
	docker compose run --rm migrate -path /migrations -database "$(MIGRATE_DATABASE_URL)" down 1

sqlc:
	docker compose run --rm sqlc generate

stress-reset:
	curl -X POST http://localhost:8080/v1/admin/stress/reset

stress-seed:
	curl -X POST http://localhost:8080/v1/admin/stress/seed

stress:
	docker compose run --rm k6 run -e BASE_URL=$(K6_BASE_URL) -e VUS=$(K6_VUS) -e DURATION=$(K6_DURATION) /scripts/mixed.js

k6: stress

stress-quantity:
	docker compose run --rm k6 run -e BASE_URL=$(K6_BASE_URL) -e VUS=$(K6_VUS) -e DURATION=$(K6_DURATION) /scripts/quantity.js

stress-seats:
	docker compose run --rm k6 run -e BASE_URL=$(K6_BASE_URL) -e VUS=$(K6_VUS) -e DURATION=$(K6_DURATION) /scripts/seats.js

assert:
	curl http://localhost:8080/v1/admin/stress/assert-consistency
