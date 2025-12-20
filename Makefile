.PHONY: build clean build-arm build-amd64 build-host dist fmt deps lint test

BINARY_NAME=settings-service
BUILD_DIR=bin
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-w -s -X main.version=$(VERSION) -extldflags '-static'"

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/settings-service

clean:
	rm -rf $(BUILD_DIR)

build-arm:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/settings-service

build-amd64:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-amd64 ./cmd/settings-service

build-host:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-host ./cmd/settings-service

dist:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/settings-service

fmt:
	go fmt ./...

deps:
	go mod download && go mod tidy

lint:
	golangci-lint run

test:
	go test -v ./...