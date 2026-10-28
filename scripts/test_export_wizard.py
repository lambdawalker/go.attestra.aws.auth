import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('exporter', Path(__file__).with_name('export-wizard.py'))
exporter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(exporter)


class ExportTests(unittest.TestCase):
    def test_allowlist_and_environment_merge(self):
        backend = {'senderDomain': 'dev.info.example.com', 'senderAddress': 'verify@dev.info.example.com', 'region': 'us-east-2', 'proofKey': 'never-export', 'apiDomain': 'legacy.example.com', 'indexApiArn': 'arn:aws:execute-api:us-east-2:123456789012:api'}
        with tempfile.TemporaryDirectory() as tmp, patch.object(exporter, 'config', side_effect=[backend, {'domain': 'index.example.com'}]):
            root = Path(tmp)
            values = exporter.collect(root, 'dev')
            self.assertEqual(values['SenderDomain'], 'dev.info.example.com')
            self.assertEqual(values['BaseDomain'], 'example.com')
            self.assertEqual(values['Account'], '123456789012')
            path = root / 'home' / 'legacy-wizard.json'
            exporter.write(path, 'qa', {'SenderDomain': 'qa.info.example.com'})
            exporter.write(path, 'dev', values)
            data = path.read_text()
            self.assertNotIn('never-export', data)
            self.assertNotIn('legacy.example.com', data)
            self.assertIn('qa', json.loads(data)['Environments'])
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_config_never_requests_decrypted_secrets(self):
        class Result:
            stdout = json.dumps({'aws:region': {'value': 'us-east-2'}, 'project:senderDomain': {'value': 'example.com', 'secret': False}, 'project:proofKey': {'value': 'secret', 'secret': True}})
        with patch.object(exporter.subprocess, 'run', return_value=Result()) as run:
            result = exporter.config(Path('/repo'), 'infra', 'dev')
            self.assertNotIn('proofKey', result)
            self.assertNotIn('--show-secrets', run.call_args.args[0])


if __name__ == '__main__':
    unittest.main()
