#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: create-source-bundle.sh TAG COMMIT OUTPUT_DIR" >&2
  exit 2
fi

tag=$1
commit=$2
output_dir=$3

if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "release tag must be stable SemVer with a v prefix" >&2
  exit 2
fi
if [[ ! $commit =~ ^[0-9a-f]{40}$ ]]; then
  echo "release commit must be a full lowercase SHA-1" >&2
  exit 2
fi
if [[ $output_dir != /* ]]; then
  echo "output directory must be absolute" >&2
  exit 2
fi

repository_root=$(git rev-parse --show-toplevel)
cd "$repository_root"

if [[ -n $(git status --porcelain --untracked-files=all) ]]; then
  echo "release source tree must be clean" >&2
  exit 1
fi
if [[ $(git cat-file -t "refs/tags/$tag" 2>/dev/null) != tag ]]; then
  echo "release tag must be an annotated tag" >&2
  exit 1
fi

tag_object_sha=$(git rev-parse "refs/tags/$tag")
peeled_commit_sha=$(git rev-parse "refs/tags/$tag^{commit}")
if [[ $peeled_commit_sha != "$commit" ]]; then
  echo "release tag does not peel to the requested commit" >&2
  exit 1
fi
if ! git show-ref --verify --quiet refs/remotes/origin/main; then
  echo "origin/main is required for release validation" >&2
  exit 1
fi
if ! git merge-base --is-ancestor "$commit" refs/remotes/origin/main; then
  echo "release commit is not reachable from origin/main" >&2
  exit 1
fi

version=${tag#v}
notes_path="docs/releases/$tag.md"
if ! git cat-file -e "$commit:$notes_path" 2>/dev/null; then
  echo "release notes are missing: $notes_path" >&2
  exit 1
fi
if ! git show "$commit:CHANGELOG.md" |
  grep -Eq "^## \[$version\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$"; then
  echo "CHANGELOG.md has no exact entry for $version" >&2
  exit 1
fi

output_parent=$(dirname "$output_dir")
output_name=$(basename "$output_dir")
if [[ $output_name == "." || $output_name == ".." || $output_dir == "/" ]]; then
  echo "output directory must name a new child directory" >&2
  exit 2
fi
mkdir -p "$output_parent"
if [[ -e $output_dir || -L $output_dir ]]; then
  echo "output directory must not exist" >&2
  exit 1
fi

archive_name="slack-agent-cli-$version.tar.gz"
manifest_name=release-manifest.json
checksum_name=SHA256SUMS
temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/slack-agent-cli-release.XXXXXX")
staging_dir=$(mktemp -d "$output_parent/.$output_name.staging.XXXXXX")
cleanup() {
  rm -rf "$temporary_dir"
  if [[ -n ${staging_dir:-} && -d $staging_dir ]]; then
    rm -rf "$staging_dir"
  fi
}
trap cleanup EXIT

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

for attempt in one two; do
  git archive --format=tar --prefix="slack-agent-cli-$version/" "$commit" |
    gzip -n -9 >"$temporary_dir/$archive_name.$attempt"
done

first_digest=$(sha256_file "$temporary_dir/$archive_name.one")
second_digest=$(sha256_file "$temporary_dir/$archive_name.two")
if [[ $first_digest != "$second_digest" ]]; then
  echo "source archive generation is not deterministic" >&2
  exit 1
fi
mv "$temporary_dir/$archive_name.one" "$staging_dir/$archive_name"

archive_entries="$temporary_dir/archive-entries.txt"
tar -tzf "$staging_dir/$archive_name" >"$archive_entries"
if [[ ! -s $archive_entries ]] ||
  grep -Ev "^slack-agent-cli-$version/([^/].*)?$" "$archive_entries" | grep -q .; then
  echo "source archive contains an invalid root layout" >&2
  exit 1
fi
for required in .gitattributes go.mod go.sum LICENSE cmd/slack-agent-cli/main.go internal/cli/archive.go assets/skills/slack/SKILL.md; do
  if ! grep -Fxq "slack-agent-cli-$version/$required" "$archive_entries"; then
    echo "source archive is missing $required" >&2
    exit 1
  fi
done
if grep -E '(^|/)\.git(/|$)|(^|/)\.env($|\.)|(^|/)\.DS_Store$|(^|/)outputs(/|$)|(^|/)work(/|$)' "$archive_entries" |
  grep -q .; then
  echo "source archive contains forbidden local content" >&2
  exit 1
fi

archive_size=$(wc -c <"$staging_dir/$archive_name" | tr -d ' ')
jq_arguments=(
  --arg tag "$tag"
  --arg version "$version"
  --arg tag_object_sha "$tag_object_sha"
  --arg commit_sha "$commit"
  --arg source_name "$archive_name"
  --arg source_sha256 "$first_digest"
  --argjson source_size "$archive_size"
)
jq -n -S "${jq_arguments[@]}" '{
    schema: 1,
    tag: $tag,
    version: $version,
    tag_object_sha: $tag_object_sha,
    commit_sha: $commit_sha,
    source: {
      name: $source_name,
      sha256: $source_sha256,
      size: $source_size
    }
  }' >"$staging_dir/$manifest_name"

(
  cd "$staging_dir"
  for asset in "$archive_name" "$manifest_name"; do
    printf '%s  %s\n' "$(sha256_file "$asset")" "$asset"
  done | LC_ALL=C sort -k2 >"$checksum_name"
)

actual_assets=$(find "$staging_dir" -mindepth 1 -maxdepth 1 -type f -print |
  wc -l | tr -d ' ')
if [[ $actual_assets != 3 ]]; then
  echo "release bundle has an unexpected asset count" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$staging_dir" && sha256sum -c "$checksum_name")
else
  (cd "$staging_dir" && shasum -a 256 -c "$checksum_name")
fi

if [[ -e $output_dir || -L $output_dir ]]; then
  echo "output directory appeared during bundle generation" >&2
  exit 1
fi
mv -n "$staging_dir" "$output_dir"
if [[ -e $staging_dir || ! -d $output_dir ]]; then
  echo "atomic release bundle publication failed" >&2
  exit 1
fi
staging_dir=
