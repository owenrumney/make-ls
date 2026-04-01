# ============================================================================
# Multi-service Go project with Docker, protobuf, and multi-environment deploy
# ============================================================================

.PHONY: all build test lint clean proto docker docker-push db-migrate db-rollback db-seed db-reset deploy release watch debug help

# --- Configuration -----------------------------------------------------------

PROJECT     := myplatform
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_TIME  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

GO          := go
GOFLAGS     := -trimpath
CGO_ENABLED := 0

DOCKER_REGISTRY := ghcr.io/myorg
DOCKER_TAG      := $(VERSION)

PROTO_DIR   := api/proto
PROTO_OUT   := internal/gen

SERVICES    := gateway worker scheduler
TOOLS       := migrate seed healthcheck

# --- Environment -------------------------------------------------------------

ENV ?= staging

ifeq ($(ENV),production)
REPLICAS     := 3
LOG_LEVEL    := warn
DEPLOY_FLAGS := --timeout 300s --rollback
else ifeq ($(ENV),staging)
REPLICAS     := 1
LOG_LEVEL    := debug
DEPLOY_FLAGS := --timeout 120s
else
REPLICAS     := 1
LOG_LEVEL    := debug
DEPLOY_FLAGS :=
endif

ifdef VERBOSE
GOFLAGS += -v
endif

# --- Derived -----------------------------------------------------------------

SERVICE_BINS := $(addprefix bin/,$(SERVICES))
TOOL_BINS    := $(addprefix bin/,$(TOOLS))
ALL_BINS     := $(SERVICE_BINS) $(TOOL_BINS)
PROTO_FILES  := $(wildcard $(PROTO_DIR)/**/*.proto $(PROTO_DIR)/*.proto)
PROTO_GEN    := $(patsubst $(PROTO_DIR)/%.proto,$(PROTO_OUT)/%.pb.go,$(PROTO_FILES))
SRC_FILES    := $(shell find . -name '*.go' -not -path './vendor/*')

# --- Targets -----------------------------------------------------------------

# Build all binaries
all: proto lint test build

# Build all service and tool binaries
build: $(ALL_BINS)

bin/%: cmd/%/main.go $(SRC_FILES) $(PROTO_GEN) | bin
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $@ ./cmd/$*

bin:
	mkdir -p bin

# --- Protobuf ----------------------------------------------------------------

proto: $(PROTO_GEN)

$(PROTO_OUT)/%.pb.go: $(PROTO_DIR)/%.proto | $(PROTO_OUT)
	protoc \
		--go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
		-I $(PROTO_DIR) $<

$(PROTO_OUT):
	mkdir -p $(PROTO_OUT)

# --- Quality -----------------------------------------------------------------

test:
	$(GO) test $(GOFLAGS) -race -coverprofile=coverage.out ./...
	@echo "Coverage:"
	@$(GO) tool cover -func=coverage.out | tail -1

test-integration:
	$(GO) test $(GOFLAGS) -tags=integration -race -count=1 ./...

lint:
	golangci-lint run ./...

# Benchmark a specific package (usage: make bench PKG=./internal/cache)
PKG ?= ./...
bench:
	$(GO) test -bench=. -benchmem -run='^$$' $(PKG)

# --- Docker ------------------------------------------------------------------

docker: $(addprefix docker-,$(SERVICES))

docker-%: bin/%
	docker build \
		--build-arg BINARY=$* \
		--build-arg VERSION=$(VERSION) \
		-t $(DOCKER_REGISTRY)/$(PROJECT)-$*:$(DOCKER_TAG) \
		-f deploy/Dockerfile .

docker-push: docker
	@$(foreach svc,$(SERVICES),\
		echo "Pushing $(svc)..." && \
		docker push $(DOCKER_REGISTRY)/$(PROJECT)-$(svc):$(DOCKER_TAG) && \
	) true

# --- Database ----------------------------------------------------------------

DB_URL ?= postgres://localhost:5432/$(PROJECT)_$(ENV)?sslmode=disable

db-migrate: bin/migrate
	./bin/migrate -url "$(DB_URL)" -path db/migrations up

db-rollback: bin/migrate
	./bin/migrate -url "$(DB_URL)" -path db/migrations down 1

db-seed: bin/seed
	./bin/seed -url "$(DB_URL)" -path db/seeds

db-reset: db-rollback db-migrate db-seed

# --- Deploy ------------------------------------------------------------------

deploy: docker-push
	@echo "Deploying to $(ENV) with $(REPLICAS) replicas..."
	@$(foreach svc,$(SERVICES),\
		echo "  $(svc) → $(DOCKER_REGISTRY)/$(PROJECT)-$(svc):$(DOCKER_TAG)" && \
		kubectl set image deployment/$(svc) \
			$(svc)=$(DOCKER_REGISTRY)/$(PROJECT)-$(svc):$(DOCKER_TAG) \
			-n $(PROJECT)-$(ENV) $(DEPLOY_FLAGS) && \
		kubectl scale deployment/$(svc) --replicas=$(REPLICAS) -n $(PROJECT)-$(ENV) && \
	) true
	@echo "Deploy complete."

# --- Codegen -----------------------------------------------------------------

generate:
	$(GO) generate ./...

mocks:
	mockgen -source=internal/service/interface.go -destination=internal/service/mock/mock.go
	mockgen -source=internal/repo/interface.go -destination=internal/repo/mock/mock.go

# --- Release -----------------------------------------------------------------

RELEASE_TARGETS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

release: test
	@$(foreach target,$(RELEASE_TARGETS),\
		$(eval OS   := $(word 1,$(subst /, ,$(target)))) \
		$(eval ARCH := $(word 2,$(subst /, ,$(target)))) \
		$(foreach svc,$(SERVICES),\
			echo "Building $(svc) $(OS)/$(ARCH)..." && \
			GOOS=$(OS) GOARCH=$(ARCH) CGO_ENABLED=0 \
				$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
				-o dist/$(svc)-$(OS)-$(ARCH) ./cmd/$(svc) && \
		) \
	) true
	@echo "Release binaries in dist/"

# --- Utilities ---------------------------------------------------------------

clean:
	rm -rf bin/ dist/ coverage.out
	rm -rf $(PROTO_OUT)

# Check binary sizes
sizes: build
	@echo "Binary sizes:"
	@du -sh bin/*

# Run a specific service locally (usage: make run SVC=gateway)
SVC ?= gateway
run: bin/$(SVC)
	LOG_LEVEL=$(LOG_LEVEL) ./bin/$(SVC)

# Watch for changes and rebuild (requires entr)
watch:
	find . -name '*.go' | entr -r make run SVC=$(SVC)

# Print all variables for debugging
debug:
	@echo "PROJECT     = $(PROJECT)"
	@echo "VERSION     = $(VERSION)"
	@echo "COMMIT      = $(COMMIT)"
	@echo "ENV         = $(ENV)"
	@echo "REPLICAS    = $(REPLICAS)"
	@echo "LOG_LEVEL   = $(LOG_LEVEL)"
	@echo "SERVICES    = $(SERVICES)"
	@echo "TOOLS       = $(TOOLS)"
	@echo "PROTO_FILES = $(PROTO_FILES)"
	@echo "LDFLAGS     = $(LDFLAGS)"

# Help target — extracts comments above each target
help:
	@echo "Usage: make [target] [ENV=staging|production] [VERBOSE=1]"
	@echo ""
	@echo "Targets:"
	@grep -E '^# .+$$' $(MAKEFILE_LIST) | grep -B1 -E '^[a-zA-Z_-]+:' | \
		awk '/^#/{desc=$$0} /^[a-zA-Z_-]+:/{printf "  %-20s %s\n", $$1, desc; desc=""}'
