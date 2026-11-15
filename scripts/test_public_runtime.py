"""Exercise public HTTP failures and transient recovery through the operator CLI."""
import http.server
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest
from unittest import mock

import public_runtime


class PublicBackupTests(unittest.TestCase):
    def config(self, root):
        return {'root': str(root), 'state': str(root / 'state'), 'project': 'platform',
                'database_container': 'shared-db', 'database_owner': 'legacy_owner',
                'databases': ['legacy',
                              {'database': 'arkhiv', 'name': 'riot-core', 'container': 'riot-db',
                               'project': 'riot', 'owner': 'arkhiv'},
                              {'database': 'arkhiv', 'name': 'anarchist-core', 'container': 'anarchist-db',
                               'project': 'anarchist', 'owner': 'arkhiv'}],
                'checkpoint_budget_bytes': 1000000, 'checkpoint_write_allowance_bytes': 1000,
                'free_space_reserve_bytes': 0,
                'mirror': {'host': 'backup-host', 'path': '/backups', 'legacy_scp': True}}

    def exercise(self, root, failure=None):
        cfg = self.config(root)
        config = root / 'config.json'
        config.write_text(json.dumps(cfg))
        calls = []
        copies = {}

        def run(command, **kwargs):
            calls.append(command)
            if command[:2] == ['docker', 'inspect']:
                project = {'shared-db': 'platform', 'riot-db': 'riot', 'anarchist-db': 'anarchist'}[command[2]]
                if failure == 'ownership' and command[2] == 'anarchist-db':
                    project = 'unrelated'
                return json.dumps([{'Config': {'Labels': {'com.docker.compose.project': project}}}]).encode()
            if 'psql' in command:
                return 'public.documents\n' if 'pg_tables' in command[-1] else '23\n'
            if 'pg_dump' in command:
                kwargs['stdout'].write(('dump of ' + command[2]).encode())
                return subprocess.CompletedProcess(command, 1 if failure == 'dump' else 0)
            if 'pg_restore' in command:
                if failure == 'toc':
                    raise RuntimeError('invalid dump')
                self.assertTrue(kwargs['stdin'].read().startswith(b'dump of '))
                return b'TOC'
            if command[0] == 'scp':
                copies[Path(command[-2]).name] = Path(command[-2])
                return b''
            if command[0] == 'ssh':
                if command[-1].startswith('sha256sum '):
                    name = Path(command[-1].split()[1]).name
                    checksum = 'bad' if failure == 'checksum' else public_runtime.digest(copies[name])
                    return checksum + '  ' + name
                return b''
            raise AssertionError(command)

        with mock.patch.object(sys, 'argv', ['public_runtime.py', 'backup', '--config', str(config)]), \
                mock.patch.object(public_runtime, 'run', side_effect=run), \
                mock.patch.object(public_runtime.subprocess, 'run', side_effect=run), \
                mock.patch('builtins.print'):
            if failure:
                with self.assertRaises(RuntimeError):
                    public_runtime.main()
            else:
                public_runtime.main()
        return calls, copies

    def test_backups_include_separate_same_named_core_databases(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calls, copies = self.exercise(root)
            manifest = json.loads(copies['manifest.json'].read_text())
            entries = manifest['databases']
            self.assertEqual([e['filename'] for e in entries],
                             ['legacy.dump', 'riot-core.dump', 'anarchist-core.dump'])
            self.assertEqual([e['project'] for e in entries], ['platform', 'riot', 'anarchist'])
            self.assertEqual([e['table_counts'] for e in entries], [{'public.documents': 23}] * 3)
            dumps = [c for c in calls if 'pg_dump' in c]
            self.assertEqual([c[2] for c in dumps], ['shared-db', 'riot-db', 'anarchist-db'])
            self.assertEqual([c[c.index('-U') + 1] for c in dumps], ['legacy_owner', 'arkhiv', 'arkhiv'])
            self.assertTrue(all('-O' in c for c in calls if c[0] == 'scp'))
            receipt = json.loads((root / 'state/backup.json').read_text())
            self.assertEqual(receipt['databases'], 3)
            self.assertTrue(receipt['independent_metadata_checksums_match'])
            self.assertFalse(receipt['restore_verified'])

    def test_wrong_project_is_rejected_before_any_dump(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calls, copies = self.exercise(root, 'ownership')
            self.assertFalse(any('pg_dump' in c for c in calls))
            self.assertEqual(copies, {})
            self.assertFalse((root / 'state/backup.json').exists())

    def test_failed_dump_or_toc_never_transfers_a_checkpoint(self):
        for failure in ('dump', 'toc'):
            with self.subTest(failure=failure), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                _, copies = self.exercise(root, failure)
                self.assertEqual(copies, {})
                self.assertFalse(list((root / 'state/checkpoints').glob('*/manifest.json')))
                self.assertFalse((root / 'state/backup.json').exists())

    def test_failed_remote_hash_never_records_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.exercise(root, 'checksum')
            self.assertFalse((root / 'state/backup.json').exists())

    def test_duplicate_or_unsafe_names_rejected(self):
        for name in ('legacy', '../escape', '/absolute'):
            with self.subTest(name=name):
                cfg = self.config(Path('/unused'))
                cfg['databases'][1]['name'] = name
                with self.assertRaises(RuntimeError):
                    public_runtime.backup_targets(cfg)


class PublicMonitorTests(unittest.TestCase):
    def exercise(self, responses):
        class Handler(http.server.BaseHTTPRequestHandler):
            attempts = 0

            def do_GET(self):
                status = responses[min(type(self).attempts, len(responses) - 1)]
                type(self).attempts += 1
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(b'{"audience":"public","total":23}')

            def log_message(self, *_):
                pass

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            docker = root / 'inspect-owner'
            docker.write_text('#!/usr/bin/env python3\nprint(\'[ {"Config":{"Labels":'
                              '{"com.docker.compose.project":"owned"}}} ]\')\n')
            docker.chmod(0o700)
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            try:
                url = f'http://127.0.0.1:{server.server_port}/api/documents'
                cfg = {'root': str(root), 'state': str(root / 'state'), 'project': 'owned',
                       'database_container': 'owned-db', 'docker': [str(docker)],
                       'sites': [{'url': url, 'total': 23, 'equals': {'audience': 'public'}}]}
                config = root / 'config.json'
                config.write_text(json.dumps(cfg))
                result = subprocess.run([sys.executable, str(Path(__file__).with_name('public_runtime.py')),
                                         'monitor', '--config', str(config)], capture_output=True, text=True)
                files = {p.name: json.loads(p.read_text()) for p in (root / 'state').glob('*.json')}
                return result, files, Handler.attempts, url
            finally:
                server.shutdown()
                server.server_close()
                thread.join()

    def test_transient_service_failure_retries_and_checks_contract(self):
        result, files, attempts, _ = self.exercise([503, 200])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(attempts, 2)
        self.assertEqual(files['monitor.json']['sites'][0]['total'], 23)
        self.assertNotIn('monitor-failure.json', files)

    def test_access_failure_is_not_hidden_or_marked_success(self):
        result, files, attempts, url = self.exercise([403])
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(attempts, 1)
        self.assertIn(url, result.stderr)
        self.assertIn('HTTP 403', result.stderr)
        self.assertEqual(files['monitor-failure.json']['http_status'], '403')
        self.assertNotIn('monitor.json', files)
