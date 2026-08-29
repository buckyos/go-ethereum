#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORKSPACE_ROOT=${USDB_E2E_WORKSPACE_ROOT:-$(dirname "$ROOT_DIR")}
USDB_REPO=${USDB_REPO:-"$WORKSPACE_ROOT/usdb"}
SOURCE_DAO_REPO=${SOURCE_DAO_REPO:-"$WORKSPACE_ROOT/SourceDAO"}
WORK_DIR=${USDB_E2E_LOCAL_IMAGE_WORK_DIR:-/tmp/usdb-three-node-local-images}
REGISTRY=${USDB_E2E_LOCAL_REGISTRY:-127.0.0.1:5000}
REGISTRY_CONTAINER=${USDB_E2E_LOCAL_REGISTRY_CONTAINER:-usdb-e2e-registry}
REGISTRY_IMAGE=${USDB_E2E_LOCAL_REGISTRY_IMAGE:-registry:2}
RELEASE_ID=${USDB_E2E_RELEASE_ID:-usdb-testnet-v0-r999999}
PLATFORM=${USDB_E2E_PLATFORM:-linux/amd64}
TOOL="$ROOT_DIR/scripts/usdb/release_three_node_e2e.py"

usage() {
  cat <<EOF
Usage: scripts/usdb/prepare_local_release_images.sh <action>

Actions:
  build   Export committed sources, build/push three images, and create a local candidate.
  status  Show the local registry and the generated execution plan.
  stop    Stop only the temporary local registry container.

Environment:
  USDB_E2E_RELEASE_ID             Candidate identity; defaults to ${RELEASE_ID}.
  USDB_E2E_LOCAL_REGISTRY         Registry host:port; defaults to ${REGISTRY}.
  USDB_E2E_LOCAL_IMAGE_WORK_DIR   Output root; defaults to ${WORK_DIR}.

The generated release manifest remains production-shaped and contains canonical
GHCR references. execution-plan.json maps those references to the local registry
without changing their OCI digest. This artifact is test-only and has no CI
provenance or release approval.
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Required command is unavailable: $1" >&2
    exit 1
  }
}

require_repo() {
  local repo=$1
  local label=$2
  if [[ ! -d "$repo/.git" ]]; then
    echo "Missing ${label} checkout: $repo" >&2
    exit 1
  fi
}

export_revision() {
  local repo=$1
  local revision=$2
  local output=$3
  mkdir -p "$output"
  git -C "$repo" archive --format=tar "$revision" | tar -xf - -C "$output"
}

ensure_registry() {
  if docker container inspect "$REGISTRY_CONTAINER" >/dev/null 2>&1; then
    if [[ "$(docker inspect --format '{{.State.Running}}' "$REGISTRY_CONTAINER")" != "true" ]]; then
      docker start "$REGISTRY_CONTAINER" >/dev/null
    fi
    return
  fi
  local port=${REGISTRY##*:}
  if [[ ! "$port" =~ ^[1-9][0-9]{0,4}$ ]]; then
    echo "USDB_E2E_LOCAL_REGISTRY must include a numeric port: $REGISTRY" >&2
    exit 1
  fi
  docker run -d --name "$REGISTRY_CONTAINER" -p "127.0.0.1:${port}:5000" "$REGISTRY_IMAGE" >/dev/null
}

build_image() {
  local context=$1
  local dockerfile=$2
  local tag=$3
  local revision=$4
  local metadata=$5
  shift 5
  docker buildx build \
    --platform "$PLATFORM" \
    --push \
    --provenance=false \
    --sbom=false \
    --label "org.opencontainers.image.revision=${revision}" \
    --metadata-file "$metadata" \
    -f "$dockerfile" \
    -t "$tag" \
    "$@" \
    "$context"
}

metadata_digest() {
  local path=$1
  local digest
  digest=$(jq -r '."containerimage.digest" // empty' "$path")
  if [[ ! "$digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Build metadata has no OCI digest: $path" >&2
    exit 1
  fi
  printf '%s\n' "$digest"
}

build_candidate() {
  require_command docker
  require_command git
  require_command jq
  require_command python3
  require_command tar
  docker buildx version >/dev/null
  require_repo "$ROOT_DIR" go-ethereum
  require_repo "$USDB_REPO" USDB
  require_repo "$SOURCE_DAO_REPO" SourceDAO

  if [[ -e "$WORK_DIR/release-manifest.json" ]]; then
    echo "Local candidate already exists: $WORK_DIR/release-manifest.json" >&2
    echo "Choose a new USDB_E2E_LOCAL_IMAGE_WORK_DIR to preserve the previous run." >&2
    exit 1
  fi

  local go_revision usdb_revision source_dao_revision created_at
  go_revision=$(git -C "$ROOT_DIR" rev-parse HEAD)
  usdb_revision=$(git -C "$USDB_REPO" rev-parse HEAD)
  source_dao_revision=$(git -C "$SOURCE_DAO_REPO" rev-parse HEAD)
  created_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  mkdir -p "$WORK_DIR/contexts" "$WORK_DIR/metadata"
  echo "Exporting committed source revisions"
  export_revision "$ROOT_DIR" "$go_revision" "$WORK_DIR/contexts/go-ethereum"
  export_revision "$USDB_REPO" "$usdb_revision" "$WORK_DIR/contexts/usdb"
  export_revision "$SOURCE_DAO_REPO" "$source_dao_revision" "$WORK_DIR/contexts/SourceDAO"
  ensure_registry

  local chain_tag services_tag bitcoin_tag
  chain_tag="$REGISTRY/buckyos/usdb-chain:git-$go_revision"
  services_tag="$REGISTRY/buckyos/usdb-services:git-$usdb_revision"
  bitcoin_tag="$REGISTRY/buckyos/usdb-bitcoin-core:git-$usdb_revision"

  echo "Building USDB chain image from $go_revision"
  build_image \
    "$WORK_DIR/contexts/go-ethereum" \
    "$WORK_DIR/contexts/go-ethereum/Dockerfile" \
    "$chain_tag" \
    "$go_revision" \
    "$WORK_DIR/metadata/usdb-chain.json" \
    --build-arg "COMMIT=$go_revision" \
    --build-arg "VERSION=local-$go_revision" \
    --build-arg "BUILDNUM=three-node-e2e"

  echo "Building USDB services image from $usdb_revision"
  build_image \
    "$WORK_DIR/contexts/usdb" \
    "$WORK_DIR/contexts/usdb/docker/Dockerfile.usdb-services" \
    "$services_tag" \
    "$usdb_revision" \
    "$WORK_DIR/metadata/usdb-services.json"

  echo "Building Bitcoin Core image from $usdb_revision"
  build_image \
    "$WORK_DIR/contexts/usdb" \
    "$WORK_DIR/contexts/usdb/docker/Dockerfile.bitcoin-core" \
    "$bitcoin_tag" \
    "$usdb_revision" \
    "$WORK_DIR/metadata/bitcoin-core.json"

  local chain_digest services_digest bitcoin_digest local_lock manifest plan
  chain_digest=$(metadata_digest "$WORK_DIR/metadata/usdb-chain.json")
  services_digest=$(metadata_digest "$WORK_DIR/metadata/usdb-services.json")
  bitcoin_digest=$(metadata_digest "$WORK_DIR/metadata/bitcoin-core.json")
  local_lock="$WORK_DIR/local-ci-revisions.json"
  manifest="$WORK_DIR/release-manifest.json"
  plan="$WORK_DIR/execution-plan.json"

  python3 "$TOOL" write-local-lock \
    --base "$WORK_DIR/contexts/go-ethereum/scripts/usdb/ci-revisions.json" \
    --output "$local_lock" \
    --usdb-revision "$usdb_revision" \
    --source-dao-revision "$source_dao_revision"

  python3 "$WORK_DIR/contexts/usdb/docker/scripts/tools/release_manifest.py" create \
    --bundle-dir "$WORK_DIR/contexts/usdb/docker/networks/testnet-v0" \
    --output "$manifest" \
    --release-id "$RELEASE_ID" \
    --created-at-utc "$created_at" \
    --compatibility-lock "$local_lock" \
    --usdb-revision "$usdb_revision" \
    --go-ethereum-revision "$go_revision" \
    --source-dao-revision "$source_dao_revision" \
    --services-image "ghcr.io/buckyos/usdb-services@${services_digest}" \
    --chain-image "ghcr.io/buckyos/usdb-chain@${chain_digest}" \
    --bitcoin-image "ghcr.io/buckyos/usdb-bitcoin-core@${bitcoin_digest}"

  python3 "$TOOL" plan \
    --manifest "$manifest" \
    --image-mirror "$REGISTRY" \
    --output "$plan"

  echo "Local release candidate is ready"
  echo "  manifest:           $manifest"
  echo "  compatibility lock: $local_lock"
  echo "  execution plan:     $plan"
  echo "  clean USDB bundle:  $WORK_DIR/contexts/usdb/docker/networks/testnet-v0"
}

case "${1:-}" in
  build)
    build_candidate
    ;;
  status)
    docker ps --filter "name=^/${REGISTRY_CONTAINER}$"
    if [[ -f "$WORK_DIR/execution-plan.json" ]]; then
      jq . "$WORK_DIR/execution-plan.json"
    fi
    ;;
  stop)
    if docker container inspect "$REGISTRY_CONTAINER" >/dev/null 2>&1; then
      docker stop "$REGISTRY_CONTAINER" >/dev/null
      echo "Stopped local E2E registry: $REGISTRY_CONTAINER"
    fi
    ;;
  help|--help|-h|"")
    usage
    ;;
  *)
    echo "Unknown action: $1" >&2
    usage >&2
    exit 1
    ;;
esac
