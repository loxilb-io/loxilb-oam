#!/usr/bin/env python3
"""Regression checks for NAT identities, missing bridges, and real-path gates."""
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("converged_network", Path(__file__).with_name("converged-network.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class NetworkTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.env = self.root / ".env"
        self.original = ("# preserve unrelated settings\nDB_PASSWORD=unchanged-secret\n"
                         "OAM_JWT_SECRET=unchanged-jwt\nGW_HOST=gw.example.internal\n"
                         "EDGE_BIND_IP=10.0.0.14\nEDGE_HTTPS_PORT=8443\n"
                         "SITE_ADDRESS=https://oam.internal:8443, https://10.0.0.14:8443\n"
                         "OAM_ALLOWED_ORIGINS=https://oam.internal:8443\n"
                         "EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem\n")
        self.env.write_text(self.original)

    def configure(self, public="203.0.113.10", gateway=""):
        with patch.object(module, "local_addresses", return_value=["10.0.0.14"]), contextlib.redirect_stdout(io.StringIO()):
            module.configure(self.env, public, gateway)
        return module.env_values(self.env)

    def test_floating_ip_added_to_sites_origins_and_sni_not_bind(self):
        result = self.configure()
        self.assertIn("https://203.0.113.10:8443", result["SITE_ADDRESS"])
        self.assertIn("https://203.0.113.10:8443", result["OAM_ALLOWED_ORIGINS"])
        self.assertEqual(result["EDGE_BIND_IP"], "10.0.0.14")
        self.assertEqual(result["GW_CONNECT_IP"], "10.0.0.14")
        self.assertEqual(result["EDGE_SNI_FALLBACK"], "default_sni oam.internal")
        self.assertIn("DB_PASSWORD=unchanged-secret\nOAM_JWT_SECRET=unchanged-jwt\n", self.env.read_text())
        backup = list((self.root / "backups/network").glob("*/.env"))
        self.assertEqual(backup[0].read_text(), self.original)
        self.assertEqual(backup[0].stat().st_mode & 0o777, 0o600)
        self.assertEqual(self.env.stat().st_mode & 0o777, 0o600)

    def test_replay_is_byte_identical_and_keeps_public_address(self):
        first = self.configure()
        before = self.env.read_bytes()
        self.configure("-")
        self.assertEqual(self.env.read_bytes(), before)
        self.assertEqual(first["EDGE_PUBLIC_ADDRESSES"], "203.0.113.10")
        self.assertEqual(len(list((self.root / "backups/network").glob("*/.env"))), 1)

    def test_invalid_public_inputs_do_not_modify_env(self):
        for value in ["https://public.example", "1.2.3.999", "public.example:8443", "good.example\nDB_PASSWORD=x", "::1", "0.0.0.0", "a,,b", "a/b", "a?b", "a#b"]:
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.configure(value)
            self.assertEqual(self.env.read_text(), self.original)

    def test_floating_ip_cannot_be_bind_or_gateway(self):
        with self.assertRaisesRegex(ValueError, "GW_CONNECT_IP"):
            self.configure(gateway="203.0.113.10")
        self.env.write_text(self.original.replace("EDGE_BIND_IP=10.0.0.14", "EDGE_BIND_IP=203.0.113.10"))
        with self.assertRaisesRegex(ValueError, "EDGE_BIND_IP"):
            self.configure()

    def test_no_default_bridge_required_and_wildcard_uses_real_route(self):
        self.assertEqual(module.gateway_address("10.0.0.14", "", ["10.0.0.14"]), "10.0.0.14")
        with patch.object(module, "run", return_value=subprocess.CompletedProcess([], 0, '[{"prefsrc":"10.0.0.14"}]', "")):
            self.assertEqual(module.gateway_address("0.0.0.0", "", ["10.0.0.14", "10.1.0.14"]), "10.0.0.14")

    def test_stale_explicit_gateway_address_is_not_silently_reused(self):
        self.env.write_text(self.original + "GW_CONNECT_IP=172.17.0.1\n")
        with self.assertRaisesRegex(ValueError, "GW_CONNECT_IP"):
            self.configure()
        self.assertEqual(self.configure(gateway="10.0.0.14")["GW_CONNECT_IP"], "10.0.0.14")

    def test_duplicate_keys_and_symlinks_refused(self):
        self.env.write_text(self.original + "SITE_ADDRESS=https://other:8443\n")
        with self.assertRaisesRegex(ValueError, "Duplicate"):
            self.configure()
        target = self.root / "target"
        target.write_text(self.original)
        self.env.unlink()
        self.env.symlink_to(target)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.configure()
        self.assertEqual(target.read_text(), self.original)

    def test_acme_does_not_silently_accept_ip_or_replace_mode(self):
        self.env.write_text(self.original.replace("EDGE_TLS=tls /certs/edge/cert.pem /certs/edge/key.pem", "EDGE_TLS="))
        with self.assertRaisesRegex(ValueError, "ACME"):
            self.configure()

    def test_certificate_missing_public_san_is_red_then_recovers(self):
        self.configure()
        seed = self.root / "certs/edge"
        seed.mkdir(parents=True)
        def generate(san):
            subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                            "-keyout", str(seed / "key.pem"), "-out", str(seed / "cert.pem"),
                            "-subj", "/CN=oam.internal", "-addext", "subjectAltName=" + san],
                           check=True, capture_output=True)
        generate("DNS:oam.internal,IP:10.0.0.14")
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertFalse(module.certificate(self.env))
            self.assertTrue(module.certificate(self.env, self_signed=True))
            generate("DNS:oam.internal,IP:10.0.0.14,IP:203.0.113.10")
            self.assertTrue(module.certificate(self.env))

    def test_probe_uses_effective_oam_hosts_ca_token_and_enforces_negative(self):
        calls = []
        state = [{"State": {"Pid": 321, "Running": True},
                  "Config": {"Env": ["OAM_INSTANCE_CA_BUNDLE=/etc/ca/instance-ca.pem", "OAM_GATEWAY_SERVICE_TOKEN_FILE=/run/token"]},
                  "Mounts": [{"Destination": "/etc/ca", "Source": "/vol/public-ca"}]}]
        def fake(args, **kwargs):
            calls.append((args, kwargs))
            if args[:2] == ["docker", "inspect"]:
                return subprocess.CompletedProcess(args, 0, json.dumps(state), "")
            if args[-1] == "/etc/hosts":
                return subprocess.CompletedProcess(args, 0, "10.0.0.14 gw.example.internal\n", "")
            if args[-1] == "/run/token":
                return subprocess.CompletedProcess(args, 0, "test-token\n", "")
            code = "200" if kwargs["input"] else "401"
            return subprocess.CompletedProcess(args, 0, code, "")
        with patch.object(module, "run", side_effect=fake), contextlib.redirect_stdout(io.StringIO()):
            module.probe("oam-test", "gw.example.internal")
        probes = [(args, kwargs) for args, kwargs in calls if args[0] == "nsenter"]
        self.assertEqual(len(probes), 3)
        for args, kwargs in probes:
            self.assertIn("gw.example.internal:8091:10.0.0.14", args)
            self.assertIn("/vol/public-ca/instance-ca.pem", args)
            self.assertNotIn("test-token", " ".join(args))
            self.assertNotIn("-k", args)
        self.assertIn("test-token", probes[1][1]["input"])
        def anonymous_allowed(args, **kwargs):
            value = fake(args, **kwargs)
            if args[0] == "nsenter":
                value.stdout = "200"
            return value
        with patch.object(module, "run", side_effect=anonymous_allowed), contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(ValueError, "probe failed"):
            module.probe("oam-test", "gw.example.internal")

    def test_probe_timeout_cannot_pass_on_host_loopback_success(self):
        def fake(args, **kwargs):
            if args[:2] == ["docker", "inspect"]:
                return subprocess.CompletedProcess(args, 0, json.dumps([{"State": {"Pid": 321, "Running": True}, "Config": {"Env": []}, "Mounts": [{"Destination": "/etc/loxilb-oam/certs", "Source": "/ca"}]}]), "")
            if args[-1] == "/etc/hosts":
                return subprocess.CompletedProcess(args, 0, "172.17.0.1 gw.example.internal\n", "")
            if args[0] == "docker":
                return subprocess.CompletedProcess(args, 0, "test-token", "")
            return subprocess.CompletedProcess(args, 28, "000", "timeout")
        with patch.object(module, "run", side_effect=fake), contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(ValueError, "probe failed"):
            module.probe("oam-test", "gw.example.internal")

    def test_ca_issued_certificate_is_not_classified_as_self_signed(self):
        seed = self.root / "certs/edge"
        seed.mkdir(parents=True)
        commands = [
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=operator-ca", "-keyout", str(self.root / "ca.key"), "-out", str(self.root / "ca.pem")],
            ["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-subj", "/CN=oam.internal", "-keyout", str(seed / "key.pem"), "-out", str(self.root / "edge.csr")],
            ["openssl", "x509", "-req", "-in", str(self.root / "edge.csr"), "-CA", str(self.root / "ca.pem"), "-CAkey", str(self.root / "ca.key"), "-CAcreateserial", "-days", "1", "-out", str(seed / "cert.pem")],
        ]
        for args in commands:
            subprocess.run(args, check=True, capture_output=True)
        self.assertFalse(module.certificate(self.env, self_signed=True))

    def test_expired_self_signed_certificate_can_be_identified_for_renewal(self):
        seed = self.root / "certs/edge"
        seed.mkdir(parents=True)
        subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=oam.internal", "-keyout", str(seed / "key.pem"), "-out", str(seed / "cert.pem")], check=True, capture_output=True)
        (self.root / "index.txt").write_text("")
        (self.root / "serial").write_text("01\n")
        config = self.root / "ca.cnf"
        config.write_text("[ca]\ndefault_ca=local\n[local]\ndatabase=" + str(self.root / "index.txt")
                          + "\nserial=" + str(self.root / "serial") + "\nnew_certs_dir=" + str(self.root)
                          + "\ndefault_md=sha256\npolicy=names\n[names]\ncommonName=supplied\n")
        subprocess.run(["openssl", "req", "-new", "-key", str(seed / "key.pem"), "-subj", "/CN=oam.internal", "-out", str(self.root / "edge.csr")], check=True, capture_output=True)
        subprocess.run(["openssl", "ca", "-selfsign", "-batch", "-config", str(config), "-keyfile", str(seed / "key.pem"), "-cert", str(seed / "cert.pem"), "-in", str(self.root / "edge.csr"), "-startdate", "20000101000000Z", "-enddate", "20000102000000Z", "-out", str(seed / "expired.pem")], check=True, capture_output=True)
        (seed / "cert.pem").write_bytes((seed / "expired.pem").read_bytes())
        self.assertTrue(module.certificate(self.env, self_signed=True))
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertFalse(module.certificate(self.env))

    def test_edge_probe_validates_public_identity_without_claiming_ingress(self):
        self.configure()
        calls = []
        def fake(args, **kwargs):
            calls.append(args)
            return subprocess.CompletedProcess(args, 0, "200", "")
        output = io.StringIO()
        with patch.object(module, "run", side_effect=fake), contextlib.redirect_stdout(output):
            module.probe_edge(self.env)
        self.assertEqual(len(calls), 6)
        self.assertIn("203.0.113.10:8443:10.0.0.14", calls[-1])
        self.assertIn("--cacert", calls[-1])
        self.assertIn("requires a client outside", output.getvalue())
        with patch.object(module, "run", return_value=subprocess.CompletedProcess([], 60, "000", "TLS rejection")), contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(ValueError, "probe failed"):
            module.probe_edge(self.env)


if __name__ == "__main__":
    unittest.main()
