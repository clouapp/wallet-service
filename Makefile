.PHONY: help build clean run dev stop deploy deploy-guided delete validate local test test-coverage test-race test-verbose lint fmt vet security migrate migrate-rollback migrate-status migrate-fresh db-reset db-seed docker-up docker-down docker-logs docker-build docker-test docker-status ecr-login ecr-push logs-api logs-scanner logs-webhook logs-withdrawal dlq-check dlq-replay-webhooks dlq-replay-withdrawals ping env-info swagger-install swagger-generate swagger-fmt deps-install deps-update

# =============================================================================
# Configuration
# =============================================================================

# Default environment
ENV ?= dev

# Load environment-specific .env file
ifeq ($(ENV),prod)
  -include .env.prod
  STACK_NAME = vault-prod
else ifeq ($(ENV),staging)
  -include .env.staging
  STACK_NAME = vault-staging
else
  -include .env.dev
  STACK_NAME = vault-dev
endif

# Docker configuration
DOCKER_COMPOSE = docker-compose
DOCKER_IMAGE_NAME = vault-service
DOCKER_TAG ?= latest

# AWS configuration
AWS_REGION ?= us-east-1
AWS_ACCOUNT_ID := $(shell aws sts get-caller-identity --query Account --output text 2>/dev/null)
ECR_REGISTRY := $(AWS_ACCOUNT_ID).dkr.ecr.$(AWS_REGION).amazonaws.com
ECR_IMAGE := $(ECR_REGISTRY)/$(DOCKER_IMAGE_NAME):$(DOCKER_TAG)

# SAM configuration
S3_FLAG := $(if $(S3_BUCKET),--s3-bucket $(S3_BUCKET),--resolve-s3)

# Parameter overrides for SAM deployment
PARAMETER_OVERRIDES = \
	Environment=$(ENVIRONMENT) \
	DatabaseURL=$(DATABASE_URL) \
	RedisURL=$(REDIS_URL) \
	EthRpcURL=$(ETH_RPC_URL) \
	PolygonRpcURL=$(POLYGON_RPC_URL) \
	SolanaRpcURL=$(SOLANA_RPC_URL) \
	BtcRpcURL=$(BTC_RPC_URL) \
	ApiKeySecret=$(API_KEY_SECRET) \
	MasterKeyRef=$(MASTER_KEY_REF)

# =============================================================================
# Docker health check — auto-starts postgres + redis when needed
# =============================================================================

define ensure_docker
	@if ! docker ps --format '{{.Names}}' | grep -q vault-postgres; then \
		echo "⚠️  PostgreSQL not running — starting Docker services..."; \
		$(DOCKER_COMPOSE) up -d postgres redis; \
		echo "⏳ Waiting for PostgreSQL to accept connections..."; \
		for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
			docker exec vault-postgres pg_isready -U vault -d vault >/dev/null 2>&1 && break; \
			sleep 1; \
		done; \
		if ! docker exec vault-postgres pg_isready -U vault -d vault >/dev/null 2>&1; then \
			echo "❌ PostgreSQL failed to start within 15 s"; \
			exit 1; \
		fi; \
		echo "✅ PostgreSQL ready"; \
	fi
endef

# =============================================================================
# Help
# =============================================================================

help: ## Show this help message
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║                  Vault Service - Makefile                   ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "📦 Build Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^build/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🚀 Deployment Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^deploy|^delete|^validate/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🧪 Testing Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^test/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🐳 Docker Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^docker/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "💾 Database Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^migrate|^db-/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "📊 Monitoring Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^logs|^dlq|^ping/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🔧 Utility Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^lint|^fmt|^vet|^security|^env-info|^clean/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "🖥️  Local Dev Commands:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(firstword $(MAKEFILE_LIST)) | awk 'BEGIN {FS = ":.*?## "}; /^run|^dev|^stop|^air-install/ {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "💡 Examples:"
	@echo "  make dev                    # Start API with live reload (Air)"
	@echo "  make run                    # Start API without live reload"
	@echo "  make docker-up              # Start local development environment"
	@echo "  make test-coverage          # Run tests with coverage report"
	@echo "  make deploy ENV=prod        # Deploy to production"
	@echo "  make logs-api               # Tail API Lambda logs"
	@echo ""

# =============================================================================
# Build Commands
# =============================================================================

build: ## Build Lambda function for deployment
	@echo "🔨 Building Lambda function..."
	sam build
	@echo "✅ Build complete"

build-lambda: ## Build Lambda binary for arm64
	@echo "🔨 Building Lambda binary for arm64..."
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bootstrap main.go
	zip function.zip bootstrap
	@echo "✅ Lambda binary built: function.zip"

clean: ## Clean build artifacts
	@echo "🧹 Cleaning build artifacts..."
	rm -rf .aws-sam/ tmp/
	rm -f bootstrap function.zip
	go clean -testcache
	@echo "✅ Clean complete"

# =============================================================================
# Deployment Commands
# =============================================================================

deploy: build ## Deploy to AWS using SAM (uses ENV variable)
	@echo "🚀 Deploying to $(ENV) environment..."
	@echo "Stack: $(STACK_NAME)"
	sam deploy \
		--stack-name $(STACK_NAME) \
		--parameter-overrides "$(PARAMETER_OVERRIDES)" \
		--capabilities CAPABILITY_IAM \
		$(S3_FLAG) \
		--no-confirm-changeset \
		--no-fail-on-empty-changeset
	@echo "✅ Deployment complete"

deploy-guided: build ## Deploy with guided prompts (first time setup)
	@echo "🚀 Running guided deployment..."
	sam deploy --guided \
		--stack-name $(STACK_NAME) \
		--parameter-overrides "$(PARAMETER_OVERRIDES)" \
		--capabilities CAPABILITY_IAM \
		$(S3_FLAG)

delete: ## Delete CloudFormation stack
	@echo "⚠️  Deleting stack: $(STACK_NAME)"
	sam delete --stack-name $(STACK_NAME) --no-prompts
	@echo "✅ Stack deleted"

validate: ## Validate SAM template
	@echo "✅ Validating SAM template..."
	sam validate

# =============================================================================
# Local Development Commands
# =============================================================================

run: ## Run API server locally (go run)
	$(call ensure_docker)
	@echo "🚀 Starting local API server..."
	@export $$(grep -v '^#' .env.dev | xargs) && APP_KEY=$$(openssl rand -hex 16) go run .

dev: ## Run API server with live reload (requires: go install github.com/air-verse/air@latest)
	$(call ensure_docker)
	@echo "🔥 Starting API server with live reload (Air)..."
	@if [ ! -f "$$(go env GOPATH)/bin/air" ]; then \
		echo "❌ Air not found. Install with: go install github.com/air-verse/air@latest"; \
		exit 1; \
	fi
	@export $$(grep -v '^#' .env.dev | xargs) && APP_KEY=$$(openssl rand -hex 16) $$(go env GOPATH)/bin/air

stop: ## Stop running API server (go run or Air)
	@echo "🛑 Stopping local API server..."
	@pkill -f "air" 2>/dev/null && echo "  Stopped Air process" || true
	@pkill -f "go run \." 2>/dev/null && echo "  Stopped go run process" || true
	@API_PORT=$$(grep -E '^PORT=' .env.dev 2>/dev/null | cut -d= -f2); \
	lsof -ti:$${API_PORT:-2002} | xargs kill -9 2>/dev/null && echo "  Killed process on port $${API_PORT:-2002}" || true
	@echo "✅ API server stopped"

air-install: ## Install Air live-reload tool
	@echo "📦 Installing Air..."
	go install github.com/air-verse/air@latest
	@echo "✅ Air installed"

local: build ## Run API locally with SAM (uses warm containers)
	@echo "🔥 Starting local API with warm containers..."
	sam local start-api --warm-containers EAGER --env-vars env.json

invoke-scanner: ## Invoke deposit scanner locally
	@echo "🔍 Invoking deposit scanner (ETH)..."
	sam local invoke DepositScannerFunction -e '{"chain": "eth"}'

invoke-scanner-remote: ## Invoke deposit scanner on AWS
	@echo "🔍 Invoking remote deposit scanner (ETH)..."
	aws lambda invoke --function-name vault-deposit-scanner-$(ENV) \
		--payload '{"chain": "eth"}' /dev/stdout

# =============================================================================
# Testing Commands
# =============================================================================

test: ## Run all tests
	@echo "🧪 Running tests..."
	go test ./... -v -count=1

test-coverage: ## Run tests with coverage report
	@echo "📊 Running tests with coverage..."
	go test ./... -coverprofile=coverage.out -count=1
	go tool cover -html=coverage.out
	@echo "✅ Coverage report generated: coverage.out"

test-race: ## Run tests with race detector
	@echo "🏁 Running tests with race detector..."
	go test ./... -race -count=1

test-verbose: ## Run tests with verbose output
	@echo "🔍 Running tests (verbose)..."
	go test ./... -v -count=1

test-unit: ## Run unit tests only (exclude integration tests)
	@echo "🧪 Running unit tests..."
	go test ./... -short -v -count=1

test-integration: ## Run integration tests only
	@echo "🔗 Running integration tests..."
	@TEST_DATABASE_URL=$(DATABASE_URL) go test ./... -run Integration -v -count=1

# =============================================================================
# Code Quality Commands
# =============================================================================

lint: ## Run golangci-lint
	@echo "🔍 Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "⚠️  golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

fmt: ## Format Go code
	@echo "✨ Formatting code..."
	go fmt ./...
	@echo "✅ Code formatted"

vet: ## Run go vet
	@echo "🔍 Running go vet..."
	go vet ./...
	@echo "✅ Vet complete"

security: ## Run security scan with gosec
	@echo "🔒 Running security scan..."
	@if command -v gosec >/dev/null 2>&1; then \
		gosec ./...; \
	else \
		echo "⚠️  gosec not installed. Install with: go install github.com/securego/gosec/v2/cmd/gosec@latest"; \
	fi

# =============================================================================
# Docker Commands (Local Development)
# =============================================================================

docker-up: ## Start local development environment (PostgreSQL + Redis)
	@echo "🐳 Starting local development environment..."
	$(DOCKER_COMPOSE) up -d
	@echo "⏳ Waiting for PostgreSQL to accept connections..."
	@for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do \
		docker exec vault-postgres pg_isready -U vault -d vault >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	@echo "✅ Development environment ready!"
	@echo "   PostgreSQL: localhost:$(or $(POSTGRES_PORT),5432)"
	@echo "   Redis:      localhost:$(or $(REDIS_PORT),6379)"
	@echo "   PgAdmin:    http://localhost:$(or $(PGADMIN_PORT),5050)"

docker-down: ## Stop local development environment
	@echo "🛑 Stopping local development environment..."
	$(DOCKER_COMPOSE) down
	@echo "✅ Environment stopped"

docker-down-volumes: ## Stop and remove volumes (WARNING: deletes data)
	@echo "⚠️  Stopping environment and removing volumes..."
	$(DOCKER_COMPOSE) down -v
	@echo "✅ Environment stopped and volumes removed"

docker-logs: ## View logs from all containers
	$(DOCKER_COMPOSE) logs -f

docker-logs-postgres: ## View PostgreSQL logs
	$(DOCKER_COMPOSE) logs -f postgres

docker-logs-redis: ## View Redis logs
	$(DOCKER_COMPOSE) logs -f redis

docker-build: ## Build Docker image for vault service
	@echo "🐳 Building Docker image: $(DOCKER_IMAGE_NAME):$(DOCKER_TAG)..."
	docker build -t $(DOCKER_IMAGE_NAME):$(DOCKER_TAG) .
	@echo "✅ Image built: $(DOCKER_IMAGE_NAME):$(DOCKER_TAG)"

docker-test: docker-build ## Build and run Docker container for testing
	@echo "🧪 Running Docker container for testing..."
	docker run --rm \
		-p 8080:8080 \
		--env-file .env.dev \
		-e DATABASE_URL=postgres://vault:vault@host.docker.internal:5432/vault?sslmode=disable \
		-e REDIS_URL=redis://host.docker.internal:6379 \
		$(DOCKER_IMAGE_NAME):$(DOCKER_TAG)

docker-shell: ## Open shell in PostgreSQL container
	@echo "🐚 Opening PostgreSQL shell..."
	$(DOCKER_COMPOSE) exec postgres psql -U vault -d vault

docker-redis-cli: ## Open Redis CLI
	@echo "🐚 Opening Redis CLI..."
	$(DOCKER_COMPOSE) exec redis redis-cli

docker-status: ## Show status of all vault containers
	@docker ps -a --filter name=vault --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}"

# =============================================================================
# Database Commands
# =============================================================================

db-reset: docker-down-volumes docker-up migrate ## Reset database (WARNING: deletes all data)
	@echo "✅ Database reset complete"

db-seed: ## Seed database with test data (admin@vault.dev / Password123!)
	$(call ensure_docker)
	@echo "🌱 Seeding database..."
	@export $$(grep -v '^#' .env | xargs) && go run cmd/seed/main.go

# =============================================================================
# ECR Commands
# =============================================================================

ecr-login: ## Login to AWS ECR
	@echo "🔐 Logging into AWS ECR..."
	@aws ecr get-login-password --region $(AWS_REGION) | docker login --username AWS --password-stdin $(ECR_REGISTRY)
	@echo "✅ Logged in to ECR"

ecr-create-repo: ## Create ECR repository
	@echo "📦 Creating ECR repository: $(DOCKER_IMAGE_NAME)..."
	@aws ecr describe-repositories --repository-names $(DOCKER_IMAGE_NAME) 2>/dev/null || \
	aws ecr create-repository \
		--repository-name $(DOCKER_IMAGE_NAME) \
		--image-scanning-configuration scanOnPush=true \
		--encryption-configuration encryptionType=AES256
	@echo "✅ ECR repository ready"

ecr-push: docker-build ecr-login ## Build and push Docker image to ECR
	@echo "📤 Pushing image to ECR..."
	docker tag $(DOCKER_IMAGE_NAME):$(DOCKER_TAG) $(ECR_IMAGE)
	docker push $(ECR_IMAGE)
	@echo "✅ Image pushed: $(ECR_IMAGE)"

# =============================================================================
# Monitoring Commands
# =============================================================================

logs-api: ## Tail API Lambda logs
	sam logs -n ApiFunction --stack-name $(STACK_NAME) --tail

logs-scanner: ## Tail deposit scanner logs
	sam logs -n DepositScannerFunction --stack-name $(STACK_NAME) --tail

logs-webhook: ## Tail webhook worker logs
	sam logs -n WebhookWorkerFunction --stack-name $(STACK_NAME) --tail

logs-withdrawal: ## Tail withdrawal worker logs
	sam logs -n WithdrawalWorkerFunction --stack-name $(STACK_NAME) --tail

dlq-check: ## Check DLQ message counts
	@echo "═══════════════════════════════════════"
	@echo "📊 Checking Dead Letter Queues"
	@echo "═══════════════════════════════════════"
	@echo "Webhook DLQ:"
	@aws sqs get-queue-attributes \
		--queue-url $$(aws sqs get-queue-url --queue-name vault-webhooks-dlq-$(ENV) --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text || echo "0"
	@echo ""
	@echo "Withdrawal DLQ:"
	@aws sqs get-queue-attributes \
		--queue-url $$(aws sqs get-queue-url --queue-name vault-withdrawals-dlq-$(ENV) --query QueueUrl --output text) \
		--attribute-names ApproximateNumberOfMessages \
		--query 'Attributes.ApproximateNumberOfMessages' --output text || echo "0"
	@echo "═══════════════════════════════════════"

dlq-replay-webhooks: ## Replay webhook DLQ messages back to main queue
	@echo "♻️  Replaying webhook DLQ messages..."
	aws sqs start-message-move-task \
		--source-arn $$(aws sqs get-queue-attributes --queue-url $$(aws sqs get-queue-url --queue-name vault-webhooks-dlq-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text) \
		--destination-arn $$(aws sqs get-queue-attributes --queue-url $$(aws sqs get-queue-url --queue-name vault-webhooks-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text)
	@echo "✅ Replay initiated"

dlq-replay-withdrawals: ## Replay withdrawal DLQ messages back to main queue
	@echo "♻️  Replaying withdrawal DLQ messages..."
	aws sqs start-message-move-task \
		--source-arn $$(aws sqs get-queue-attributes --queue-url $$(aws sqs get-queue-url --queue-name vault-withdrawals-dlq-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text) \
		--destination-arn $$(aws sqs get-queue-attributes --queue-url $$(aws sqs get-queue-url --queue-name vault-withdrawals-$(ENV) --query QueueUrl --output text) --attribute-names QueueArn --query Attributes.QueueArn --output text)
	@echo "✅ Replay initiated"

ping: ## Check API health endpoint
	@echo "🏓 Pinging API health endpoint..."
	@curl -s $$(aws cloudformation describe-stacks --stack-name $(STACK_NAME) \
		--query 'Stacks[0].Outputs[?OutputKey==`ApiEndpoint`].OutputValue' --output text)health | jq .

# =============================================================================
# Utility Commands
# =============================================================================

env-info: ## Display current environment configuration
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║              Environment Configuration                       ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"
	@echo ""
	@echo "Environment:        $(ENV)"
	@echo "Stack Name:         $(STACK_NAME)"
	@echo "AWS Region:         $(AWS_REGION)"
	@echo "AWS Account:        $(AWS_ACCOUNT_ID)"
	@echo ""
	@echo "Environment Variables:"
	@echo "  ENVIRONMENT       = $(ENVIRONMENT)"
	@echo "  DATABASE_URL      = $(DATABASE_URL)"
	@echo "  REDIS_URL         = $(REDIS_URL)"
	@echo "  ETH_RPC_URL       = $(ETH_RPC_URL)"
	@echo "  POLYGON_RPC_URL   = $(POLYGON_RPC_URL)"
	@echo "  SOLANA_RPC_URL    = $(SOLANA_RPC_URL)"
	@echo "  BTC_RPC_URL       = $(BTC_RPC_URL)"
	@echo ""

deps-install: ## Install development dependencies
	@echo "📦 Installing development dependencies..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	@echo "✅ Dependencies installed"

deps-update: ## Update Go dependencies
	@echo "🔄 Updating dependencies..."
	go get -u ./...
	go mod tidy
	@echo "✅ Dependencies updated"

swagger-install: ## Install Swagger CLI tool
	@echo "📦 Installing Swagger CLI..."
	go install github.com/swaggo/swag/cmd/swag@v1.8.12
	@echo "✅ Swagger CLI installed"

swagger-generate: ## Generate Swagger documentation
	@echo "📝 Generating Swagger documentation..."
	swag init -g main.go --output ./docs --parseDependency
	@echo "✅ Swagger docs generated in ./docs"
	@echo "   View at: http://localhost:$(or $(PORT),2002)/swagger/index.html"

swagger-fmt: ## Format Swagger comments
	@echo "✨ Formatting Swagger comments..."
	swag fmt
	@echo "✅ Swagger comments formatted"

# =============================================================================
# Database Migrations (Goravel)
# =============================================================================

migrate: ## Run pending database migrations
	$(call ensure_docker)
	@echo "🔄 Running database migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate

migrate-status: ## Show migration status
	$(call ensure_docker)
	@echo "📊 Checking migration status..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:status

migrate-rollback: ## Rollback last migration batch
	$(call ensure_docker)
	@echo "⏪ Rolling back migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:rollback

migrate-fresh: ## Drop all tables and re-run migrations
	$(call ensure_docker)
	@echo "🆕 Dropping tables and running fresh migrations..."
	@export $$(grep -v '^#' .env.dev | xargs) && go run . artisan migrate:fresh

.DEFAULT_GOAL := help
