.PHONY: all build build-server build-client build-all build-linux build-mac build-windows run run-server run-client clean install test test-race fmt vet help

# Binaries
SERVER_NAME=link-chat-server
CLIENT_NAME=link-chat
BUILD_DIR=build
TARGET_ARCH ?= amd64
SERVER_MAIN=./cmd/link-chat-server
CLIENT_MAIN=./cmd/link-chat

GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt

# The release version is embedded from the VERSION file (see pkg/version); there
# is nothing to inject here. To release, edit VERSION and commit it.
LDFLAGS=-ldflags "-s -w"

# OS-specific commands
ifeq ($(OS),Windows_NT)
    SHELL = cmd.exe
    .SHELLFLAGS = /c
    RM_BUILD     = if exist $(BUILD_DIR) rmdir /s /q $(BUILD_DIR)
    MKDIR_BUILD  = if not exist "$(BUILD_DIR)" mkdir "$(BUILD_DIR)"
    BINARY_EXT   = .exe
    RUN_PREFIX   =
    SET_GOOS_LINUX = set "GOOS=linux"&& set "GOARCH=$(TARGET_ARCH)"&& set "CGO_ENABLED=0"&&
    SET_GOOS_MAC   = set "GOOS=darwin"&& set "GOARCH=$(TARGET_ARCH)"&& set "CGO_ENABLED=0"&&
    SET_GOOS_WIN   = set "GOOS=windows"&& set "GOARCH=$(TARGET_ARCH)"&& set "CGO_ENABLED=0"&&
else
    RM_BUILD    = rm -rf $(BUILD_DIR)
    MKDIR_BUILD = mkdir -p "$(BUILD_DIR)"
    BINARY_EXT  =
    RUN_PREFIX  = ./
    SET_GOOS_LINUX = GOOS=linux GOARCH=$(TARGET_ARCH) CGO_ENABLED=0
    SET_GOOS_MAC   = GOOS=darwin GOARCH=$(TARGET_ARCH) CGO_ENABLED=0
    SET_GOOS_WIN   = GOOS=windows GOARCH=$(TARGET_ARCH) CGO_ENABLED=0
endif

all: build

build: build-server build-client

build-server:
	@echo Building $(SERVER_NAME)...
	@$(MKDIR_BUILD)
	$(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(SERVER_NAME)$(BINARY_EXT)" $(SERVER_MAIN)

build-client:
	@echo Building $(CLIENT_NAME)...
	@$(MKDIR_BUILD)
	$(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(CLIENT_NAME)$(BINARY_EXT)" $(CLIENT_MAIN)

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
	$(SET_GOOS_LINUX) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(SERVER_NAME)-linux-$(TARGET_ARCH)" $(SERVER_MAIN)
	$(SET_GOOS_LINUX) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(CLIENT_NAME)-linux-$(TARGET_ARCH)" $(CLIENT_MAIN)

build-mac:
	@$(MKDIR_BUILD)
	$(SET_GOOS_MAC) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(SERVER_NAME)-darwin-$(TARGET_ARCH)" $(SERVER_MAIN)
	$(SET_GOOS_MAC) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(CLIENT_NAME)-darwin-$(TARGET_ARCH)" $(CLIENT_MAIN)

build-windows:
	@$(MKDIR_BUILD)
	$(SET_GOOS_WIN) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(SERVER_NAME)-windows-$(TARGET_ARCH).exe" $(SERVER_MAIN)
	$(SET_GOOS_WIN) $(GOBUILD) $(LDFLAGS) -o "$(BUILD_DIR)/$(CLIENT_NAME)-windows-$(TARGET_ARCH).exe" $(CLIENT_MAIN)

build-all: build build-linux build-mac build-windows
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
