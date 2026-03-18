#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
CLUSTER_NAME="debox-local"
HARBOR_HOST="localhost:5443"
REGISTRY="localhost:5443/debox"

# ─── Prerequisites ────────────────────────────────────────────────────────────

echo "Checking prerequisites..."
for cmd in docker kind kubectl helm curl cosign; do
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

# ─── Step 3: Install Harbor ───────────────────────────────────────────────────

helm repo add harbor https://helm.goharbor.io 2>/dev/null || true
helm repo update

if ! helm status harbor -n harbor &>/dev/null; then
  echo "Installing Harbor (this may take a few minutes)..."
  helm install harbor harbor/harbor \
    -n harbor \
    -f "$SCRIPT_DIR/harbor/values.yaml" \
    --wait --timeout 10m
else
  echo "Harbor already installed."
fi

echo "Waiting for Harbor pods to be ready..."
kubectl wait --for=condition=ready pod -l app=harbor -n harbor --timeout=300s 2>/dev/null || true

# Give Harbor API time to initialize
echo "Waiting for Harbor API..."
for i in $(seq 1 30); do
  if curl -sf "http://${HARBOR_HOST}/api/v2.0/health" &>/dev/null; then
    break
  fi
  sleep 5
done

# Create 'debox' project in Harbor
echo "Creating 'debox' project in Harbor..."
curl -sf -o /dev/null \
  -X POST "http://${HARBOR_HOST}/api/v2.0/projects" \
  -H "Content-Type: application/json" \
  -u "admin:Harbor12345" \
  -d '{"project_name":"debox","public":true}' 2>/dev/null || true

# ─── Step 4: Configure containerd to trust Harbor ─────────────────────────────

echo "Configuring containerd in kind node to trust Harbor..."
HARBOR_SVC_IP=$(kubectl get svc harbor-core -n harbor -o jsonpath='{.spec.clusterIP}' 2>/dev/null || echo "")

if [ -n "$HARBOR_SVC_IP" ]; then
  docker exec "${CLUSTER_NAME}-control-plane" bash -c "
    mkdir -p /etc/containerd/certs.d/localhost:5443
    cat > /etc/containerd/certs.d/localhost:5443/hosts.toml <<EOF
server = \"http://${HARBOR_SVC_IP}:80\"

[host.\"http://${HARBOR_SVC_IP}:80\"]
  capabilities = [\"pull\", \"resolve\", \"push\"]
  skip_verify = true
EOF
  "
  docker exec "${CLUSTER_NAME}-control-plane" systemctl restart containerd
  sleep 5
  echo "containerd configured."
else
  echo "WARNING: Could not get Harbor ClusterIP. You may need to configure containerd manually."
fi

# ─── Step 5: Generate cosign key pair ────────────────────────────────────────

if [ ! -f "$SCRIPT_DIR/cosign.key" ]; then
  echo "Generating cosign key pair..."
  COSIGN_PASSWORD="" cosign generate-key-pair --output-key-prefix="$SCRIPT_DIR/cosign"
else
  echo "Cosign key pair already exists."
fi

if [ ! -f "$SCRIPT_DIR/cosign-signing-config.json" ]; then
  echo "Generating cosign signing config (no transparency log)..."
  cosign signing-config create \
    --no-default-fulcio --no-default-oidc --no-default-rekor --no-default-tsa \
    --out "$SCRIPT_DIR/cosign-signing-config.json"
fi

# ─── Step 6: Build, push, and sign images ────────────────────────────────────

echo "Building, pushing, and signing images..."
docker login localhost:5443 -u admin -p Harbor12345

# MongoDB: pull, retag, push, sign
echo "  Pulling, pushing, and signing mongo:7..."
docker pull mongo:7
docker tag mongo:7 "${REGISTRY}/mongo:7"
docker push "${REGISTRY}/mongo:7"
COSIGN_PASSWORD="" cosign sign --key "$SCRIPT_DIR/cosign.key" --signing-config="$SCRIPT_DIR/cosign-signing-config.json" --yes "${REGISTRY}/mongo:7"

# Backend
echo "  Building, pushing, and signing backend..."
docker build -t "${REGISTRY}/backend:latest" "$PROJECT_ROOT/backend"
docker push "${REGISTRY}/backend:latest"
COSIGN_PASSWORD="" cosign sign --key "$SCRIPT_DIR/cosign.key" --signing-config="$SCRIPT_DIR/cosign-signing-config.json" --yes "${REGISTRY}/backend:latest"

# Frontend
echo "  Building, pushing, and signing frontend..."
docker build -t "${REGISTRY}/frontend:latest" "$PROJECT_ROOT/frontend"
docker push "${REGISTRY}/frontend:latest"
COSIGN_PASSWORD="" cosign sign --key "$SCRIPT_DIR/cosign.key" --signing-config="$SCRIPT_DIR/cosign-signing-config.json" --yes "${REGISTRY}/frontend:latest"

# ─── Step 7: Create secrets from .env files ──────────────────────────────────

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

# ─── Step 8: Install ArgoCD ──────────────────────────────────────────────────

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

# ─── Step 9: Apply ArgoCD apps ───────────────────────────────────────────────

echo "Applying ArgoCD Applications..."
kubectl apply -f "$SCRIPT_DIR/argocd/apps/"

# ─── Done ─────────────────────────────────────────────────────────────────────

echo ""
echo "=== DeBOX bootstrap complete ==="
echo ""
echo "ArgoCD is now syncing all applications (Kyverno, MongoDB, Backend, Frontend, Monitoring)."
echo "Watch sync progress:"
echo "  kubectl get applications -n argocd -w"
echo ""
echo "  ArgoCD UI:  http://localhost:8080  (no auth, local dev)"
echo "  Frontend:   http://localhost:9080  (available once debox-frontend syncs)"
echo "  Harbor UI:  http://localhost:5443  (admin / Harbor12345)"
echo "  Grafana:    http://localhost:3000  (available once kube-prometheus-stack syncs)"
echo "  Jaeger UI:  http://localhost:16686 (available once monitoring-resources syncs)"
echo ""
kubectl get applications -n argocd 2>/dev/null || true
