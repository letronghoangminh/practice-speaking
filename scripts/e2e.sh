#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE=(docker compose -p practice-speaking-e2e -f "$ROOT_DIR/docker-compose.e2e.yml")

cleanup() {
  local status=$?
  if [ "$status" -ne 0 ]; then
    echo
    echo "E2E failed. Recent API and frontend logs:"
    "${COMPOSE[@]}" logs --no-color --tail=120 api frontend || true
  fi

  echo
  echo "Stopping isolated E2E stack..."
  "${COMPOSE[@]}" down -v --remove-orphans >/dev/null
  exit "$status"
}

trap cleanup EXIT

echo "Starting deterministic E2E stack with OpenAI disabled..."
"${COMPOSE[@]}" up --build -d

(
  cd "$ROOT_DIR/frontend"
  E2E_BASE_URL=http://localhost:13001 E2E_API_URL=http://localhost:18081/api/v1 npx playwright test "$@"
)
