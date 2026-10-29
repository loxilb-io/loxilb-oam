#!/usr/bin/env python3
"""Negative and replay checks for seed custody; no Docker daemon required."""
import contextlib
import importlib.util
import io
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('prepare_integrations', Path(__file__).with_name('prepare-integrations.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SeedCustodyTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        (self.root / 'integrations').mkdir()
        template = self.root / 'integrations/syslog-ng.conf'
        template.write_text((module.BASE / 'integrations/syslog-ng.conf').read_text())
        template.chmod(0o600)
        self.base = patch.object(module, 'BASE', self.root)
        self.base.start()
        self.addCleanup(self.base.stop)

    def prepare(self, *args):
        with patch.object(sys, 'argv', ['prepare', '--no-volumes', *args]), contextlib.redirect_stdout(io.StringIO()):
            module.main()

    def hashes(self):
        return {str(p.relative_to(self.root)): p.read_bytes() for p in self.root.rglob('*') if p.is_file() and str(p.relative_to(self.root)) != 'integrations/syslog-ng.conf'}

    def test_explicit_opt_in(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.prepare()
        self.assertEqual(self.hashes(), {})

    def test_invalid_host_does_not_create_seeds(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.prepare('--keycloak', '--keycloak-host', 'localhost\ndb-password=oops')
        self.assertEqual(self.hashes(), {})

    def test_replay_and_incremental_selection_preserve_secrets(self):
        self.prepare('--siem')
        first = self.hashes()
        self.prepare('--keycloak')
        both = self.hashes()
        for name, value in first.items():
            # OpenSSL serial is incremented only when signing a NEW cert.
            if not name.endswith('.srl'):
                self.assertEqual(both[name], value)
        self.prepare('--keycloak', '--siem')
        self.assertEqual(both, self.hashes())
        self.assertEqual((self.root / 'integrations/private').stat().st_mode & 0o777, 0o700)
        self.assertEqual((self.root / 'integrations/trust').stat().st_mode & 0o777, 0o755)
        self.assertEqual((self.root / 'integrations/private/syslog-ng.conf').stat().st_mode & 0o777, 0o444)
        self.assertEqual((self.root / 'integrations/private/ca.key').stat().st_mode & 0o777, 0o600)

    def test_changed_receiver_config_requires_explicit_update(self):
        self.prepare('--siem')
        before = self.hashes()
        (self.root / 'integrations/syslog-ng.conf').write_text('changed configuration')
        with self.assertRaises(RuntimeError):
            self.prepare('--siem')
        self.assertEqual(before, self.hashes())

    def test_changed_hostname_refused_without_replacing_files(self):
        self.prepare('--keycloak')
        before = self.hashes()
        with self.assertRaises((RuntimeError, module.subprocess.CalledProcessError)):
            self.prepare('--keycloak', '--keycloak-host', 'different.example')
        self.assertEqual(before, self.hashes())

    def test_changed_ca_and_incomplete_pair_refused(self):
        self.prepare('--siem')
        ca = self.root / 'integrations/trust/integration-ca.crt'
        ca.write_text('wrong CA')
        before = self.hashes()
        with self.assertRaises(RuntimeError):
            self.prepare('--siem')
        self.assertEqual(before, self.hashes())
        ca.write_bytes((self.root / 'integrations/private/ca.crt').read_bytes())
        (self.root / 'integrations/private/siem.key').unlink()
        with self.assertRaises(RuntimeError):
            self.prepare('--siem')


if __name__ == '__main__':
    unittest.main()
