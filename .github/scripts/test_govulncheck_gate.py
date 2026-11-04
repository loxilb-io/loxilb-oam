#!/usr/bin/env python3
"""Regression tests for govulncheck-gate.py. Standard library only.

Run: python3 -m unittest discover -s .github/scripts -p 'test_*.py'
"""
import importlib.util
import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout, redirect_stderr
from io import StringIO

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("gate", os.path.join(HERE, "govulncheck-gate.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)

OWN = {"module": gate.MODULE, "package": gate.MODULE, "function": "main"}


def finding(osv, trace, fixed="v1.2.3"):
    fd = {"osv": osv, "trace": trace}
    if fixed:
        fd["fixed_version"] = fixed
    return {"finding": fd}


def run(objs, *mode):
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False) as fh:
        fh.write("\n".join(json.dumps(o, indent=2) for o in objs))
        path = fh.name
    try:
        with redirect_stdout(StringIO()), redirect_stderr(StringIO()):
            return gate.main([*mode, path])
    finally:
        os.unlink(path)


SYMBOL = {"module": "golang.org/x/net", "package": "golang.org/x/net/html", "function": "Parse"}
MODULE_ONLY = {"module": "golang.org/x/crypto"}
CONFIG = {"config": {"scan_mode": "binary"}}


class SourceMode(unittest.TestCase):
    def test_called_from_this_module_with_fix_fails(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [SYMBOL, OWN])]), 1)

    def test_symbol_not_called_from_this_module_passes(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [SYMBOL])]), 0)

    def test_module_level_passes(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [MODULE_ONLY])]), 0)

    def test_called_without_fix_passes(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [SYMBOL, OWN], fixed="")]), 0)


class BinaryMode(unittest.TestCase):
    def test_symbol_in_binary_with_fix_fails(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [SYMBOL])], "--mode", "binary"), 1)

    def test_module_level_passes(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [MODULE_ONLY])], "--mode", "binary"), 0)

    def test_symbol_without_fix_passes(self):
        self.assertEqual(run([CONFIG, finding("GO-1", [SYMBOL], fixed="")], "--mode", "binary"), 0)


class Usage(unittest.TestCase):
    def test_empty_output_is_an_error(self):
        self.assertEqual(run([]), 2)

    def test_unknown_mode_is_an_error(self):
        self.assertEqual(run([CONFIG], "--mode", "fast"), 2)


if __name__ == "__main__":
    unittest.main()
