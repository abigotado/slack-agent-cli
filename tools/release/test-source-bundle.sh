#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
bundle_script="$script_dir/create-source-bundle.sh"
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/slack-agent-cli-release-test.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
}
trap cleanup EXIT

repository="$temporary_dir/repository"
mkdir -p "$repository/assets/skills/slack" "$repository/cmd/slack-agent-cli" "$repository/docs/releases" "$repository/internal/cli" "$repository/tools/release/atomicrename"
cp "$script_dir/atomicrename/"*.go "$repository/tools/release/atomicrename/"
cd "$repository"
git init -q -b main
git config user.name "Release Test"
git config user.email "release-test@example.invalid"
printf 'module example.invalid/slack-agent-cli\n\ngo 1.25.0\n\nrequire golang.org/x/sys v0.47.0\n' >go.mod
cp "$script_dir/../../go.sum" go.sum
printf 'MIT\n' >LICENSE
printf 'internal/cli/archive.go export-subst\n' >.gitattributes
# The literal Git archive placeholders must survive fixture creation.
# shellcheck disable=SC2016
printf 'package cli\n\nconst Version = "$Format:%%(describe:tags)$"\nconst Commit = "$Format:%%H$"\n' >internal/cli/archive.go
printf 'package main\n\nimport (\n  "fmt"\n  "example.invalid/slack-agent-cli/internal/cli"\n)\n\nfunc main() { fmt.Printf("%%s %%s", cli.Version, cli.Commit) }\n' >cmd/slack-agent-cli/main.go
printf '# fixture Skill\n' >assets/skills/slack/SKILL.md
printf '# Changelog\n\n## [0.1.0] - 2026-09-02\n' >CHANGELOG.md
printf '# v0.1.0\n' >docs/releases/v0.1.0.md
git add .
GIT_AUTHOR_DATE=2026-09-02T00:00:00Z GIT_COMMITTER_DATE=2026-09-02T00:00:00Z git commit -q -m "fixture"
commit=$(git rev-parse HEAD)
git update-ref refs/remotes/origin/main "$commit"
GIT_COMMITTER_DATE=2026-09-02T00:00:01Z git tag -a v0.1.0 -m "v0.1.0" "$commit"

first="$temporary_dir/first"
second="$temporary_dir/second"
"$bundle_script" v0.1.0 "$commit" "$first" >/dev/null
"$bundle_script" v0.1.0 "$commit" "$second" >/dev/null

for asset in slack-agent-cli-0.1.0.tar.gz release-manifest.json SHA256SUMS; do
  cmp "$first/$asset" "$second/$asset"
done

tar -tzf "$first/slack-agent-cli-0.1.0.tar.gz" >"$temporary_dir/entries"
grep -Fxq 'slack-agent-cli-0.1.0/go.mod' "$temporary_dir/entries"
grep -Fxq 'slack-agent-cli-0.1.0/assets/skills/slack/SKILL.md' "$temporary_dir/entries"
if grep -Eq '(^|/)\.git(/|$)|(^|/)outputs(/|$)|(^|/)work(/|$)' "$temporary_dir/entries"; then
  echo "archive included forbidden local content" >&2
  exit 1
fi

extracted="$temporary_dir/extracted"
mkdir -p "$extracted"
tar -xzf "$first/slack-agent-cli-0.1.0.tar.gz" -C "$extracted"
(
  cd "$extracted/slack-agent-cli-0.1.0"
  GOWORK=off go build -o "$temporary_dir/source-binary" ./cmd/slack-agent-cli
)
source_identity=$("$temporary_dir/source-binary")
if [[ $source_identity != "v0.1.0 $commit" ]]; then
  echo "source archive build identity mismatch: $source_identity" >&2
  exit 1
fi

jq -e --arg commit "$commit" '
  .schema == 1 and
  .tag == "v0.1.0" and
  .version == "0.1.0" and
  .commit_sha == $commit and
  (.tag_object_sha | test("^[0-9a-f]{40}$")) and
  .source.name == "slack-agent-cli-0.1.0.tar.gz" and
  (.source.sha256 | test("^[0-9a-f]{64}$")) and
  (.source.size > 0)
' "$first/release-manifest.json" >/dev/null

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$first" && sha256sum -c SHA256SUMS >/dev/null)
else
  (cd "$first" && shasum -a 256 -c SHA256SUMS >/dev/null)
fi

fake_bin="$temporary_dir/fake-bin"
mkdir -p "$fake_bin"
printf '#!/usr/bin/env bash\nexit 23\n' >"$fake_bin/jq"
chmod +x "$fake_bin/jq"
fault_output="$temporary_dir/fault-output"
if PATH="$fake_bin:$PATH" "$bundle_script" v0.1.0 "$commit" "$fault_output" >/dev/null 2>&1; then
  echo "injected late failure unexpectedly succeeded" >&2
  exit 1
fi
if [[ -e $fault_output || -L $fault_output ]]; then
  echo "late failure exposed partial final assets" >&2
  exit 1
fi
"$bundle_script" v0.1.0 "$commit" "$fault_output" >/dev/null
for asset in slack-agent-cli-0.1.0.tar.gz release-manifest.json SHA256SUMS; do
  [[ -f $fault_output/$asset ]]
done

real_go=$(command -v go)
race_bin="$temporary_dir/race-bin"
mkdir -p "$race_bin"
# The wrapper creates the destination after the script's final existence check
# and immediately before the OS-level no-replace rename.
# shellcheck disable=SC2016
printf '#!/usr/bin/env bash\nset -euo pipefail\nif [[ $1 == run && $2 == ./tools/release/atomicrename ]]; then mkdir "$4"; fi\nexec "$REAL_GO" "$@"\n' >"$race_bin/go"
chmod +x "$race_bin/go"
race_output="$temporary_dir/race-output"
if REAL_GO="$real_go" PATH="$race_bin:$PATH" "$bundle_script" v0.1.0 "$commit" "$race_output" >/dev/null 2>&1; then
  echo "destination-appearance race unexpectedly succeeded" >&2
  exit 1
fi
if find "$race_output" -mindepth 1 -print -quit | grep -q .; then
  echo "destination-appearance race exposed final assets" >&2
  exit 1
fi
if find "$temporary_dir" -maxdepth 1 -name '.race-output.staging.*' -print -quit | grep -q .; then
  echo "destination-appearance race left staging assets" >&2
  exit 1
fi
rmdir "$race_output"
"$bundle_script" v0.1.0 "$commit" "$race_output" >/dev/null
for asset in slack-agent-cli-0.1.0.tar.gz release-manifest.json SHA256SUMS; do
  [[ -f $race_output/$asset ]]
done

if "$bundle_script" invalid "$commit" "$temporary_dir/invalid" >/dev/null 2>&1; then
  echo "invalid tag was accepted" >&2
  exit 1
fi
git tag v0.1.1 "$commit"
if "$bundle_script" v0.1.1 "$commit" "$temporary_dir/lightweight" >/dev/null 2>&1; then
  echo "lightweight tag was accepted" >&2
  exit 1
fi
git tag -a v0.1.2 -m v0.1.2 "$commit"
empty_commit=$(printf '' | git commit-tree "$(git mktree </dev/null)")
git update-ref refs/remotes/origin/main "$empty_commit"
if "$bundle_script" v0.1.2 "$commit" "$temporary_dir/not-main" >/dev/null 2>&1; then
  echo "commit outside origin/main was accepted" >&2
  exit 1
fi
git update-ref refs/remotes/origin/main "$commit"
printf 'dirty\n' >local-only.txt
if "$bundle_script" v0.1.0 "$commit" "$temporary_dir/dirty" >/dev/null 2>&1; then
  echo "dirty tree was accepted" >&2
  exit 1
fi

echo "source release bundle tests passed"
