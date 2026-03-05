.PHONY: build build-bot build-parser build-userbot build-api lint test clean docker-build

SERVICES := bot parser userbot api
GOFLAGS := -ldflags="-s -w"

build: $(addprefix build-,$(SERVICES))
	@echo "✓ All services built"

build-%:
	@echo "Building $*..."
	CGO_ENABLED=0 GOOS=linux go build $(GOFLAGS) -o ./bin/$* ./cmd/$*

lint:
	@echo "Running golangci-lint..."
	golangci-lint run ./...

test:
	@echo "Running tests..."
	go test -v ./...

docker-build:
	@echo "Building Docker images..."
	@for service in $(SERVICES); do \
		echo "Building $$service..."; \
		docker build --build-arg SERVICE=$$service -t rss-reader:$$service .; \
	done

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

dev: build docker-down docker-up
	@echo "✓ Development environment ready"

clean:
	rm -rf ./bin
	docker compose down -v
	@echo "✓ Cleaned up"

help:
	@echo "RSS Reader - Development Commands"
	@echo ""
	@echo "make build              - Build all services"
	@echo "make build-bot          - Build bot service"
	@echo "make build-parser       - Build parser service"
	@echo "make build-userbot      - Build userbot service"
	@echo "make build-api          - Build api service"
	@echo "make lint               - Run linter"
	@echo "make test               - Run tests"
	@echo "make docker-build       - Build all Docker images"
	@echo "make docker-up          - Start docker compose"
	@echo "make docker-down        - Stop docker compose"
	@echo "make docker-logs        - Follow docker logs"
	@echo "make dev                - Build and start everything"
	@echo "make clean              - Remove binaries and containers"
