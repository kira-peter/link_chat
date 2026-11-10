.PHONY: build build-server build-client run run-server run-client clean install test test-race fmt vet help

# Binaries
SERVER_NAME=link-chat-server
CLIENT_NAME=link-chat
BUILD_DIR=build
SERVER_MAIN=./cmd/link-chat-server
CLIENT_MAIN=./cmd/link-chat

GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# Build metadata, injected so the binary can report what it is.
#   make build VERSION=1.2.0
# Without VERSION the binary reports "dev" (see pkg/app/version.go). The target of
# -X must be an existing string variable, or the linker silently ignores it.
VERSION ?=
VERSION_PKG=link-chat/pkg/app
LDFLAGS=-ldflags "-s -w $(if $(VERSION),-X $(VERSION_PKG).Version=$(VERSION),)"

# OS-specific commands
ifeq ($(OS),Windows_NT)
    SHELL = cmd.exe
    .SHELLFLAGS = /c
    RM_BUILD     = if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
    MKDIR_BUILD  = if not exist $(BUILD_DIR) mkdir $(BUILD_DIR)
    BINARY_EXT   = .exe
    RUN_PREFIX   =
    SET_GOOS     = set GOOS=linux&&
    SET_GOOS_MAC = set GOOS=darwin&&
else
    RM_BUILD    = rm -rf $(BUILD_DIR)
    MKDIR_BUILD = mkdir -p $(BUILD_DIR)
    BINARY_EXT  =
    RUN_PREFIX  = ./
    SET_GOOS    = GOOS=linux
    SET_GOOS_MAC= GOOS=darwin
endif

all: build

build: build-server build-client

build-server:
	@echo Building $(SERVER_NAME)...
	@$(MKDIR_BUILD)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(SERVER_NAME)$(BINARY_EXT) $(SERVER_MAIN)

build-client:
	@echo Building $(CLIENT_NAME)...
	@$(MKDIR_BUILD)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(CLIENT_NAME)$(BINARY_EXT) $(CLIENT_MAIN)

run-server: build-server
	@echo Running $(SERVER_NAME)...
	$(RUN_PREFIX)$(BUILD_DIR)/$(SERVER_NAME)$(BINARY_EXT)

run-client: build-client
	@echo Running $(CLIENT_NAME)...
	$(RUN_PREFIX)$(BUILD_DIR)/$(CLIENT_NAME)$(BINARY_EXT)

run: build-server
	@echo Running $(SERVER_NAME)...
	$(RUN_PREFIX)$(BUILD_DIR)/$(SERVER_NAME)$(BINARY_EXT)

install:
	@echo "Installing dependencies..."
	@$(GOMOD) download
	@$(GOMOD) tidy

test:
	@$(GOTEST) ./...

test-race:
	@$(GOTEST) -race ./...

fmt:
	@$(GOFMT) ./...

vet:
	@$(GOCMD) vet ./...

clean:
	@echo Cleaning...
	@$(GOCLEAN)
	@$(RM_BUILD)

build-linux:
	@$(MKDIR_BUILD)
	$(SET_GOOS) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(SERVER_NAME)-linux $(SERVER_MAIN)
	$(SET_GOOS) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(CLIENT_NAME)-linux $(CLIENT_MAIN)

build-mac:
	@$(MKDIR_BUILD)
	$(SET_GOOS_MAC) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(SERVER_NAME)-mac $(SERVER_MAIN)
	$(SET_GOOS_MAC) $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(CLIENT_NAME)-mac $(CLIENT_MAIN)

build-all: build build-linux build-mac
	@echo All platform builds complete

help:
	@echo "Available targets:"
	@echo "  make build         - Build server and client"
	@echo "  make run-server    - Build and run the server"
	@echo "  make run-client    - Build and run the client"
	@echo "  make test          - Run all tests"
	@echo "  make test-race     - Run tests with the race detector"
	@echo "  make vet           - Run go vet"
	@echo "  make fmt           - Format the tree"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make install       - Download and tidy dependencies"
	@echo "  make build-all     - Cross-compile for Windows, Linux and macOS"
