#!/usr/bin/env python3
"""Export allowlisted, nonsecret legacy settings for the new deployment wizard."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import sys


def config(root, directory, stack):
    path = root / directory / ('Pulumi.' + stack + '.yaml')
    if path.exists():
        try:
            import yaml
        except ImportError as error:
            raise RuntimeError('Reading local Pulumi YAML requires PyYAML. Run: python -m pip install PyYAML') from error
        try:
            document = yaml.safe_load(path.read_text(encoding='utf-8-sig'))
        except (ValueError, yaml.YAMLError) as error:
            raise RuntimeError('Invalid YAML in ' + str(path)) from error
        if not isinstance(document, dict) or not isinstance(document.get('config'), dict):
            raise RuntimeError('Missing config mapping in ' + str(path))
        # Secure values are mappings, never strings; they are neither decrypted nor exported.
        return {k.split(':')[-1]: v for k, v in document['config'].items()
                if isinstance(k, str) and isinstance(v, str)}
    try:
        result = subprocess.run(['pulumi', 'config', '--json', '--non-interactive', '--cwd', str(root / directory), '--stack', stack], check=True, capture_output=True, text=True)
    except FileNotFoundError as error:
        raise RuntimeError('No local config at ' + str(path) + '; install Pulumi CLI or restore this file from the original checkout') from error
    except subprocess.CalledProcessError as error:
        detail = (error.stderr or '').strip() or 'Pulumi provided no error details'
        raise RuntimeError('Cannot read ' + directory + '/' + stack + ' (Pulumi exit ' + str(error.returncode) + '):\n' + detail + '\nRestore ' + str(path) + ' or select/login to the original state backend and retry.') from error
    raw = json.loads(result.stdout)
    return {k.split(':')[-1]: v['value'] for k, v in raw.items()
            if isinstance(v, dict) and not v.get('secret') and isinstance(v.get('value'), str)}


def collect(root, environment):
    backend = config(root, 'infra', environment)
    shared = config(root, 'infra-index', 'shared')
    values = {}
    for source, target in {'region': 'Region', 'senderDomain': 'SenderDomain', 'senderAddress': 'SenderAddress', 'androidAssociations': 'AndroidAssociations', 'environmentClass': 'EnvironmentClass'}.items():
        if backend.get(source):
            values[target] = backend[source]
    domain = shared.get('domain', '')
    if domain.startswith('index.'):
        values['BaseDomain'] = domain[len('index.'):]
    arn = backend.get('indexApiArn', '')
    if re.fullmatch(r'arn:aws:execute-api:[^:]+:[0-9]{12}:.+', arn):
        values['Account'] = arn.split(':')[4]
    # Only deployment preferences, never bootstrap receipts or credential vaults.
    path = root / '.attestra' / ('deploy.' + environment + '.local.json')
    if path.exists():
        prefs = json.loads(path.read_text())
        if prefs.get('Stack') != environment:
            raise ValueError('saved deployment settings have a different stack')
        for source, target in [('Backend', 'StateBackend'), ('Profile', 'Profile'), ('Region', 'Region')]:
            if isinstance(prefs.get(source), str) and prefs[source]:
                values[target] = prefs[source]
    return values


def write(path, environment, values):
    data = {'Version': 1, 'Environments': {}}
    if path.exists():
        data = json.loads(path.read_text())
        if data.get('Version') != 1 or not isinstance(data.get('Environments'), dict):
            raise ValueError('unsupported or invalid existing export')
    data['Environments'][environment] = values
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix='.wizard-export-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            json.dump(data, stream, indent=2)
            stream.write('\n')
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--environment', default='dev')
    parser.add_argument('--output', type=Path, default=Path.home() / '.attestra' / 'legacy-wizard.json')
    args = parser.parse_args()
    if not re.fullmatch(r'[a-z][a-z0-9-]*', args.environment):
        parser.error('invalid environment')
    values = collect(Path(__file__).resolve().parents[1], args.environment)
    write(args.output, args.environment, values)
    print('Exported ' + ', '.join(sorted(values)) + ' to ' + str(args.output))
    print('No secrets, legacy host overrides, certificates, proof keys or state ownership exported.')


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, ValueError, OSError) as error:
        print('Export failed: ' + str(error), file=sys.stderr)
        sys.exit(1)
