#!/usr/bin/env bash
# Runs once after the dev container is created: caches modules, builds the SPA,
# applies migrations and creates the initial administrator.
set -euo pipefail
cd "$(dirname "$0")/.."
# Bind-mounted caches are owned by root on first creation.
sudo chown -R "$(id -u):$(id -g)" /go/pkg/mod ~/.cache/go-build web/node_modules 2>/dev/null || true
git config --global --add safe.directory /workspaces/konnyaku
go mod download
# The store must share a filesystem with node_modules (hard links); without the
# flag pnpm falls back to a .pnpm-store directory at the repository root.
(cd web && pnpm install --frozen-lockfile --store-dir node_modules/.pnpm-store && pnpm build)
for i in $(seq 1 30); do
  if go run ./cmd/konnyaku migrate; then break; fi
  echo "waiting for PostgreSQL ($i)"; sleep 2
done
go run ./cmd/konnyaku create-admin 2>/dev/null || echo "administrator already exists"
echo
echo "Ready. Run 'make run' (http://localhost:8080, admin@example.com / admin-password-123)"
echo "or 'cd web && pnpm dev' for the Vite dev server on :5173."
