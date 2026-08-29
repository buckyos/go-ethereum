#!/usr/bin/env python3
"""Prepare and verify digest-pinned inputs for the three-node release E2E."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import tempfile
from pathlib import Path
from typing import Any


SCHEMA_VERSION = "usdb-release-manifest:v3"
IMAGE_SPECS = {
    "usdb_services": {
        "canonical_name": "ghcr.io/buckyos/usdb-services",
        "env_key": "USDB_SERVICES_IMAGE",
        "source": "usdb",
    },
    "usdb_chain": {
        "canonical_name": "ghcr.io/buckyos/usdb-chain",
        "env_key": "USDB_CHAIN_IMAGE",
        "source": "go_ethereum",
    },
    "bitcoin_core": {
        "canonical_name": "ghcr.io/buckyos/usdb-bitcoin-core",
        "env_key": "USDB_BITCOIN_IMAGE",
        "source": "usdb",
    },
}
REVISION_RE = re.compile(r"^[0-9a-f]{40}$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
MIRROR_RE = re.compile(
    r"^(?:localhost|127\.0\.0\.1|[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?)(?::[1-9][0-9]{0,4})?$"
)


def strict_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate JSON key: {key}")
        value[key] = item
    return value


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(
            path.read_text(encoding="utf-8"), object_pairs_hook=strict_object
        )
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        raise ValueError(f"failed to load strict JSON {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object in {path}")
    return value


def load_env(path: Path) -> dict[str, str]:
    result: dict[str, str] = {}
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise ValueError(f"failed to read env file {path}: {exc}") from exc
    for line_number, raw in enumerate(lines, start=1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise ValueError(f"invalid env line {path}:{line_number}")
        key, value = line.split("=", 1)
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
            raise ValueError(f"invalid env key {key!r} at {path}:{line_number}")
        if key in result:
            raise ValueError(f"duplicate env key {key!r} in {path}")
        result[key] = value
    return result


def write_json(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=path.parent, prefix=f".{path.name}.", delete=False
    ) as output:
        temporary = Path(output.name)
        json.dump(value, output, indent=2, sort_keys=True)
        output.write("\n")
        output.flush()
        os.fsync(output.fileno())
    os.replace(temporary, path)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ValueError(message)


def parse_reference(value: Any, canonical_name: str, context: str) -> tuple[str, str]:
    require(isinstance(value, str), f"{context} must be a string")
    prefix = f"{canonical_name}@"
    require(value.startswith(prefix), f"{context} must use {canonical_name}@sha256:<digest>")
    digest = value[len(prefix) :]
    require(DIGEST_RE.fullmatch(digest) is not None, f"{context} has an invalid digest")
    return value, digest


def mirror_reference(canonical_reference: str, mirror: str | None) -> str:
    if not mirror:
        return canonical_reference
    require(MIRROR_RE.fullmatch(mirror) is not None, "image mirror must be a registry host with optional port")
    repository_and_digest = canonical_reference.removeprefix("ghcr.io/")
    require(repository_and_digest != canonical_reference, "only canonical GHCR references may be mirrored")
    return f"{mirror}/{repository_and_digest}"


def build_plan(manifest_path: Path, mirror: str | None) -> dict[str, Any]:
    manifest = load_json(manifest_path)
    require(manifest.get("schema_version") == SCHEMA_VERSION, "unsupported release manifest schema")
    require(manifest.get("stage") == "candidate", "three-node E2E requires a candidate manifest")
    release_id = manifest.get("release_id")
    require(isinstance(release_id, str) and release_id, "release manifest ID is required")

    repositories = manifest.get("repositories")
    require(isinstance(repositories, dict), "release manifest repositories are required")
    images = manifest.get("images")
    require(isinstance(images, dict) and set(images) == set(IMAGE_SPECS), "release manifest image set mismatch")

    planned_images: dict[str, Any] = {}
    for key, spec in IMAGE_SPECS.items():
        entry = images[key]
        require(isinstance(entry, dict), f"images.{key} must be an object")
        source_key = spec["source"]
        source = repositories.get(source_key)
        require(isinstance(source, dict), f"repositories.{source_key} is required")
        revision = source.get("revision")
        require(
            isinstance(revision, str) and REVISION_RE.fullmatch(revision) is not None,
            f"repositories.{source_key}.revision must be a full Git SHA",
        )
        require(entry.get("source_revision") == revision, f"images.{key} source revision mismatch")
        canonical, digest = parse_reference(
            entry.get("reference"), spec["canonical_name"], f"images.{key}.reference"
        )
        planned_images[key] = {
            "canonical_reference": canonical,
            "execution_reference": mirror_reference(canonical, mirror),
            "digest": digest,
            "env_key": spec["env_key"],
            "source_revision": revision,
        }

    network = manifest.get("network_bundle")
    require(isinstance(network, dict), "release manifest network bundle is required")
    chain_id = network.get("chain_id")
    network_id = network.get("network_id")
    genesis_hash = network.get("genesis_block_hash")
    require(isinstance(chain_id, int) and chain_id > 0, "network bundle chain ID is invalid")
    require(isinstance(network_id, int) and network_id > 0, "network bundle network ID is invalid")
    require(
        isinstance(genesis_hash, str) and re.fullmatch(r"0x[0-9a-f]{64}", genesis_hash) is not None,
        "network bundle genesis block hash is invalid",
    )
    return {
        "schema_version": "usdb-three-node-release-e2e-plan:v1",
        "release_id": release_id,
        "manifest_path": str(manifest_path.resolve()),
        "manifest_sha256": sha256(manifest_path),
        "image_mirror": mirror or "",
        "network": {
            "bundle_id": network.get("bundle_id"),
            "chain_id": chain_id,
            "network_id": network_id,
            "genesis_block_hash": genesis_hash,
        },
        "images": planned_images,
    }


def validate_node_env(plan: dict[str, Any], node_env_path: Path) -> None:
    env = load_env(node_env_path)
    for image in plan["images"].values():
        key = image["env_key"]
        require(env.get(key) == image["canonical_reference"], f"{key} does not match the release manifest")


def render_node_env(
    plan: dict[str, Any], base_path: Path, output_path: Path, node_index: int, role: str
) -> None:
    require(node_index in {1, 2, 3}, "node index must be 1, 2, or 3")
    require(role in {"bootnode", "full", "miner"}, "unsupported node role")
    env = load_env(base_path)
    for image in plan["images"].values():
        env[image["env_key"]] = image["canonical_reference"]

    offset = node_index - 1
    env.update(
        {
            "USDB_NODE_ROLE": role,
            "USDB_BOOTNODES": "",
            "USDB_NAT": "",
            "USDB_HTTP_BIND_ADDRESS": "127.0.0.1",
            "USDB_HTTP_BIND_PORT": str(18545 + offset * 10000),
            "USDB_WS_BIND_ADDRESS": "127.0.0.1",
            "USDB_WS_BIND_PORT": str(18546 + offset * 10000),
            "USDB_P2P_BIND_ADDRESS": "127.0.0.1",
            "USDB_P2P_BIND_PORT": str(31303 + offset),
            "BTC_P2P_BIND_ADDRESS": "127.0.0.1",
            "BTC_P2P_BIND_PORT": str(38333 + offset),
            "BH_BIND_ADDRESS": "127.0.0.1",
            "BH_BIND_PORT": str(28110 + offset * 100),
            "USDB_INDEXER_BIND_ADDRESS": "127.0.0.1",
            "USDB_INDEXER_BIND_PORT": str(28120 + offset * 100),
            "CONTROL_PLANE_BIND_ADDRESS": "127.0.0.1",
            "CONTROL_PLANE_BIND_PORT": str(28140 + offset * 100),
        }
    )
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        "w", encoding="utf-8", dir=output_path.parent, prefix=f".{output_path.name}.", delete=False
    ) as output:
        temporary = Path(output.name)
        for key in sorted(env):
            output.write(f"{key}={env[key]}\n")
        output.flush()
        os.fsync(output.fileno())
    os.chmod(temporary, 0o600)
    os.replace(temporary, output_path)


def verify_image_inspect(plan: dict[str, Any], key: str, inspect_path: Path) -> None:
    require(key in IMAGE_SPECS, f"unknown image key: {key}")
    raw = json.loads(inspect_path.read_text(encoding="utf-8"))
    require(isinstance(raw, list) and len(raw) == 1 and isinstance(raw[0], dict), "docker inspect must contain one image")
    inspected = raw[0]
    image = plan["images"][key]
    repo_digests = inspected.get("RepoDigests")
    require(isinstance(repo_digests, list), f"{key} image has no RepoDigests")
    require(
        image["execution_reference"] in repo_digests,
        f"{key} image RepoDigests do not contain the execution reference",
    )
    config = inspected.get("Config")
    require(isinstance(config, dict), f"{key} image Config is missing")
    labels = config.get("Labels") or {}
    require(isinstance(labels, dict), f"{key} image labels are invalid")
    revision = labels.get("org.opencontainers.image.revision") or labels.get("commit")
    require(revision == image["source_revision"], f"{key} image source revision label mismatch")


def write_local_compatibility_lock(
    base_path: Path, output_path: Path, usdb_revision: str, source_dao_revision: str
) -> None:
    require(REVISION_RE.fullmatch(usdb_revision) is not None, "USDB revision must be a full Git SHA")
    require(
        REVISION_RE.fullmatch(source_dao_revision) is not None,
        "SourceDAO revision must be a full Git SHA",
    )
    lock = load_json(base_path)
    require(lock.get("schema_version") == "usdb-ci-revisions:v2", "unsupported compatibility lock schema")
    dependencies = lock.get("dependencies")
    require(isinstance(dependencies, dict), "compatibility lock dependencies are required")
    require(isinstance(dependencies.get("usdb"), dict), "compatibility lock USDB dependency is required")
    require(
        isinstance(dependencies.get("source_dao"), dict),
        "compatibility lock SourceDAO dependency is required",
    )
    dependencies["usdb"]["revision"] = usdb_revision
    dependencies["source_dao"]["revision"] = source_dao_revision
    write_json(output_path, lock)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    plan = subparsers.add_parser("plan", help="write a validated E2E execution plan")
    plan.add_argument("--manifest", type=Path, required=True)
    plan.add_argument("--image-mirror")
    plan.add_argument("--output", type=Path, required=True)

    validate_env = subparsers.add_parser("validate-node-env", help="bind a private node env to the manifest")
    validate_env.add_argument("--plan", type=Path, required=True)
    validate_env.add_argument("--node-env", type=Path, required=True)

    render_env = subparsers.add_parser("render-node-env", help="render one isolated E2E node env")
    render_env.add_argument("--plan", type=Path, required=True)
    render_env.add_argument("--base", type=Path, required=True)
    render_env.add_argument("--output", type=Path, required=True)
    render_env.add_argument("--node-index", type=int, required=True)
    render_env.add_argument("--role", required=True)

    inspect = subparsers.add_parser("verify-image-inspect", help="verify a pulled Docker image")
    inspect.add_argument("--plan", type=Path, required=True)
    inspect.add_argument("--image-key", required=True)
    inspect.add_argument("--inspect", type=Path, required=True)

    local_lock = subparsers.add_parser(
        "write-local-lock", help="write a test-only compatibility lock for clean local revisions"
    )
    local_lock.add_argument("--base", type=Path, required=True)
    local_lock.add_argument("--output", type=Path, required=True)
    local_lock.add_argument("--usdb-revision", required=True)
    local_lock.add_argument("--source-dao-revision", required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.command == "plan":
            write_json(args.output, build_plan(args.manifest, args.image_mirror))
        elif args.command == "validate-node-env":
            validate_node_env(load_json(args.plan), args.node_env)
        elif args.command == "render-node-env":
            render_node_env(
                load_json(args.plan), args.base, args.output, args.node_index, args.role
            )
        elif args.command == "verify-image-inspect":
            verify_image_inspect(load_json(args.plan), args.image_key, args.inspect)
        else:
            write_local_compatibility_lock(
                args.base, args.output, args.usdb_revision, args.source_dao_revision
            )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"three-node release E2E input error: {exc}", file=os.sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
