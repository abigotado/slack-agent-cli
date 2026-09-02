#!/usr/bin/env bash
set -euo pipefail

repository_root=$(git rev-parse --show-toplevel)
cd "$repository_root"
if [[ -n $(git status --porcelain --untracked-files=all) ]]; then
  echo "production source-bundle test requires a clean committed tree" >&2
  exit 1
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/slack-agent-cli-production-release-test.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT

clone="$temporary_dir/repository"
git clone -q --no-hardlinks "$repository_root" "$clone"
cd "$clone"
git config user.name "Release Test"
git config user.email "release-test@example.invalid"
git tag -d v0.1.0 >/dev/null 2>&1 || true
commit=$(git rev-parse HEAD)
git update-ref refs/remotes/origin/main "$commit"
GIT_COMMITTER_DATE=2026-09-02T00:00:01Z git tag -a v0.1.0 -m "v0.1.0" "$commit"

bundle="$temporary_dir/bundle"
tools/release/create-source-bundle.sh v0.1.0 "$commit" "$bundle" >/dev/null
extracted="$temporary_dir/extracted"
mkdir -p "$extracted"
tar -xzf "$bundle/slack-agent-cli-0.1.0.tar.gz" -C "$extracted"
cd "$extracted/slack-agent-cli-0.1.0"
GOWORK=off go build -o "$temporary_dir/slack-agent-cli" ./cmd/slack-agent-cli
"$temporary_dir/slack-agent-cli" version |
  jq -e --arg commit "$commit" '
    .ok == true and
    .v == 1 and
    .data.version == "v0.1.0" and
    .data.commit == $commit
  ' >/dev/null
jq -e --arg commit "$commit" '
  .tag == "v0.1.0" and
  .version == "0.1.0" and
  .commit_sha == $commit
' "$bundle/release-manifest.json" >/dev/null

echo "production source release identity test passed"
