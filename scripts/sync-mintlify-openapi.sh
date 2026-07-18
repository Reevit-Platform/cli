#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cp "$ROOT_DIR/backend/internal/docs/openapi.yaml" "$ROOT_DIR/mintlify-docs/openapi.yaml"
echo "Synced OpenAPI spec to mintlify-docs/openapi.yaml"
