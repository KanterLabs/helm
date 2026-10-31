#!/usr/bin/env python3
"""Isolated SSH transport proofs; written before the client implementation."""
import importlib.machinery
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


class TransportProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        path = Path(__file__).resolve().parents[2] / 'deploy/ssh-observer.py'
        spec = importlib.util.spec_from_file_location('ssh_observer', path)
        self.mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.mod)
        self.mod.DIRECTORY = self.root
        self.fake = self.root / 'ssh'
        self.fake.write_text('#!/usr/bin/python3\nimport json,os,sys\n'
                             'print(json.dumps({"args":sys.argv[1:],"input":sys.stdin.read(),"env":sorted(os.environ)}))\n')
        self.fake.chmod(0o700)
        self.mod.SSH = str(self.fake)
        self.config = {'host': '100.64.0.9', 'port': 22, 'user': 'helm-observer'}
        self.save_config()
        for name in ['id_ed25519', 'known_hosts']:
            p = self.root / name
            p.write_text('synthetic fixture')
            p.chmod(0o600)

    def save_config(self):
        path = self.root / 'client.json'
        path.write_text(json.dumps(self.config))
        path.chmod(0o600)

    def invoke(self, payload=b'{}'):
        output = io.StringIO()
        with patch.object(sys, 'argv', ['ssh-observer.py']), \
                patch.object(sys, 'stdin', io.TextIOWrapper(io.BytesIO(payload))), \
                patch.object(sys, 'stdout', output), patch.object(sys, 'stderr', io.StringIO()), \
                patch.dict(os.environ, {'COOLIFY_API_TOKEN': 'must-not-reach-ssh'}):
            code = self.mod.main()
        return code, output.getvalue()

    def test_exact_endpoint_host_pinning_and_no_remote_command_or_secret_env(self):
        code, output = self.invoke(b'{"nonce":"synthetic"}')
        self.assertEqual(code, 0)
        value = json.loads(output)
        self.assertEqual(value['args'][-1], 'helm-observer@100.64.0.9')
        self.assertIn('StrictHostKeyChecking=yes', value['args'])
        self.assertIn('ClearAllForwardings=yes', value['args'])
        self.assertIn('IdentityAgent=none', value['args'])
        self.assertEqual(value['input'], '{"nonce":"synthetic"}')
        self.assertNotIn('COOLIFY_API_TOKEN', value['env'])

    def test_malformed_or_public_target_rejected_before_ssh(self):
        base = dict(self.config)
        for value in [dict(base, user='root'), dict(base, port=23),
                      dict(base, host='1.1.1.1'), dict(base, extra='command')]:
            self.config = value
            self.save_config()
            code, output = self.invoke()
            self.assertNotEqual(code, 0)
            self.assertEqual(output, '')

    def test_writable_credentials_symlinks_and_oversized_input_fail_closed(self):
        key = self.root / 'id_ed25519'
        key.chmod(0o644)
        self.assertNotEqual(self.invoke()[0], 0)
        key.unlink()
        key.symlink_to(self.fake)
        self.assertNotEqual(self.invoke()[0], 0)
        key.unlink()
        key.write_text('synthetic fixture')
        key.chmod(0o600)
        self.assertNotEqual(self.invoke(b'x' * 20001)[0], 0)


if __name__ == '__main__':
    unittest.main()
