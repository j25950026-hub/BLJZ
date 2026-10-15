#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-$ROOT_DIR/.cache/go-build}"
export GOMODCACHE="${GOMODCACHE:-$ROOT_DIR/.cache/gomod}"

export FOUR_LAYER_LOAD_USERS="${FOUR_LAYER_LOAD_USERS:-3500}"
export FOUR_LAYER_LOAD_LOCAL_OPERATORS="${FOUR_LAYER_LOAD_LOCAL_OPERATORS:-840}"
export FOUR_LAYER_LOAD_GLOBAL_OPERATORS="${FOUR_LAYER_LOAD_GLOBAL_OPERATORS:-210}"

run_layer() {
  local name="$1"
  shift
  echo "==== ${name} ===="
  "$@"
}

echo "四层测试参数:"
echo "  FOUR_LAYER_LOAD_USERS=${FOUR_LAYER_LOAD_USERS}"
echo "  FOUR_LAYER_LOAD_LOCAL_OPERATORS=${FOUR_LAYER_LOAD_LOCAL_OPERATORS}"
echo "  FOUR_LAYER_LOAD_GLOBAL_OPERATORS=${FOUR_LAYER_LOAD_GLOBAL_OPERATORS}"

run_layer "Layer 1 - Smoke" \
  go test ./internal/bot -run "^TestFourLayerSmokeScenario$" -count=1 -v

run_layer "Layer 2 - Business Closed Loop" \
  go test ./internal/bot ./internal/db ./internal/admin \
    -run "TestCreatePrivatePurchaseOrder|TestPrivatePurchaseOrderFlow|TestAdminPrivateUsersReviewOrder" \
    -count=1 -v

run_layer "Layer 3 - Permission And Exception" \
  go test ./internal/bot \
    -run "TestFourLayerPermissionAndExceptionScenario|TestControlPoint331PermissionAlert" \
    -count=1 -v

run_layer "Layer 4 - 3500 Users Multi-Role Burst" \
  go test ./internal/bot -run "^TestFourLayer3500UsersMultiRoleShortBurst$" -count=1 -v -timeout 20m

echo "==== 四层测试全部通过 ===="
