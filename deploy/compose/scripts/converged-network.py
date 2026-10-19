#!/usr/bin/env python3
"""Validate converged access identities and probe the actual OAM network path.

Public/Floating addresses are TLS/HTTP identities, never host bind addresses.
No cloud metadata API, external IP discovery service, or Docker daemon setting
is changed. Environment updates preserve unrelated lines and keep a 0600 backup.
"""
import argparse
import datetime
import ipaddress
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile
from urllib.parse import urlsplit

ROOT = pathlib.Path(__file__).resolve().parent.parent
EXCLUDED = re.compile(r"^(docker\d+|br-|veth|virbr|cni|flannel|llb\d+|vlan\d+|lo$)")


def run(args, **kwargs):
    return subprocess.run(args, capture_output=True, text=True, **kwargs)


def identity(value):
    value = value.strip()
    try:
        address = ipaddress.ip_address(value)
    except ValueError:
        if not value or len(value) > 253 or not all(
            re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", label)
            for label in value.split(".")
        ) or re.fullmatch(r"[0-9.]+", value):
            raise ValueError("Enter a hostname or IPv4 address without scheme, port or path")
        return value.lower()
    if address.version != 4 or address.is_unspecified or address.is_multicast:
        raise ValueError("This converged installer requires a unicast IPv4 address")
    return str(address)


def sites(value, port):
    result = []
    for item in value.split(","):
        parsed = urlsplit(item.strip())
        if (parsed.scheme != "https" or parsed.username or parsed.password
                or parsed.path or parsed.query or parsed.fragment or (parsed.port or 443) != port):
            raise ValueError("SITE_ADDRESS must contain HTTPS origins using EDGE_HTTPS_PORT")
        host = identity(parsed.hostname or "")
        if host not in result:
            result.append(host)
    return result


def env_values(path):
    values = {}
    for line in path.read_text().splitlines():
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            values[key] = value
    return values


def local_addresses():
    result = run(["ip", "-j", "-4", "addr", "show", "scope", "global"], check=True)
    return [a["local"] for item in json.loads(result.stdout)
            if not EXCLUDED.match(item["ifname"])
            for a in item.get("addr_info", []) if a.get("family") == "inet"]


def gateway_address(bind, configured, local):
    if bind != "0.0.0.0" and bind not in local:
        raise ValueError("EDGE_BIND_IP must be an actual host NIC address; do not bind a NAT/Floating IP")
    if configured:
        if configured not in local:
            raise ValueError("GW_CONNECT_IP must be an actual host NIC IPv4 address")
        return configured
    if bind in local:
        return bind
    if not local:
        raise ValueError("No usable host NIC IPv4 address for OAM to Gateway connectivity")
    # With a wildcard edge binding, choose the default-route NIC when available.
    route = run(["ip", "-j", "-4", "route", "get", "1.1.1.1"])
    if route.returncode == 0:
        for item in json.loads(route.stdout):
            if item.get("prefsrc") in local:
                return item["prefsrc"]
    if len(local) != 1:
        raise ValueError("Multiple NICs without a default route; specify --gateway-connect-ip")
    return local[0]


def update_env(path, updates):
    if path.is_symlink():
        raise ValueError("Refusing a symlink .env")
    before = path.read_text()
    pending = dict(updates)
    lines = []
    seen = set()
    for line in before.splitlines():
        key = line.split("=", 1)[0]
        if key in updates:
            if key in seen:
                raise ValueError("Duplicate network key in .env: " + key)
            seen.add(key)
            lines.append(key + "=" + pending.pop(key))
        else:
            lines.append(line)
    lines.extend(key + "=" + value for key, value in pending.items())
    after = "\n".join(lines) + "\n"
    if before == after:
        return
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    backup = path.parent / "backups" / "network" / stamp
    backup.mkdir(parents=True, mode=0o700)
    saved = backup / ".env"
    saved.write_text(before)
    saved.chmod(0o600)
    fd, name = tempfile.mkstemp(prefix=".env.network-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as output:
            output.write(after)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def configure(path, public, gateway_ip):
    env = env_values(path)
    gateway_host = identity(env["GW_HOST"])
    if gateway_host == "localhost" or re.fullmatch(r"[0-9.]+", gateway_host):
        raise ValueError("GW_HOST must be a dedicated certificate hostname, not localhost or an IP")
    port = int(env.get("EDGE_HTTPS_PORT", "8443"))
    if not 1 <= port <= 65535:
        raise ValueError("EDGE_HTTPS_PORT must be in 1..65535")
    hosts = sites(env["SITE_ADDRESS"], port)
    extra = [] if public == "-" else [identity(item) for item in public.split(",")]
    for host in extra:
        if host not in hosts:
            hosts.append(host)
    if not env.get("EDGE_TLS", "") and any(re.fullmatch(r"[0-9.]+", host) for host in hosts):
        raise ValueError("This installer's ACME mode requires DNS names; supply a matching edge certificate for IP access")
    bind = env.get("EDGE_BIND_IP", "0.0.0.0")
    local = local_addresses()
    selected = gateway_address(bind, gateway_ip or env.get("GW_CONNECT_IP", ""), local)
    origins = [value.strip() for value in env.get("OAM_ALLOWED_ORIGINS", "").split(",") if value.strip()]
    urls = ["https://" + host + ":" + str(port) for host in hosts]
    for url in urls:
        if url not in origins:
            origins.append(url)
    public_hosts = list(dict.fromkeys(extra + [host for host in hosts
        if re.fullmatch(r"[0-9.]+", host) and host not in local
        and not ipaddress.ip_address(host).is_loopback]))
    updates = {
        "GW_CONNECT_IP": selected,
        "SITE_ADDRESS": ", ".join(urls),
        "OAM_ALLOWED_ORIGINS": ", ".join(origins),
        "EDGE_PUBLIC_ADDRESSES": ",".join(public_hosts) or "-",
    }
    if any(re.fullmatch(r"[0-9.]+", host) for host in hosts):
        updates["EDGE_SNI_FALLBACK"] = "default_sni " + hosts[0]
    update_env(path, updates)
    for key, value in updates.items():
        print(key + "=" + value)
    san = [("IP:" if re.fullmatch(r"[0-9.]+", host) else "DNS:") + host for host in hosts]
    for entry in ["DNS:localhost", "IP:127.0.0.1"]:
        if entry not in san:
            san.append(entry)
    print("SAN_LIST=" + ",".join(san))


def certificate(path, self_signed=False):
    env = env_values(path)
    source = pathlib.Path(env.get("EDGE_TLS_SEED_DIR", "certs/edge"))
    if not source.is_absolute():
        source = path.parent / source
    cert = source / "cert.pem"
    if self_signed:
        result = run(["openssl", "x509", "-in", str(cert), "-noout", "-subject", "-issuer", "-nameopt", "RFC2253"])
        lines = result.stdout.splitlines()
        return (result.returncode == 0 and len(lines) == 2
                and lines[0].removeprefix("subject=") == lines[1].removeprefix("issuer=")
                and run(["openssl", "verify", "-check_ss_sig", "-no_check_time", "-CAfile", str(cert), str(cert)]).returncode == 0)
    for host in sites(env["SITE_ADDRESS"], int(env.get("EDGE_HTTPS_PORT", "8443"))):
        # Ubuntu OpenSSL 3.0's x509 -checkip/-checkhost can exit zero even
        # on a mismatch. verify has a reliable failure status on both 3.0/3.6.
        # Trusting this leaf is only for identity/expiry inspection here; the
        # live probes independently verify against the configured trust files.
        flag = "-verify_ip" if re.fullmatch(r"[0-9.]+", host) else "-verify_hostname"
        if run(["openssl", "verify", "-partial_chain", "-trusted", str(cert), flag, host, str(cert)]).returncode:
            print("Edge certificate does not cover: " + host)
            return False
    print("Edge certificate covers every configured console identity")
    return True


def probe(container, host):
    if re.fullmatch(r"[0-9.]+", identity(host)):
        raise ValueError("GW_HOST must be the Gateway certificate hostname")
    state = json.loads(run(["docker", "inspect", container], check=True).stdout)[0]
    if not state["State"]["Running"]:
        raise ValueError("OAM container is not running")
    entries = run(["docker", "exec", container, "cat", "/etc/hosts"], check=True).stdout
    addresses = [line.split()[0] for line in entries.splitlines()
                 if host in line.split("#", 1)[0].split()[1:]]
    if len(addresses) != 1:
        raise ValueError("OAM /etc/hosts must contain exactly one mapping for GW_HOST")
    values = dict(value.split("=", 1) for value in state["Config"]["Env"] if "=" in value)
    ca_target = values.get("OAM_INSTANCE_CA_BUNDLE", "/etc/loxilb-oam/certs/instance-ca.pem")
    mounts = sorted(state["Mounts"], key=lambda m: len(m["Destination"]), reverse=True)
    ca = None
    for mount in mounts:
        if ca_target == mount["Destination"] or ca_target.startswith(mount["Destination"] + "/"):
            ca = mount["Source"] + ca_target[len(mount["Destination"]):]
            break
    if ca is None:
        raise ValueError("OAM public CA must be mounted for the network probe")
    token_file = values.get("OAM_GATEWAY_SERVICE_TOKEN_FILE", "/run/secrets/gateway_service_token")
    token = run(["docker", "exec", container, "cat", token_file], check=True).stdout.strip()
    if not token or any(value in token for value in ['\r', '\n', '"', '\\']):
        raise ValueError("Invalid service-token file (value withheld)")
    tests = [("config/loadbalancer/all", False, "401"), ("version", True, "200"),
             ("config/port/all", True, "200")]
    for endpoint, authenticated, expected in tests:
        args = ["nsenter", "--target", str(state["State"]["Pid"]), "--net", "curl",
                "--noproxy", "*", "--cacert", ca, "--resolve", host + ":8091:" + addresses[0],
                "--connect-timeout", "3", "--max-time", "5", "-sS", "-K", "-",
                "-o", "/dev/null", "-w", "%{http_code}", "https://" + host + ":8091/netlox/v1/" + endpoint]
        config = 'header = "Authorization: Bearer ' + token + '"\n' if authenticated else ""
        result = run(args, input=config)
        label = "authenticated" if authenticated else "anonymous"
        print("OAM network → " + host + " (" + addresses[0] + "): " + endpoint
              + " " + label + " HTTP " + result.stdout + " (expected " + expected + ")")
        if result.returncode or result.stdout != expected:
            raise ValueError("OAM-to-Gateway probe failed; TLS verification was enabled; check mapping, CA and service identity")


def probe_edge(path):
    env = env_values(path)
    port = int(env.get("EDGE_HTTPS_PORT", "8443"))
    address = env.get("EDGE_BIND_IP", "0.0.0.0")
    if address == "0.0.0.0":
        address = "127.0.0.1"
    trust = []
    if env.get("EDGE_TLS") == "tls /certs/edge/cert.pem /certs/edge/key.pem":
        source = pathlib.Path(env.get("EDGE_TLS_SEED_DIR", "certs/edge"))
        if not source.is_absolute():
            source = path.parent / source
        trust = ["--cacert", str(source / "cert.pem")]
    for host in sites(env["SITE_ADDRESS"], port):
        for endpoint in ["/netlox/", "/api/oam/health"]:
            result = run(["curl", "--noproxy", "*", *trust, "--resolve", host + ":" + str(port) + ":" + address,
                          "--connect-timeout", "3", "--max-time", "5", "-sS", "-o", "/dev/null", "-w", "%{http_code}",
                          "https://" + host + ":" + str(port) + endpoint])
            print("Local edge identity check: " + host + endpoint + " HTTP " + result.stdout)
            if result.returncode or result.stdout != "200":
                raise ValueError("Edge identity/TLS/API probe failed; no certificate verification bypass was used")
    print("Public ingress/firewall reachability still requires a client outside this host/NAT")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["configure", "certificate", "probe", "edge-probe"])
    parser.add_argument("--env-file", type=pathlib.Path, default=ROOT / ".env")
    parser.add_argument("--public-address", default="-")
    parser.add_argument("--gateway-connect-ip", default="")
    parser.add_argument("--self-signed", action="store_true")
    parser.add_argument("--container", default="loxilb-mgmt-oam-loxilb-1")
    args = parser.parse_args()
    os.umask(0o077)
    if args.action == "configure":
        configure(args.env_file, args.public_address, args.gateway_connect_ip)
    elif args.action == "certificate":
        return 0 if certificate(args.env_file, args.self_signed) else 1
    elif args.action == "probe":
        probe(args.container, env_values(args.env_file)["GW_HOST"])
    else:
        probe_edge(args.env_file)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as error:
        # Do not expose subprocess stdin, rendered environments or credentials.
        message = "Command failed (details withheld)" if isinstance(error, subprocess.CalledProcessError) else str(error)
        print("ERROR: " + message, file=sys.stderr)
        sys.exit(1)
