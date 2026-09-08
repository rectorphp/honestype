#!/usr/bin/env bash
# Run docblockcheck against laravel/framework.
# Clones the framework, instruments its src, runs a slice of its test suite,
# then renders the docblock type mismatches as a Checkstyle report.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${WORK:-/tmp/docblockcheck-laravel}"
REF="${LARAVEL_REF:-11.x}"
TEST_PATH="${TEST_PATH:-tests/Support}"   # narrow slice; set to tests/ for full

echo ">> building docblockcheck"
( cd "$REPO_ROOT" && go build -o "$WORK/docblockcheck" . ) 2>/dev/null || \
  ( mkdir -p "$WORK" && cd "$REPO_ROOT" && go build -o "$WORK/docblockcheck" . )
DBC="$WORK/docblockcheck"

if [ ! -d "$WORK/framework" ]; then
  echo ">> cloning laravel/framework ($REF)"
  git clone --depth 1 --branch "$REF" https://github.com/laravel/framework.git "$WORK/framework"
fi
cd "$WORK/framework"

echo ">> composer install"
composer install --no-interaction --no-progress --quiet

echo ">> instrument src/"
"$DBC" instrument src

# helper loaded before the suite; prepend it via PHP ini
HELPER="$WORK/framework/src/docblock_check.php"
export DOCBLOCK_CHECK_LOG="$WORK/docblock-check.log"
rm -f "$DOCBLOCK_CHECK_LOG"

echo ">> run tests ($TEST_PATH)"
php -d auto_prepend_file="$HELPER" vendor/bin/phpunit "$TEST_PATH" || true

echo ">> restore src/"
git checkout -- src && rm -f "$HELPER"

echo ">> report"
"$DBC" report -checkstyle "$WORK/checkstyle.xml" "$DOCBLOCK_CHECK_LOG" || true
echo ">> checkstyle at $WORK/checkstyle.xml"
