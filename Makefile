.PHONY: build test integration simulate vet fmt demo up down
build:
	go build -trimpath -o bin/dq ./cmd/dq
test:
	go test -race ./...
integration:
	@test -n "$$TEST_DATABASE_URL" || (echo 'Set TEST_DATABASE_URL to a PostgreSQL database URL'; exit 1)
	go test -race -count=1 -v ./queue/postgres
simulate:
	go run ./cmd/dq simulate -seed 42 -seeds 100 -steps 1000 -jobs 30
vet:
	go vet ./...
fmt:
	gofmt -w cmd internal queue simulation
demo:
	./scripts/demo.sh
up:
	docker compose up --build -d
down:
	docker compose down
