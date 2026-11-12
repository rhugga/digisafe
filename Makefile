.DEFAULT_GOAL := help

BIN    := bin/digisafe
PREFIX ?= $(HOME)/bin

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build

.PHONY: build
build: ## Build bin/digisafe
	go build -trimpath -o $(BIN) .

.PHONY: cross
cross: ## Build for macOS, Linux and Windows into bin/
	GOOS=darwin  GOARCH=arm64 go build -trimpath -o bin/digisafe-darwin-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -trimpath -o bin/digisafe-darwin-amd64 .
	GOOS=linux   GOARCH=amd64 go build -trimpath -o bin/digisafe-linux-amd64 .
	GOOS=windows GOARCH=amd64 go build -trimpath -o bin/digisafe-windows-amd64.exe .
	GOOS=windows GOARCH=arm64 go build -trimpath -o bin/digisafe-windows-arm64.exe .

.PHONY: install
install: build ## Install to ~/bin (override with PREFIX=/some/dir)
	install -d $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/digisafe

.PHONY: clean
clean: ## Remove build output
	rm -rf bin

##@ Check

.PHONY: test
test: ## Run the tests
	go test ./...

.PHONY: race
race: ## Run the tests with the race detector
	go test -race -count=1 ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: check
check: ## Fail on unformatted code, vet problems or failing tests (run before committing)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi
	go vet ./...
	GOOS=windows go vet ./...
	GOOS=linux go vet ./...
	go test -race -count=1 ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy
