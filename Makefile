.PHONY: help install dev dev-frontend dev-backend build test test-backend test-watch test-ui up down db clean k8s-up k8s-down k8s-status k8s-logs-backend k8s-logs-frontend k8s-policies k8s-monitoring-status argocd-status argocd-sync argocd-logs kustomize-build jaeger-ui

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
	cd backend && go build -o server .

build-frontend: ## Build frontend for production
	npm run build --prefix frontend

# ─── Test ─────────────────────────────────────────────────────────────────────

test: ## Run all tests (frontend + backend)
	npm test --prefix frontend
	cd backend && go test ./... -count=1

test-backend: ## Run backend Go tests (needs MongoDB for integration tests)
	cd backend && go test ./... -v -count=1

test-watch: ## Run tests in watch mode
	npm run test:watch --prefix frontend

test-ui: ## Run tests with browser UI
	npm run test:ui --prefix frontend

# ─── Docker Compose ───────────────────────────────────────────────────────────

up: ## Start full stack with Docker Compose
	docker compose up --build

down: ## Stop Docker Compose services
	docker compose down

# ─── Kubernetes (kind + Harbor + Kyverno) ─────────────────────────────────────

k8s-up: ## Create kind cluster, install Harbor + Kyverno, deploy DeBOX
	bash k8s/setup.sh

k8s-down: ## Delete the kind cluster entirely
	bash k8s/teardown.sh

k8s-status: ## Show pod status in debox namespace
	kubectl get pods -n debox -o wide

k8s-logs-backend: ## Tail backend logs
	kubectl logs -f -l app=backend -n debox

k8s-logs-frontend: ## Tail frontend logs
	kubectl logs -f -l app=frontend -n debox

k8s-policies: ## Show Kyverno policy status
	kubectl get clusterpolicy
	kubectl get policyreport -A

# ─── Monitoring ──────────────────────────────────────────────────────────────

k8s-monitoring-status: ## Show monitoring pod status
	kubectl get pods -n monitoring -o wide

jaeger-ui: ## Port-forward Jaeger UI to http://localhost:16686
	kubectl port-forward svc/jaeger -n monitoring 16686:16686

# ─── ArgoCD ──────────────────────────────────────────────────────────────────

argocd-status: ## Show ArgoCD application sync status
	kubectl get applications -n argocd

argocd-sync: ## Force sync all ArgoCD applications
	kubectl get applications -n argocd -o name | xargs -I{} kubectl -n argocd patch {} --type merge -p '{"operation":{"initiatedBy":{"username":"admin"},"sync":{"revision":"HEAD"}}}'

argocd-logs: ## Tail ArgoCD server logs
	kubectl logs -f -l app.kubernetes.io/name=argocd-server -n argocd

# ─── Kustomize ───────────────────────────────────────────────────────────────

kustomize-build: ## Render Kustomize manifests locally (all components)
	@echo "=== Backend ===" && kubectl kustomize k8s/backend
	@echo "---"
	@echo "=== Frontend ===" && kubectl kustomize k8s/frontend
	@echo "---"
	@echo "=== Mongo ===" && kubectl kustomize k8s/mongo

# ─── Cleanup ──────────────────────────────────────────────────────────────────

clean: ## Remove build artifacts
	rm -f backend/server
	rm -rf frontend/dist
