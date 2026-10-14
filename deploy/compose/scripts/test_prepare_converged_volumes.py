#!/usr/bin/env python3
"""Persistence safety regressions; no Docker daemon or credentials required."""
import importlib.util
import pathlib
import subprocess
import tempfile
import unittest

SPEC = importlib.util.spec_from_file_location(
    "prepare_volumes", pathlib.Path(__file__).with_name("prepare-converged-volumes.py")
)
VOLUMES = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VOLUMES)


class MaterialPolicyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = pathlib.Path(self.directory.name)

    def leaf_pair(self):
        (self.root / "server.crt").write_text("test certificate")
        (self.root / "server.key").write_text("test leaf key")

    def test_missing_gateway_pair_refused(self):
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("gateway-tls", self.root, None)

    def test_incomplete_edge_pair_refused(self):
        (self.root / "cert.pem").write_text("test certificate")
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("edge-tls", self.root, None)

    def test_empty_edge_store_allowed_for_automatic_tls(self):
        VOLUMES.validate_material("edge-tls", self.root, None)

    def test_signing_key_hidden_in_subdirectory_refused(self):
        self.leaf_pair()
        nested = self.root / "custody"
        nested.mkdir()
        (nested / "server.key").write_text("-----BEGIN " + "PRIVATE KEY-----")
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("gateway-tls", self.root, None)

    def test_public_ca_with_private_key_refused(self):
        ca = self.root / "ca.pem"
        ca.write_text("-----BEGIN " + "PRIVATE KEY-----")
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("instance-ca", ca, "instance-ca.pem")

    def test_leaf_and_nested_public_ca_allowed(self):
        self.leaf_pair()
        nested = self.root / "trust"
        nested.mkdir()
        (nested / "ca.pem").write_text("test public anchor")
        VOLUMES.validate_material("gateway-tls", self.root, None)

    def test_symlink_seed_refused(self):
        self.leaf_pair()
        (self.root / "ca.pem").symlink_to(self.root / "server.crt")
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("gateway-tls", self.root, None)

    def test_empty_credential_refused(self):
        token = self.root / "token"
        token.touch()
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_material("gateway-token", token, "gateway_service_token")

    def test_aliased_volume_roles_refused(self):
        with self.assertRaises(RuntimeError):
            VOLUMES.validate_volume_names(["config", "db", "config"])

    def test_nested_hidden_and_quoted_filenames_are_compared(self):
        seed = self.root / "seed"
        target = self.root / "target"
        seed.mkdir()
        target.mkdir()
        for base in (seed, target):
            (base / "trust").mkdir()
            (base / "trust/ca's bundle.pem").write_text("same anchor")
            (base / ".hidden.pem").write_text("same hidden anchor")
        script = VOLUMES.comparison_script(seed, None)
        # Run the generated shell comparison against temporary stores.
        script = script.replace("/seed/", str(seed) + "/").replace("/persist/", str(target) + "/")
        self.assertEqual(subprocess.run(["sh", "-ec", script]).returncode, 0)
        (target / "trust/ca's bundle.pem").write_text("different anchor")
        self.assertNotEqual(subprocess.run(["sh", "-ec", script]).returncode, 0)
        (target / "trust/ca's bundle.pem").write_text("same anchor")
        (target / ".hidden.pem").write_text("different hidden anchor")
        self.assertNotEqual(subprocess.run(["sh", "-ec", script]).returncode, 0)


if __name__ == "__main__":
    unittest.main()
