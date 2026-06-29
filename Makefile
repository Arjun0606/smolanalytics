.PHONY: build test race run demo mcp docker fmt vet tidy clean

build: ## build the binary to ./bin/smolanalytics
	go build -trimpath -o bin/smolanalytics ./cmd/smolanalytics

test: ## run the test suite
	go test ./...

race: ## run tests with the race detector
	go test -race ./...

run: ## run the server (empty)
	go run ./cmd/smolanalytics serve

demo: ## seed demo data + open a populated dashboard on :8080
	go run ./cmd/smolanalytics demo

mcp: ## run the MCP server over stdio (demo data)
	go run ./cmd/smolanalytics mcp

docker: ## build the docker image
	docker build -t smolanalytics .

fmt: ## format the code
	gofmt -w ./internal ./cmd

vet: ## go vet
	go vet ./...

tidy: ## tidy modules
	go mod tidy

clean:
	rm -rf bin
