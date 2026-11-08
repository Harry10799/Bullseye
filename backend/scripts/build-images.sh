#!/usr/bin/env bash
# Build Bullseye Docker images.
#
# Usage:
#   ./backend/scripts/build-images.sh              # build all 3 images, tagged "latest"
#   ./backend/scripts/build-images.sh --version v0.1.0
#   ./backend/scripts/build-images.sh --registry ghcr.io/your-org
#   ./backend/scripts/build-images.sh --multiarch  # buildx for amd64 + arm64
#   ./backend/scripts/build-images.sh --push       # push after build
#
# Prereqs:
#   - Docker (with buildx for multi-arch)
#   - For multi-arch: docker buildx install && docker run --privileged --rm tonistiigi/binfmt --install all

set -euo pipefail

BACKEND_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROJECT_DIR="$(cd "$BACKEND_DIR/.." && pwd)"
cd "$PROJECT_DIR"

VERSION="latest"
REGISTRY="harikumar15"
MULTIARCH=false
PUSH=false
PLATFORMS="linux/amd64,linux/arm64"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)    VERSION="$2"; shift 2 ;;
        --registry)   REGISTRY="$2"; shift 2 ;;
        --multiarch)  MULTIARCH=true; shift ;;
        --push)       PUSH=true; shift ;;
        --platforms)  PLATFORMS="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: $0 [--version <tag>] [--registry <name>] [--multiarch] [--push] [--platforms <list>]"
            exit 0
            ;;
        *) echo "unknown flag: $1"; exit 1 ;;
    esac
done

BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_ARGS=(--build-arg "VERSION=$VERSION" --build-arg "BUILD_DATE=$BUILD_DATE" --build-arg "COMMIT=$COMMIT")

HUB_IMG="$REGISTRY/bulleyes-hub:$VERSION"
SNIFFER_IMG="$REGISTRY/bulleyes-sniffer:$VERSION"
DASHBOARD_IMG="$REGISTRY/bulleyes-dashboard:$VERSION"

echo "[build-images] configuration:"
echo "  version:   $VERSION"
echo "  registry:  $REGISTRY"
echo "  commit:    $COMMIT"
echo "  build date:$BUILD_DATE"
echo "  multiarch: $MULTIARCH"
echo "  push:      $PUSH"
echo "  platforms: $PLATFORMS"
echo ""

if [[ "$MULTIARCH" == "true" ]]; then
    PUSH_FLAG=""
    if [[ "$PUSH" == "true" ]]; then PUSH_FLAG="--push"; fi
    echo "[build-images] building multi-arch images with buildx"
    docker buildx build --platform "$PLATFORMS" "${BUILD_ARGS[@]}" -t "$HUB_IMG" -f backend/Dockerfile.hub backend/ $PUSH_FLAG
    docker buildx build --platform "$PLATFORMS" "${BUILD_ARGS[@]}" -t "$SNIFFER_IMG" -f backend/Dockerfile.sniffer backend/ $PUSH_FLAG
    docker buildx build --platform "$PLATFORMS" "${BUILD_ARGS[@]}" -t "$DASHBOARD_IMG" -f Dockerfile.dashboard . $PUSH_FLAG
else
    echo "[build-images] building single-arch images"
    docker build "${BUILD_ARGS[@]}" -t "$HUB_IMG" -f backend/Dockerfile.hub backend/
    docker build "${BUILD_ARGS[@]}" -t "$SNIFFER_IMG" -f backend/Dockerfile.sniffer backend/
    docker build "${BUILD_ARGS[@]}" -t "$DASHBOARD_IMG" -f Dockerfile.dashboard .

    if [[ "$PUSH" == "true" ]]; then
        echo "[build-images] pushing images"
        docker push "$HUB_IMG"
        docker push "$SNIFFER_IMG"
        docker push "$DASHBOARD_IMG"
    fi
fi

echo ""
echo "[build-images] ✓ done. Built images:"
docker images | grep -E "^$REGISTRY/bulleyes-(hub|sniffer|dashboard) " | head -5
