.PHONY: dev-infra test lint build

dev-infra:
	docker compose up -d mysql redis rabbitmq

test:
	cd backend && go test ./...
	cd frontend && npm run test

lint:
	cd backend && go vet ./...
	cd frontend && npm run check

build:
	cd backend && go build ./cmd/api ./cmd/worker
	cd frontend && npm run build

