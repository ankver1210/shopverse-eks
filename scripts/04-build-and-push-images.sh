#!/usr/bin/env bash
# Step 4 — Build the 10 container images and push them to Amazon ECR.
#
# Usage:
#   ./scripts/04-build-and-push-images.sh                  # all services
#   ./scripts/04-build-and-push-images.sh cart-service     # just one (or several)
#   RUN_TESTS=1 ./scripts/04-build-and-push-images.sh      # run unit tests inside Docker first
#
# Images are built for linux/amd64 (the worker node architecture), even on Apple Silicon Macs.
# The new tag is written into deploy/helm/values/<service>.yaml so the next deploy uses it.
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
require_cmd docker
load_infra

targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=("${SERVICES[@]}")
for svc in "${targets[@]}"; do is_valid_service "$svc" || die "Unknown service '$svc'. Valid: ${SERVICES[*]}"; done

git_sha="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo manual)"
TAG="${TAG:-${git_sha}-$(date +%Y%m%d%H%M%S)}"

step "Logging in to ECR (${ECR_REGISTRY})"
aws ecr get-login-password --region "$REGION" | docker login --username AWS --password-stdin "$ECR_REGISTRY"

total=${#targets[@]}; n=0
for svc in "${targets[@]}"; do
  n=$((n + 1))
  image="${ECR_REGISTRY}/shopverse/${svc}:${TAG}"
  step "[${n}/${total}] ${svc} -> ${image}"
  if [ "${RUN_TESTS:-0}" = "1" ]; then
    info "running unit tests (docker build --target test)..."
    docker build --platform linux/amd64 --target test -t "shopverse/${svc}:test" "${ROOT_DIR}/services/${svc}"
  fi
  docker buildx build \
    --platform linux/amd64 \
    --build-arg APP_VERSION="$TAG" \
    --provenance=false \
    --tag "$image" \
    --push \
    "${ROOT_DIR}/services/${svc}"
  sed_inplace "s#^(  tag: ).*#\1\"${TAG}\"#" "${VALUES_DIR}/${svc}.yaml"
  ok "pushed and recorded tag ${TAG} in deploy/helm/values/${svc}.yaml"
done

step "All images pushed"
info "Tag: ${TAG}"
info "Next: ./scripts/05-deploy-app.sh   (or, if you use GitOps: commit + push, Argo CD deploys it)"
