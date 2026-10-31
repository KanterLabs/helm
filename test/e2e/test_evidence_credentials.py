"""Final reporter/copy evidence gate proofs; temporary synthetic bytes only."""
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('evidence_credentials.py')


class CredentialEvidenceProof(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.artifacts = self.root / 'artifacts'
        self.state = self.root / 'state'
        self.artifacts.mkdir()
        self.state.mkdir()
        spec = importlib.util.spec_from_file_location('credential_evidence_gate', SCRIPT)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)

    def fingerprints(self, value):
        record = {'length': len(value), 'sha256': hashlib.sha256(value).hexdigest()}
        (self.state / 'membership-credential-fingerprints.json').write_text(json.dumps([record]))

    def test_no_membership_fingerprints_requires_no_extra_gate(self):
        result = self.module.scan_evidence(self.artifacts, self.state)
        self.assertEqual(result['status'], 'not-required')

    def test_final_report_and_copied_database_leaks_are_removed(self):
        secret = b'Tc113-synthetic-proof-material-9a'
        self.fingerprints(secret)
        (self.artifacts / 'results.json').write_bytes(b'{"received":"' + secret + b'"}')
        (self.artifacts / 'copied.db').write_bytes(b'\x00prefix' + secret + b'\x00')
        (self.artifacts / 'safe.json').write_text('{"passed":true}')
        result = self.module.scan_evidence(self.artifacts, self.state)
        self.assertEqual(result['status'], 'failed')
        self.assertEqual(result['removed_files'], 2)
        self.assertEqual(sorted(p.name for p in self.artifacts.iterdir()), ['safe.json'])

    def test_similar_unregistered_bytes_are_retained(self):
        self.fingerprints(b'Tc113-synthetic-proof-material-9a')
        safe = self.artifacts / 'results.json'
        safe.write_bytes(b'Tc113-other-synthetic-material-9a')
        self.assertEqual(self.module.scan_evidence(self.artifacts, self.state)['status'], 'passed')
        self.assertTrue(safe.exists())

    def test_malformed_metadata_cannot_leave_unchecked_evidence_uploadable(self):
        (self.state / 'membership-credential-fingerprints.json').write_text('invalid')
        (self.artifacts / 'unverified.log').write_text('synthetic unverified output')
        result = subprocess.run([sys.executable, str(SCRIPT), str(self.artifacts), str(self.state)], capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.artifacts / 'unverified.log').exists())
        receipt = json.loads((self.artifacts / 'credential-evidence.json').read_text())
        self.assertEqual(receipt['status'], 'failed')


if __name__ == '__main__':
    unittest.main()
