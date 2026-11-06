#!/usr/bin/env python3
"""Start the local backend with database settings from the project .env."""
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import quote

ROOT = Path(__file__).resolve().parent.parent


def read_settings():
    path = ROOT / '.env'
    if not path.is_file():
        raise ValueError('Create .env from .env.example before starting.')
    values = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith('#'):
            continue
        key, separator, value = line.partition('=')
        if not separator:
            raise ValueError('Invalid .env line; expected KEY=value.')
        values[key.strip()] = value.strip()
    for key in ('POSTGRES_USER', 'POSTGRES_PASSWORD', 'POSTGRES_DB'):
        if not values.get(key):
            raise ValueError(f'{key} is required in .env.')
    return values


def main():
    try:
        values = read_settings()
    except (OSError, ValueError) as error:
        print(error, file=sys.stderr)
        return 1
    frontend = ROOT / 'frontend' / 'dist'
    if not (frontend / 'index.html').is_file():
        print('Frontend build missing. Run npm ci and npm run build in frontend/.', file=sys.stderr)
        return 1
    env = os.environ.copy()
    user = quote(values['POSTGRES_USER'], safe='')
    password = quote(values['POSTGRES_PASSWORD'], safe='')
    database = quote(values['POSTGRES_DB'], safe='')
    port = values.get('POSTGRES_PORT', '5432')
    if not port.isdecimal() or not 1 <= int(port) <= 65535:
        print('POSTGRES_PORT must be a valid TCP port.', file=sys.stderr)
        return 1
    env['DATABASE_URL'] = f'postgresql://{user}:{password}@127.0.0.1:{port}/{database}'
    env['FRONTEND_DIR'] = str(frontend)
    return subprocess.call(['go', 'run', './cmd/server'], cwd=ROOT / 'backend', env=env)


if __name__ == '__main__':
    raise SystemExit(main())
