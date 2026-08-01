#!/usr/bin/env bash
# Provision a local kind cluster and deploy Prometheus with pprof enabled.
# Requires: kind, kubectl, helm
set -euo pipefail

CLUSTER="pgoctl-dev"
NAMESPACE="monitoring"
PROMETHEUS_CHART_VERSION="25.27.0"

check_deps() {
  for cmd in kind kubectl helm; do
    if ! command -v "$cmd" &>/dev/null; then
      echo "ERROR: $cmd not found — install it first" >&2
      exit 1
    fi
  done
}

check_deps

echo "==> Creating kind cluster: $CLUSTER"
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER}$"; then
  echo "    (cluster already exists, skipping)"
else
  kind create cluster --name "$CLUSTER"
fi

echo "==> Adding Helm repo: prometheus-community"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update

echo "==> Creating namespace: $NAMESPACE"
kubectl create namespace "$NAMESPACE" 2>/dev/null || true

echo "==> Installing kube-prometheus-stack v${PROMETHEUS_CHART_VERSION}"
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
  --namespace "$NAMESPACE" \
  --version "$PROMETHEUS_CHART_VERSION" \
  --set 'prometheus.prometheusSpec.additionalArgs[0].name=web.enable-pprof' \
  --set 'prometheus.prometheusSpec.additionalArgs[0].value=' \
  --wait --timeout 5m

echo ""
echo "==> Prometheus ready. Port-forward with:"
echo "    kubectl port-forward -n $NAMESPACE svc/prometheus-kube-prometheus-prometheus 9090:9090 &"
echo ""
echo "    Then: make collect-baseline"
