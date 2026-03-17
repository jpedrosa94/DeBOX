.PHONY: help install dev dev-frontend dev-backend build test test-watch test-ui up down db clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

# ─── Install ──────────────────────────────────────────────────────────────────

install: ## Install all dependencies (frontend + backend)
	npm ci --prefix frontend
	cd backend && go mod download

# ─── Local Development ────────────────────────────────────────────────────────

dev: ## Run frontend + backend concurrently (needs MongoDB running)
	npx concurrently -n "backend,frontend" -c "cyan,magenta" \
		"cd backend && go run main.go" \
		"npm run dev --prefix frontend"

dev-frontend: ## Run frontend only
	npm run dev --prefix frontend

dev-backend: ## Run backend only (needs MongoDB running)
	cd backend && go run main.go

db: ## Start MongoDB in Docker (detached)
	docker run -d --name debox-mongo -p 27017:27017 -v debox-mongo-data:/data/db mongo:7

# ─── Build ────────────────────────────────────────────────────────────────────

build: build-backend build-frontend ## Build backend + frontend

build-backend: ## Build Go backend binary
	cd backend && go build -o server main.go

build-frontend: ## Build frontend for production
	npm run build --prefix frontend

# ─── Test ─────────────────────────────────────────────────────────────────────

test: ## Run all tests
	npm test --prefix frontend

test-watch: ## Run tests in watch mode
	npm run test:watch --prefix frontend

test-ui: ## Run tests with browser UI
	npm run test:ui --prefix frontend

# ─── Docker Compose ───────────────────────────────────────────────────────────

up: ## Start full stack with Docker Compose
	docker compose up --build

down: ## Stop Docker Compose services
	docker compose down

# ─── Cleanup ──────────────────────────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -f backend/server
	rm -rf frontend/dist
