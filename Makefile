.PHONY: test build run

test:
	go test ./...

build:
	go build ./cmd/llmproxy

run:
	go run ./cmd/llmproxy
