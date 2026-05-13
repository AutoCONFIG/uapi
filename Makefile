.PHONY: build run clean tidy test fmt dev

BINARY=cli-relay

build:
	go build -o bin/$(BINARY) ./cmd/relay/

run: build
	./bin/$(BINARY) -config config.yaml

clean:
	rm -f bin/$(BINARY)

tidy:
	go mod tidy

test:
	go test ./...

fmt:
	gofmt -w .

dev:
	docker compose -f docker-compose.dev.yaml up -d
