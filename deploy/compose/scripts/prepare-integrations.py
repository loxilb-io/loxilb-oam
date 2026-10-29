#!/usr/bin/env python3
"""Provision opt-in integration seeds once; never rotate or delete existing state."""
import argparse
import os
from pathlib import Path
import re
import secrets
import subprocess

BASE = Path(__file__).resolve().parents[1]


def run(*args):
    return subprocess.run(args, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout


def write_once(path, value, mode=0o444):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, mode)
    with os.fdopen(fd, "w") as stream:
        stream.write(value)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--keycloak', action='store_true')
    parser.add_argument('--siem', action='store_true')
    parser.add_argument('--keycloak-host', default='localhost', help='Certificate DNS SAN')
    parser.add_argument('--keycloak-port', type=int, default=18443)
    parser.add_argument('--no-volumes', action='store_true', help='Prepare files only')
    args = parser.parse_args()
    if not (args.keycloak or args.siem):
        parser.error('select --keycloak and/or --siem explicitly')
    if not re.fullmatch(r'[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?', args.keycloak_host):
        parser.error('keycloak host must be a DNS name (use localhost for loopback)')
    if not 1 <= args.keycloak_port <= 65535:
        parser.error('invalid keycloak port')
    os.umask(0o077)
    private = BASE / 'integrations/private'
    private.mkdir(mode=0o700, parents=True, exist_ok=True)
    if private.is_symlink() or private.stat().st_mode & 0o077:
        raise RuntimeError('private directory must be a real directory with mode 0700')
    ca = private / 'ca.crt'
    ca_key = private / 'ca.key'
    if not ca.exists() and not ca_key.exists():
        run('openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes', '-days', '365',
            '-keyout', str(ca_key), '-out', str(ca), '-subj', '/CN=LoxiLB optional integrations CA',
            '-addext', 'basicConstraints=critical,CA:TRUE', '-addext', 'keyUsage=critical,keyCertSign,cRLSign')
    if not ca.exists() or not ca_key.exists():
        raise RuntimeError('incomplete CA seed; recover the matching files from protected backup')
    trust = BASE / 'integrations/trust'
    trust.mkdir(mode=0o755, exist_ok=True)
    trust.chmod(0o755)
    public_ca = trust / 'integration-ca.crt'
    if not public_ca.exists():
        write_once(public_ca, ca.read_text())
        public_ca.chmod(0o644)
    if public_ca.read_bytes() != ca.read_bytes():
        raise RuntimeError('trust CA differs; explicit rotation is required')
    for service, selected in [('keycloak', args.keycloak), ('siem', args.siem)]:
        if not selected:
            continue
        cert, key = private / f'{service}.crt', private / f'{service}.key'
        host = args.keycloak_host if service == 'keycloak' else 'localhost'
        if cert.exists() != key.exists():
            raise RuntimeError(f'incomplete {service} certificate; recover from backup')
        if not cert.exists():
            csr, ext = private / f'{service}.csr', private / f'{service}.ext'
            write_once(ext, f'subjectAltName=DNS:{host},DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n')
            run('openssl', 'req', '-newkey', 'rsa:3072', '-nodes', '-keyout', str(key),
                '-out', str(csr), '-subj', f'/CN={host}')
            run('openssl', 'x509', '-req', '-in', str(csr), '-CA', str(ca), '-CAkey', str(ca_key),
                '-CAcreateserial', '-days', '90', '-extfile', str(ext), '-out', str(cert))
            # Individual read-only bind mounts/secrets must be readable by UID 1000.
            # Host traversal remains restricted by private/ mode 0700.
            cert.chmod(0o444)
            key.chmod(0o444)
        run('openssl', 'verify', '-CAfile', str(ca), str(cert))
        run('openssl', 'x509', '-in', str(cert), '-noout', '-checkhost', host, '-checkend', '0')
        cert_pub = run('openssl', 'x509', '-in', str(cert), '-pubkey', '-noout')
        key_pub = run('openssl', 'pkey', '-in', str(key), '-pubout')
        if cert_pub != key_pub:
            raise RuntimeError(f'{service} certificate/key mismatch')
    if args.siem:
        source = BASE / 'integrations/syslog-ng.conf'
        runtime_config = private / 'syslog-ng.conf'
        if not source.is_file():
            raise RuntimeError('missing syslog-ng.conf template')
        if not runtime_config.exists():
            write_once(runtime_config, source.read_text())
            runtime_config.chmod(0o444)
        elif runtime_config.read_bytes() != source.read_bytes():
            raise RuntimeError('syslog config changed; review and explicitly update the protected runtime copy')
    if args.keycloak:
        for name in ('db_password', 'admin_password'):
            path = private / name
            if not path.exists():
                write_once(path, secrets.token_urlsafe(36) + '\n')
                path.chmod(0o444)
        config = (
            'db=postgres\ndb-url=jdbc:postgresql://postgres:5432/keycloak\ndb-username=keycloak\n'
            f'db-password={(private / "db_password").read_text().strip()}\n'
            f'hostname=https://{args.keycloak_host}:{args.keycloak_port}\n'
            'https-certificate-file=/opt/keycloak/conf/server.crt\n'
            'https-certificate-key-file=/opt/keycloak/conf/server.key\n'
            'http-enabled=false\nhealth-enabled=true\n')
        cfg = private / 'keycloak.conf'
        if not cfg.exists():
            write_once(cfg, config)
            cfg.chmod(0o444)
        elif cfg.read_text() != config:
            raise RuntimeError('existing Keycloak config differs; do not rotate seeds implicitly')
    if not args.no_volumes:
        volumes = []
        if args.keycloak:
            volumes.append(os.environ.get('KEYCLOAK_DB_VOLUME', 'loxilb-keycloak-db'))
        if args.siem:
            volumes.extend([os.environ.get('SIEM_LOG_VOLUME', 'loxilb-siem-logs'),
                            os.environ.get('SIEM_STATE_VOLUME', 'loxilb-siem-state')])
        for name in volumes:
            run('docker', 'volume', 'create', name)
    print('Prepared selected integrations; existing seeds preserved. No services started.')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError, subprocess.CalledProcessError) as exc:
        # A failed subprocess can carry private paths, but never print config/password contents.
        raise SystemExit(f'Preparation failed: {exc}')
