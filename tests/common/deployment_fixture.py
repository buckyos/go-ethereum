"""Safety and evidence contracts for an explicitly dedicated deployment fixture."""

import json
from datetime import datetime
from pathlib import Path
import socket
from urllib.parse import urlparse


def require(condition, message):
    if not condition:
        raise ValueError(message)


def inside(path, root):
    return Path(path).resolve().is_relative_to(Path(root).resolve())


def read_fixture(path):
    fixture = json.loads(Path(path).read_text())
    require(fixture.get("schema") == "usdb-node-deployment-fixture:v1", "unsupported fixture schema")
    require(fixture.get("dedicated_test_node") is True, "fixture must explicitly designate a dedicated test node")
    require(fixture.get("hostname") == socket.gethostname(), "fixture belongs to another host")
    for key in ("launcher", "kit_root", "node_env", "data_root"):
        value = fixture.get(key, "")
        require(bool(value) and Path(value).is_absolute() and Path(value).exists(), f"missing absolute {key}")
        fixture[key] = str(Path(value).resolve())
    require(fixture["data_root"] not in {"/", "/home", "/data", str(Path.home())}, "data root is too broad")
    require(bool(fixture.get("identity_files")), "at least one persisted node identity is required")
    for name in fixture["identity_files"]:
        require(inside(name, fixture["data_root"]) and Path(name).is_file(), "identity must exist inside fixture data")
    for name in ("balance_history_rpc", "indexer_rpc", "chain_rpc"):
        url = urlparse(fixture.get(name, ""))
        require(url.scheme == "http" and url.hostname in {"127.0.0.1", "::1"} and url.port
                and not url.username and not url.password, f"{name} must be a private loopback RPC")
    return fixture


def verify_mount_isolation(containers, projects, fixture):
    """Refuse overlapping bind mounts belonging to any other Docker project."""
    roots = (fixture["data_root"], str(Path(fixture["node_env"]).parent))
    owned = []
    for container in containers:
        labels = container.get("Config", {}).get("Labels") or {}
        ours = labels.get("com.docker.compose.project") in projects
        for mount in container.get("Mounts", []):
            source = mount.get("Source", "")
            overlap = source and any(inside(source, root) or inside(root, source) for root in roots)
            require(not overlap or ours, "fixture data/config are mounted by another Docker project")
            if ours and mount.get("RW"):
                require(mount.get("Type") == "bind" and any(inside(source, root) for root in roots),
                        "fixture container has writable storage outside declared data/config roots")
        if ours:
            owned.append(container)
    require(owned, "no actual deployment containers found")
    return owned


def validate_recovery(before, after):
    """A fresh RPC port or READY label alone cannot qualify a recovered node."""
    for field in ("chain_id", "genesis_hash", "historical_chain_hash", "balance_ref", "indexer_ref", "epoch", "identities"):
        require(before[field] == after[field], f"restart changed {field}")
    require(after["stable_height"] > before["stable_height"], "no new confirmed BTC work after restart")
    require(after["chain_height"] > before["chain_height"], "no new executed USDB block after restart")
    require(before["containers"].keys() == after["containers"].keys(), "service set changed during restart")
    require(all(before["containers"][s] != after["containers"][s] for s in before["containers"]),
            "down/up did not recreate every running service")


def validate_progress(progress, restart_started):
    """Require all core components and a new, successful observation after up."""
    require(datetime.fromisoformat(progress["observed_at"].replace("Z", "+00:00")).timestamp() >= restart_started,
            "progress predates this restart")
    components = {c["id"]: c for c in progress["components"]}
    for name in ("bitcoin", "balance_history", "usdb_indexer", "usdb_chain"):
        require(name in components, f"progress is missing {name}")
        item = components[name]
        require(item["state"] == "READY" and not item.get("last_observed_at") and not item.get("observation_unavailable"),
                f"{name} is not freshly ready")
    require(progress.get("controller_state") in {"idle", "running", "active"}, "controller did not recover")
