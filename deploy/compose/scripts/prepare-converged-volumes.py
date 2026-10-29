#!/usr/bin/env python3
"""Prepare named converged volumes without starting/stopping any project.

Seed empty stores once; preserve populated config and compare other material.
--restore-credentials restores missing host seed files from populated stores.
--refresh ROLE requires stopped consumers and keeps a protected host backup.
No volume is deleted. Run one provisioning operation at a time.
"""
import argparse
import datetime
import json
import os
import pathlib
import shlex
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
CREDENTIAL_ROLES = ("gateway-token", "aigw-password", "mgmt-password")
REFRESH_ROLES = ("gateway-tls", "instance-ca", "edge-tls", *CREDENTIAL_ROLES)


def command(args, allow_fail=False):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode and not allow_fail:
        raise RuntimeError(
            "Command failed: " + " ".join(args[:2])
            + " (details withheld to protect credentials)"
        )
    return result


def model(filename, envfile):
    prefix = [
        "docker", "compose", "--project-directory", str(ROOT),
        "--env-file", str(envfile), "-f", str(ROOT / filename), "config",
    ]
    env = command(prefix + ["--environment"]).stdout.decode()
    values = dict(line.split("=", 1) for line in env.splitlines() if "=" in line)
    return json.loads(command(prefix + ["--format", "json"]).stdout), values


def source_path(value):
    value = pathlib.Path(value)
    return value if value.is_absolute() else ROOT / value


def validate_volume_names(names):
    if len(names) != len(set(names)):
        raise RuntimeError("Every persistence role needs a distinct volume name")


def validate_material(role, source, filename):
    if source.is_symlink() or (
        source.is_dir() and any(item.is_symlink() for item in source.rglob("*"))
    ):
        raise RuntimeError(role + ": symlink seed refused")
    if filename:
        if not source.is_file() or not source.stat().st_size:
            raise RuntimeError(role + ": protected source file missing or empty")
        if role == "instance-ca" and "PRIVATE KEY" in source.read_text(errors="ignore"):
            raise RuntimeError("instance-ca: only public trust anchors may be provisioned")
        return
    if source.exists() and not source.is_dir():
        raise RuntimeError(role + ": source must be a directory")
    if role not in ("gateway-tls", "edge-tls"):
        return
    pair = ("server.crt", "server.key") if role == "gateway-tls" else ("cert.pem", "key.pem")
    present = [(source / name).is_file() and (source / name).stat().st_size > 0 for name in pair]
    if (role == "gateway-tls" or any((source / name).exists() for name in pair)) and not all(present):
        raise RuntimeError(role + ": complete nonempty certificate/key pair required")
    for item in source.rglob("*"):
        # Only the root leaf key is allowed, not another server.key in a subtree.
        if item.is_file() and item != source / pair[1]:
            if "PRIVATE KEY" in item.read_text(errors="ignore"):
                raise RuntimeError(role + ": CA signing keys must stay outside service volumes")


def comparison_script(source, filename):
    if filename:
        files = [("/seed/" + source.name, "/persist/" + filename)]
    else:
        # Include hidden and nested files; do not silently skip a changed CA.
        files = [
            ("/seed/" + item.relative_to(source).as_posix(),
             "/persist/" + item.relative_to(source).as_posix())
            for item in sorted(source.rglob("*")) if item.is_file()
        ]
    return "; ".join(
        "test ! -L " + shlex.quote(target) + "; cmp -s "
        + shlex.quote(seed) + " " + shlex.quote(target)
        for seed, target in files
    ) or ":"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--env-file", type=pathlib.Path, default=ROOT / ".env")
    parser.add_argument("--restore-credentials", action="store_true")
    parser.add_argument("--refresh", choices=REFRESH_ROLES)
    args = parser.parse_args()
    if args.restore_credentials and args.refresh:
        parser.error("--restore-credentials and --refresh are separate operations")
    os.umask(0o077)
    data, env = model("docker-compose.dataplane.yml", args.env_file)
    state, _ = model("docker-compose.database.yml", args.env_file)
    gw = env.get("GW_HOST", "")
    if not gw or "/" in gw or ".." in gw:
        raise RuntimeError("GW_HOST must be a certificate SAN, not a path")
    roles = {
        "config": (data["volumes"]["gateway_config"]["name"], source_path(env.get("IGW_CONFIG_DIR", "/opt/loxilb/config")), None),
        "gateway-tls": (data["volumes"]["gateway_tls"]["name"], source_path(env.get("IGW_TLS_SEED_DIR", "certs/instance-ca/" + gw)), None),
        "gateway-token": (data["volumes"]["gateway_token"]["name"], source_path(env.get("GATEWAY_SERVICE_TOKEN_FILE", "secrets/gateway_service_token")), "gateway_service_token"),
        "aigw-password": (data["volumes"]["aigw_password"]["name"], source_path(env.get("AIGW_DB_PASSWORD_FILE", "secrets/aigw_db_password")), "aigw_db_password"),
        "mgmt-password": (state["volumes"]["mgmt_password"]["name"], source_path(env.get("AIGW_MGMT_DB_PASSWORD_FILE", "secrets/aigw_mgmt_db_password")), "aigw_mgmt_db_password"),
        "instance-ca": (env.get("OAM_INSTANCE_CA_VOLUME", "loxilb-oam-instance-ca"), source_path(env.get("OAM_INSTANCE_CA_SOURCE", "certs/instance-ca.pem")), "instance-ca.pem"),
        "edge-tls": (env.get("EDGE_TLS_VOLUME", "loxilb-edge-tls"), source_path(env.get("EDGE_TLS_SEED_DIR", "certs/edge")), None),
    }
    names = [data["volumes"]["gateway_audit"]["name"], state["volumes"]["postgres_data"]["name"], *(value[0] for value in roles.values())]
    validate_volume_names(names)
    image = env.get("VOLUME_INIT_IMAGE") or data["services"]["gateway"]["image"]

    def exists(name):
        return command(["docker", "volume", "inspect", name], True).returncode == 0

    def helper(name, script, extra=(), readonly=False):
        mount = "type=volume,source=" + name + ",target=/persist,volume-nocopy"
        if readonly:
            mount += ",readonly"
        # Root-only credentials; no network or privileged helper is needed.
        return command([
            "docker", "run", "--rm", "--network", "none", "--user", "0:0",
            "--mount", mount, *extra, "--entrypoint", "/bin/sh", image, "-ec", script,
        ])

    def populated(name):
        return helper(name, "if find /persist -mindepth 1 -print -quit | read item; then printf populated; fi", readonly=True).stdout == b"populated"

    def running_containers():
        ids = command(["docker", "ps", "-q"]).stdout.decode().split()
        return json.loads(command(["docker", "inspect", *ids]).stdout) if ids else []

    if args.restore_credentials:
        for role in CREDENTIAL_ROLES:
            name, source, filename = roles[role]
            if not exists(name):
                continue
            # A prior preparation failure can leave empty volumes. They have
            # no authoritative credential yet; host preparation may continue.
            if not populated(name):
                print(role + ": empty store; host seed preparation required")
                continue
            if source.is_symlink():
                raise RuntimeError(role + ": symlink seed refused")
            contents = helper(name, "test -s /persist/" + filename + "; test ! -L /persist/" + filename + "; cat /persist/" + filename, readonly=True).stdout
            if source.exists():
                if source.read_bytes() != contents:
                    raise RuntimeError(role + ": host seed differs from persistent volume; preserve both and resolve explicitly")
            else:
                source.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                with source.open("xb") as output:
                    output.write(contents)
                source.chmod(0o600)
            print(role + ": credential preserved (value not displayed)")
        return

    for name in sorted(names):
        if not exists(name):
            command(["docker", "volume", "create", "--label", "netlox.persistence=converged", name])
    for role, (name, source, filename) in roles.items():
        if args.refresh and args.refresh != role:
            continue
        has_material = populated(name)
        if role == "config" and has_material:
            print("config: existing authoritative volume preserved")
            continue
        validate_material(role, source, filename)
        if not filename and not source.exists():
            source.mkdir(mode=0o700, parents=True)
        mount = ["--mount", "type=bind,source=" + str(source if source.is_dir() else source.parent) + ",target=/seed,readonly"]
        if has_material and not args.refresh:
            helper(name, comparison_script(source, filename), mount, readonly=True)
            print(role + ": existing material verified")
            continue
        running = running_containers()
        if any(any(item.get("Name") == name for item in container["Mounts"]) for container in running):
            raise RuntimeError(role + ": stop its volume consumers before provisioning/refresh")
        if role == "config" and any(
            any(item.get("Type") == "bind" and pathlib.Path(item["Source"]).resolve() == source.resolve() for item in container["Mounts"])
            for container in running
        ):
            raise RuntimeError("config: stop the source bind-mount consumer before adopting live state")
        if args.refresh:
            timestamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
            backup = ROOT / "backups" / "persistence" / timestamp / role
            backup.mkdir(mode=0o700, parents=True)
            helper(name, "cp -a /persist/. /backup/", ["--mount", "type=bind,source=" + str(backup) + ",target=/backup"], readonly=True)
        if filename:
            script = "cp " + shlex.quote("/seed/" + source.name) + " /persist/" + filename + "; chmod 600 /persist/" + filename + "; chmod 700 /persist"
        else:
            script = "cp -a /seed/. /persist/; chown -R 0:0 /persist; find /persist -type d -exec chmod 700 {} +; find /persist -type f -exec chmod 600 {} +"
        helper(name, script, mount)
        print(role + ": " + ("refreshed with protected backup" if args.refresh else "seeded; later upgrades preserve this volume"))
    print("Named volumes prepared. Keep .env, DB backups and pre-upgrade config backups separately.")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, OSError, json.JSONDecodeError) as error:
        print("Volume provisioning stopped: " + str(error), file=sys.stderr)
        sys.exit(1)
