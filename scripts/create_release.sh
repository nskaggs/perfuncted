#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 [-b branch] [--replace] [version]

Create a GitHub release from HEAD of a branch (default: main) and attach a static pf binary.

Options:
  -b, --branch BRANCH   Branch to take commit from (default: main)
  -v, --version VERSION Release tag (positional or -v/--version). If omitted, the script only builds the binary.
  --replace             Replace existing release with same tag
  -h, --help            Show this help

Examples:
  $0 -b main v1.2.3
  $0 --version v1.2.3
EOF
}

branch=main
version=""
replace=false

# Parse args
while [[ $# -gt 0 ]]; do
  case "$1" in
    -b|--branch)
      branch="$2"; shift 2;;
    --replace)
      replace=true; shift;;
    -v|--version)
      version="$2"; shift 2;;
    -h|--help)
      usage; exit 0;;
    --)
      shift; break;;
    -*)
      echo "Unknown option: $1" >&2; usage; exit 1;;
    *)
      if [[ -z "$version" ]]; then version="$1"; shift; else echo "Extra positional arg: $1" >&2; usage; exit 1; fi;;
  esac
done

# Repo root
repo_root=$(git rev-parse --show-toplevel 2>/dev/null || true)
if [[ -z "${repo_root}" ]]; then
  echo "Not inside a git repository." >&2
  exit 1
fi

# Dependencies
if ! command -v gh >/dev/null 2>&1; then
  echo "gh CLI not found; please install and authenticate (gh auth login)." >&2
  exit 1
fi

# Ensure branch is up to date
git -C "$repo_root" fetch origin "$branch" --quiet || git -C "$repo_root" fetch --all --quiet

# Resolve commit (prefer origin/branch, then branch, then HEAD)
if commit=$(git -C "$repo_root" rev-parse --verify "origin/${branch}" 2>/dev/null); then
  :
elif commit=$(git -C "$repo_root" rev-parse --verify "${branch}" 2>/dev/null); then
  :
else
  commit=$(git -C "$repo_root" rev-parse --verify HEAD)
fi

echo "Using commit $commit (branch: $branch)"

# Build in a detached worktree to avoid modifying the current checkout
tmpdir=$(mktemp -d)
cleanup() {
  set +e
  git -C "$repo_root" worktree remove --force "$tmpdir" 2>/dev/null || true
  rm -rf "$tmpdir"
}
trap cleanup EXIT

git -C "$repo_root" worktree add --detach "$tmpdir" "$commit"

# Build metadata
timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
built_by="release-script"

ldflags="-s -w -X main.commit=${commit} -X main.date=${timestamp} -X main.builtBy=${built_by}"
if [[ -n "$version" ]]; then
  ldflags+=" -X main.version=${version}"
fi

outdir="$repo_root/dist/${version:-local}_linux_amd64"
mkdir -p "$outdir"

echo "Building static linux/amd64 binary..."
(
  cd "$tmpdir"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$ldflags" -o "${outdir}/pf" ./cmd/pf
)

echo "Built: ${outdir}/pf"

if [[ -z "$version" ]]; then
  echo "No version specified; build complete. Binary at ${outdir}/pf"
  exit 0
fi

# Create or replace GitHub release
if gh release view "$version" >/dev/null 2>&1; then
  if [[ "$replace" == "true" ]]; then
    echo "Existing release ${version} found; deleting (replace mode)..."
    gh release delete --yes "$version"
  else
    echo "Release ${version} already exists. Use --replace to overwrite." >&2
    exit 1
  fi
fi

echo "Creating GitHub release ${version} targeted to ${commit} and uploading ${outdir}/pf..."
# Create release and upload asset
if gh release create "$version" "${outdir}/pf" --target "$commit" --title "$version" --notes "Release ${version} built from ${commit}"; then
  echo "Release created: $(gh release view "$version" --json url -q .url)"
  exit 0
else
  echo "gh release create failed" >&2
  exit 1
fi
