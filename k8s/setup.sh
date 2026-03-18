#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
CLUSTER_NAME="debox-local"

# ─── Prerequisites ────────────────────────────────────────────────────────────

echo "Checking prerequisites..."
for cmd in docker kind kubectl helm; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "ERROR: '$cmd' is not installed."
    exit 1
  fi
done
echo "All prerequisites found."

# ─── Step 1: Create kind cluster ──────────────────────────────────────────────

if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
  echo "Creating kind cluster '${CLUSTER_NAME}'..."
  kind create cluster --name "$CLUSTER_NAME" --config "$SCRIPT_DIR/kind-config.yaml"
else
  echo "Cluster '${CLUSTER_NAME}' already exists."
fi
kubectl cluster-info --context "kind-${CLUSTER_NAME}"

# ─── Step 2: Create namespaces ────────────────────────────────────────────────

echo "Creating namespaces..."
kubectl apply -f "$SCRIPT_DIR/namespaces.yaml"

# ─── Step 3: Create secrets from .env files ──────────────────────────────────

echo "Creating secrets from .env files..."

GOOGLE_CLIENT_ID=""
if [ -f "$PROJECT_ROOT/backend/.env" ]; then
  GOOGLE_CLIENT_ID=$(grep -E '^GOOGLE_CLIENT_ID=' "$PROJECT_ROOT/backend/.env" | cut -d= -f2- || echo "")
fi
kubectl create secret generic backend-secrets \
  -n debox \
  --from-literal="GOOGLE_CLIENT_ID=${GOOGLE_CLIENT_ID}" \
  --dry-run=client -o yaml | kubectl apply -f -

VITE_GOOGLE_CLIENT_ID=""
VITE_ENOKI_API_KEY=""
VITE_SEAL_PACKAGE_ID=""
if [ -f "$PROJECT_ROOT/frontend/.env" ]; then
  VITE_GOOGLE_CLIENT_ID=$(grep -E '^VITE_GOOGLE_CLIENT_ID=' "$PROJECT_ROOT/frontend/.env" | cut -d= -f2- || echo "")
  VITE_ENOKI_API_KEY=$(grep -E '^VITE_ENOKI_API_KEY=' "$PROJECT_ROOT/frontend/.env" | cut -d= -f2- || echo "")
  VITE_SEAL_PACKAGE_ID=$(grep -E '^VITE_SEAL_PACKAGE_ID=' "$PROJECT_ROOT/frontend/.env" | cut -d= -f2- || echo "")
fi
kubectl create secret generic frontend-secrets \
  -n debox \
  --from-literal="VITE_GOOGLE_CLIENT_ID=${VITE_GOOGLE_CLIENT_ID}" \
  --from-literal="VITE_ENOKI_API_KEY=${VITE_ENOKI_API_KEY}" \
  --from-literal="VITE_SEAL_PACKAGE_ID=${VITE_SEAL_PACKAGE_ID}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ─── Step 4: Install ArgoCD ──────────────────────────────────────────────────

helm repo add argo https://argoproj.github.io/argo-helm 2>/dev/null || true
helm repo update

if ! helm status argocd -n argocd &>/dev/null; then
  echo "Installing ArgoCD..."
  helm install argocd argo/argo-cd \
    -n argocd \
    -f "$SCRIPT_DIR/argocd/values.yaml" \
    --wait --timeout 10m
else
  echo "ArgoCD already installed."
fi

echo "Waiting for ArgoCD server to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=argocd-server -n argocd --timeout=300s

# ─── Step 5: Apply ArgoCD apps ───────────────────────────────────────────────

echo "Applying ArgoCD Applications..."
kubectl apply -f "$SCRIPT_DIR/argocd/apps/"

# ─── Done ─────────────────────────────────────────────────────────────────────

echo ""
echo "=== DeBOX bootstrap complete ==="
echo ""
echo "ArgoCD is now syncing all applications."
echo "Watch progress:  kubectl get applications -n argocd -w"
echo ""
echo "  ArgoCD UI:  http://localhost:8080"
echo "  Frontend:   http://localhost:9080  (once debox-frontend syncs)"
echo "  Grafana:    http://localhost:3000  (once kube-prometheus-stack syncs)"
echo "  Jaeger UI:  http://localhost:16686 (once monitoring-resources syncs)"
echo ""
kubectl get applications -n argocd 2>/dev/null || true
