#!/usr/bin/env python3
"""Write the independent-copy receipt that local source-audio review requires.

Run it on the host that keeps the independent copy, over the copy's collection
directory (the one holding <source identifier>/<file name>). It hashes every
regular file and writes the JSON receipt that `vhness review-source-audio
--recovery-receipt` checks. It reads only; it never changes the copy.
"""
import argparse
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import socket
import sys


def sha256(path):
    digest = hashlib.sha256()
    with open(path, 'rb') as handle:
        for block in iter(lambda: handle.read(1 << 20), b''):
            digest.update(block)
    return digest.hexdigest()


def receipt(root, collection, host, now=None):
    root = Path(root).resolve()
    if not root.is_dir():
        raise ValueError(f'{root} is not a directory')
    files = []
    for directory, subdirectories, names in os.walk(root):
        subdirectories.sort()
        for name in sorted(names) + [d for d in subdirectories if (Path(directory) / d).is_symlink()]:
            path = Path(directory) / name
            relative = path.relative_to(root).as_posix()
            if path.is_symlink() or not path.is_file():
                raise ValueError(f'{relative} is not a regular file')
            files.append({'path': relative, 'bytes': path.stat().st_size, 'sha256': sha256(path)})
    if not files:
        raise ValueError(f'{root} holds no files')
    when = (now or datetime.now(timezone.utc)).astimezone(timezone.utc).replace(microsecond=0)
    return {'collection': collection, 'host': host, 'location': str(root),
            'verified_at': when.isoformat().replace('+00:00', 'Z'), 'files': files}


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--collection', required=True, help='engine collection the receipt is for')
    parser.add_argument('--root', required=True, help='collection directory of the independent copy')
    parser.add_argument('--output', required=True, help='new receipt file to write')
    parser.add_argument('--host', default=socket.gethostname(), help='name of this independent host')
    args = parser.parse_args(argv)
    output = Path(args.output)
    if output.exists():
        parser.error(f'{output} exists; write each receipt to a new file')
    data = receipt(args.root, args.collection, args.host)
    output.write_text(json.dumps(data, indent=1) + '\n')
    raw = output.read_bytes()
    print(json.dumps({'receipt': str(output), 'files': len(data['files']),
                      'bytes': sum(f['bytes'] for f in data['files']),
                      'sha256': hashlib.sha256(raw).hexdigest()}))
    return 0


if __name__ == '__main__':
    sys.exit(main())
