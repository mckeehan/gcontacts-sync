#!/usr/bin/env bash
# setup.sh — run this once after downloading the project files.
# It creates the correct directory layout and fetches all dependencies.

set -euo pipefail

PROJECT="gcontacts-sync"

echo "=== $PROJECT setup ==="

# ── 1. Ensure we're in the right place ──────────────────────────────────────
if [[ ! -f go.mod ]]; then
  echo "ERROR: run this script from the project root (the folder containing go.mod)"
  exit 1
fi

MODULE=$(head -1 go.mod | awk '{print $2}')
echo "Module: $MODULE"

# ── 2. Create directory structure ───────────────────────────────────────────
mkdir -p \
  internal/auth \
  internal/contacts \
  internal/markdown \
  internal/model \
  internal/sync \
  example-output

echo "✓ Directories created"

# ── 3. Move any files that landed in the wrong place ────────────────────────
# If someone dumped all .go files flat into the root, move them.
for f in auth.go; do
  [[ -f "$f" ]] && mv -v "$f" internal/auth/
done
for f in service.go; do
  [[ -f "$f" ]] && mv -v "$f" internal/contacts/
done
for f in store.go store_test.go; do
  [[ -f "$f" ]] && mv -v "$f" internal/markdown/
done
for f in contact.go; do
  [[ -f "$f" ]] && mv -v "$f" internal/model/
done
for f in syncer.go report.go fs.go; do
  [[ -f "$f" ]] && mv -v "$f" internal/sync/
done

echo "✓ Files in place"

# ── 4. Fetch dependencies and generate go.sum ────────────────────────────────
echo "Fetching dependencies (this may take a moment)…"
go get golang.org/x/oauth2@latest
go get golang.org/x/oauth2/google@latest
go get google.golang.org/api/people/v1@latest
go get google.golang.org/api/option@latest
go get gopkg.in/yaml.v3@latest
go mod tidy

echo "✓ Dependencies fetched"

# ── 5. Verify build ──────────────────────────────────────────────────────────
echo "Building…"
go build ./...
echo "✓ Build succeeded"

echo ""
echo "═══════════════════════════════════════════"
echo " Setup complete!"
echo "═══════════════════════════════════════════"
echo ""
echo " Next steps:"
echo "  1. Download credentials.json from Google Cloud Console"
echo "     https://console.cloud.google.com/apis/credentials"
echo "     (OAuth 2.0 → Desktop app → Download JSON → save as credentials.json)"
echo ""
echo "  2. Enable the People API:"
echo "     https://console.cloud.google.com/apis/library/people.googleapis.com"
echo ""
echo "  3. Run your first pull:"
echo "     go run . --pull --verbose"
echo ""
echo "  See README.md for full usage."
