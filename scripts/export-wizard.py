#!/usr/bin/env python3
"""Export allowlisted, nonsecret legacy settings for the new deployment wizard."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def config(root, directory, stack):
    result = subprocess.run(['pulumi', 'config', '--json', '--cwd', str(root / directory), '--stack', stack], check=True, capture_output=True, text=True)
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
    main()
