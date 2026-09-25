#!/bin/sh
set -eu

fail() {
  printf 'release boundary check failed: %b\n' "$1" >&2
  exit 1
}

# The remote repository is a production release repository. Only files needed
# to build the image, publish it, deploy it, or operate the deployment belong
# here. Check tracked files because ignored local runtime files are not part of
# the GitHub release boundary.
unexpected=''
for path in $(git ls-files); do
  [ -e "$path" ] || continue
  case "$path" in
    .dockerignore|.env.example|.gitignore|Dockerfile|Makefile|README.md|go.mod|go.sum|\
    compose.yaml|compose.production.yaml|compose.production.private-ca.yaml|\
    .github/dependabot.yml|.github/workflows/ci.yml|\
    cmd/server/*|internal/*|configs/*|deploy/helm/api-manager/*|\
    scripts/check-release-boundaries.sh|scripts/init-production-secrets.py|\
    scripts/preflight-production.py|scripts/verify-production.py|scripts/backup-production.py)
      ;;
    *)
      unexpected="${unexpected}${path}\n"
      ;;
  esac
done
[ -z "$unexpected" ] || fail "files outside the build/deployment allowlist:\n$unexpected"

for path in tests docs migrations integrations plugins/examples cmd/rotate-credential-encryption; do
  [ ! -e "$path" ] || fail "$path is not part of the release repository"
done

test_files=$(find . -path './.git' -prune -o -type f \( -name '*_test.go' -o -name '*.test.cjs' -o -name 'test_*.py' \) -print)
[ -z "$test_files" ] || fail "test sources are not allowed:\n$test_files"

wasm_files=$(find . -path './.git' -prune -o -type f -name '*.wasm' -print)
[ -z "$wasm_files" ] || fail "bundled WASM modules are not allowed:\n$wasm_files"

fixture_refs=$(grep -RIlE 'game[-_ ]discount|gmae[-_ ]discount|wasm-demo|region=HK' . \
  --exclude-dir=.git --exclude='check-release-boundaries.sh' 2>/dev/null || true)
[ -z "$fixture_refs" ] || fail "test integration references remain:\n$fixture_refs"

markdown_files=$(find . -path './.git' -prune -o -type f -name '*.md' ! -path './README.md' -print)
[ -z "$markdown_files" ] || fail "only the deployment README may remain:\n$markdown_files"
