#!/usr/bin/env bash
# Bump the one version the operator has.
#
#   ./hack/bump.sh                 show the current version
#   ./hack/bump.sh patch|minor|major
#   ./hack/bump.sh 0.4.1           set it explicitly
#   ./hack/bump.sh minor --tag     and commit and create an annotated tag v<X>
#
# Two hand-kept places hold it: dist/chart/Chart.yaml (version, appVersion) and
# IMG in the Makefile. Everything else that carries it -- the image tag in
# config/manager/kustomization.yaml, dist/install.yaml, manager.image.tag in
# dist/chart/values.yaml -- is generated from IMG, so this regenerates them
# rather than editing them.
set -euo pipefail
cd "$(dirname "$0")/.."

chart=dist/chart/Chart.yaml
current="$(yq -r '.appVersion' "$chart")"

if [ $# -eq 0 ]; then
  echo "$current"
  exit 0
fi

next=""
case "$1" in
  major|minor|patch)
    IFS=. read -r a b c <<< "$current"
    case "$1" in
      major) next="$((a + 1)).0.0" ;;
      minor) next="${a}.$((b + 1)).0" ;;
      patch) next="${a}.${b}.$((c + 1))" ;;
    esac
    ;;
  [0-9]*) next="$1" ;;
  *) echo "usage: $0 [major|minor|patch|X.Y.Z] [--tag]" >&2; exit 2 ;;
esac

if ! [[ "$next" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "not a semver: $next" >&2
  exit 2
fi

tag=false
[ "${2:-}" = "--tag" ] && tag=true
if $tag && [ -n "$(git status --porcelain)" ]; then
  echo "working tree is dirty; commit before tagging" >&2
  exit 1
fi

# v4.15's helm plugin renders a broken manager template for the /tmp volume.
kb="$(kubebuilder version | sed -n 's/^KubeBuilder: *v//p')"
if [ "$(printf '%s\n' 4.16.0 "$kb" | sort -V | head -1)" != 4.16.0 ]; then
  echo "kubebuilder $kb is too old; need v4.16.0 or later" >&2
  exit 1
fi

# Line-targeted rather than `yq -i`, which rewrites the whole document and
# drops comments.
tmp="$(mktemp)"
sed -e "s/^version: .*/version: $next/" \
    -e "s/^appVersion: .*/appVersion: \"$next\"/" \
    "$chart" > "$tmp"
mv "$tmp" "$chart"

# Keep the repository, which may itself contain a ':' (registry:port).
sed -E "s#^(IMG \?= .*):[^:/]+\$#\1:$next#" Makefile > "$tmp"
mv "$tmp" Makefile

# --force is what refreshes manager.image.tag in values.yaml, but it also
# rewrites the scaffolded test-chart workflow and drops the actions.lock pins.
wf=.github/workflows/test-chart.yml
cp "$wf" "$tmp"

# An exported IMG would outrank the Makefile default and stamp the wrong tag.
unset IMG
out="$(kubebuilder edit --plugins=helm/v2-alpha --force 2>&1)" && ok=true || ok=false
mv "$tmp" "$wf"
if ! $ok; then
  echo "$out" >&2
  echo "regeneration failed" >&2
  exit 1
fi

for got in \
  "$(yq -r '.appVersion' "$chart")" \
  "$(yq -r '.manager.image.tag' dist/chart/values.yaml)" \
  "$(yq -r '.images[] | select(.name == "controller") | .newTag' config/manager/kustomization.yaml)"; do
  if [ "$got" != "$next" ]; then
    echo "bump did not take: found $got, want $next; check $chart, Makefile, dist/chart/values.yaml and config/manager/kustomization.yaml" >&2
    exit 1
  fi
done

helm lint --strict dist/chart >/dev/null

echo "$current -> $next"

if $tag; then
  git add Makefile config/manager/kustomization.yaml dist
  git commit -m "chore(release): v$next"
  git tag -a "v$next" -m "llm-operator v$next"
  echo "tagged v$next"
  if [ -n "$(git status --porcelain)" ]; then
    echo "regeneration also changed files outside the bump; review them:" >&2
    git status --short >&2
  fi
fi

cat <<NEXT

publish: push the tag; CI builds 4pdosc/llm-operator:$next (and on GHCR) and
oci://ghcr.io/modelsphere/charts/llmscaleoperator:$next. Then:
  helm upgrade --install llmscaleoperator oci://ghcr.io/modelsphere/charts/llmscaleoperator --version $next -n llmscaleoperator-system
NEXT
