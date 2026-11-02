#!/usr/bin/env python3
"""Env examples and Compose files must fail closed on a missing required secret.

Compose reads the text after a blank `KEY=   # note` as the value, so an inline
comment defeats the `${KEY:?}` guard. No Docker daemon required.
"""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[3]
ENV_EXAMPLES = [ROOT / '.env.example', ROOT / 'deploy/compose/.env.example']
COMPOSE_FILES = [ROOT / 'docker-compose.yml', *sorted((ROOT / 'deploy/compose').glob('docker-compose*.yml'))]
REQUIRED_SECRETS = {'DB_PASSWORD', 'OAM_JWT_SECRET', 'OAM_DEFAULT_ADMIN_PASSWORD', 'OAM_APP_DB_PASSWORD'}
# Environment keys that must receive a guarded secret, whatever variable feeds them.
GUARDED_KEYS = {'DB_PASSWORD', 'OAM_JWT_SECRET', 'OAM_DEFAULT_ADMIN_PASSWORD', 'POSTGRES_PASSWORD', 'PGPASSWORD'}

VALUE_LINE = re.compile(r'^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)=(.*)$')
REFERENCE = re.compile(r'\$\{([A-Za-z_][A-Za-z0-9_]*)([^}]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)')
ENVIRONMENT_ENTRY = re.compile(r'^\s*(?:-\s*)?["\']?([A-Z_][A-Z0-9_]*)["\']?\s*[:=]\s*(.*)$')


def inline_comments(text):
    """Line numbers of value lines whose value is followed by a comment."""
    found = []
    for number, line in enumerate(text.splitlines(), 1):
        match = VALUE_LINE.match(line)
        if not match:
            continue
        value = match.group(2)
        if value[:1] in ('"', "'"):
            end = value.find(value[0], 1)
            value = value[end + 1:] if end > 0 else ''
        if re.search(r'(^|\s)#', value):
            found.append(number)
    return found


def filled_secrets(text):
    """Required secrets that an example ships with a value."""
    found = []
    for line in text.splitlines():
        match = VALUE_LINE.match(line)
        if match and match.group(1) in REQUIRED_SECRETS and match.group(2).strip():
            found.append(match.group(1))
    return found


def unguarded_secrets(text):
    """Required secrets a Compose file can start without, as `line: problem`."""
    found = []
    for number, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith('#'):
            continue
        for match in REFERENCE.finditer(line):
            name = match.group(1) or match.group(3)
            if name in REQUIRED_SECRETS and not (match.group(2) or '').startswith(':?'):
                found.append(f'{number}: {name} has a default or no guard')
        entry = ENVIRONMENT_ENTRY.match(line)
        if entry and entry.group(1) in GUARDED_KEYS:
            value = entry.group(2).strip().strip('"\'')
            if not re.match(r'\$\{[A-Za-z_][A-Za-z0-9_]*:\?', value):
                found.append(f'{number}: {entry.group(1)} is not fed by a guarded variable')
    return found


class RepositoryFiles(unittest.TestCase):
    def test_env_examples_have_no_inline_comments(self):
        for path in ENV_EXAMPLES:
            with self.subTest(path=path.relative_to(ROOT)):
                self.assertEqual(inline_comments(path.read_text()), [])

    def test_env_examples_leave_required_secrets_blank(self):
        for path in ENV_EXAMPLES:
            with self.subTest(path=path.relative_to(ROOT)):
                self.assertEqual(filled_secrets(path.read_text()), [])

    def test_compose_files_guard_required_secrets(self):
        self.assertGreater(len(COMPOSE_FILES), 1)
        for path in COMPOSE_FILES:
            with self.subTest(path=path.relative_to(ROOT)):
                self.assertEqual(unguarded_secrets(path.read_text()), [])

    def test_base_stacks_reference_each_secret(self):
        for path in (ROOT / 'docker-compose.yml', ROOT / 'deploy/compose/docker-compose.yml'):
            text = path.read_text()
            for name in ('DB_PASSWORD', 'OAM_JWT_SECRET', 'OAM_DEFAULT_ADMIN_PASSWORD'):
                with self.subTest(path=path.relative_to(ROOT), name=name):
                    self.assertIn('${' + name + ':?', text)


class CheckersCatchTheDefect(unittest.TestCase):
    def test_inline_comment_detection(self):
        self.assertEqual(inline_comments('A=   # note\n'), [1])
        self.assertEqual(inline_comments('OK=1\nA=value # note\n'), [2])
        self.assertEqual(inline_comments('A=#note\n'), [1])
        self.assertEqual(inline_comments('A="x" # note\n'), [1])

    def test_inline_comment_detection_allows_clean_lines(self):
        self.assertEqual(inline_comments('# comment\n\nA=\nB=value\nC=a#b\nD="x # y"\n# E=1  # off\n'), [])

    def test_filled_secret_detection(self):
        self.assertEqual(filled_secrets('OAM_JWT_SECRET=abc\n'), ['OAM_JWT_SECRET'])
        self.assertEqual(filled_secrets('OAM_JWT_SECRET=\nOTHER=abc\n'), [])

    def test_default_and_missing_guards_are_caught(self):
        for line in (
            'OAM_JWT_SECRET: ${OAM_JWT_SECRET:-fallback}',
            'OAM_JWT_SECRET: ${OAM_JWT_SECRET:-}',
            'OAM_JWT_SECRET: ${OAM_JWT_SECRET-x}',
            'OAM_JWT_SECRET: ${OAM_JWT_SECRET}',
            'OAM_JWT_SECRET: $OAM_JWT_SECRET',
            'OAM_JWT_SECRET: literal-value',
            'POSTGRES_PASSWORD: ${SOME_OTHER:-postgres}',
            '- DB_PASSWORD=literal-value',
        ):
            with self.subTest(line=line):
                self.assertNotEqual(unguarded_secrets('      ' + line + '\n'), [])

    def test_guarded_references_pass(self):
        text = ('      OAM_JWT_SECRET: ${OAM_JWT_SECRET:?set it}\n'
                '      DB_PASSWORD: ${OAM_APP_DB_PASSWORD:?set it}\n'
                '      # OAM_JWT_SECRET: ${OAM_JWT_SECRET:-commented}\n'
                '      SNAPSHOT_ENC_KEY: ${SNAPSHOT_ENC_KEY:-}\n')
        self.assertEqual(unguarded_secrets(text), [])


if __name__ == '__main__':
    unittest.main()
