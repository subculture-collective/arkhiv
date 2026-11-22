from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

from source_audio_receipt import main, receipt


class SourceAudioReceiptTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / 'copy'
        (self.root / 'chapter-001').mkdir(parents=True)
        (self.root / 'chapter-001' / 'Track.. name.mp3').write_bytes(b'first')
        (self.root / 'chapter-001' / '02 Second.mp3').write_bytes(b'second')

    def test_lists_every_file_with_its_digest(self):
        data = receipt(self.root, 'dj-screw-discography-local', 'nuc', datetime(2026, 11, 21, 12, tzinfo=timezone.utc))
        self.assertEqual(data['collection'], 'dj-screw-discography-local')
        self.assertEqual(data['host'], 'nuc')
        self.assertEqual(data['location'], str(self.root.resolve()))
        self.assertEqual(data['verified_at'], '2026-11-21T12:00:00Z')
        self.assertEqual(data['files'], [
            {'path': 'chapter-001/02 Second.mp3', 'bytes': 6, 'sha256': hashlib.sha256(b'second').hexdigest()},
            {'path': 'chapter-001/Track.. name.mp3', 'bytes': 5, 'sha256': hashlib.sha256(b'first').hexdigest()},
        ])

    def test_refuses_links_and_empty_copies(self):
        os.symlink(self.root / 'chapter-001' / '02 Second.mp3', self.root / 'chapter-001' / 'link.mp3')
        with self.assertRaises(ValueError):
            receipt(self.root, 'c', 'nuc')
        empty = Path(self.temp.name) / 'empty'
        empty.mkdir()
        with self.assertRaises(ValueError):
            receipt(empty, 'c', 'nuc')

    def test_writes_a_new_receipt_only(self):
        output = Path(self.temp.name) / 'receipt.json'
        self.assertEqual(main(['--collection', 'c', '--root', str(self.root), '--output', str(output), '--host', 'nuc']), 0)
        self.assertEqual(len(json.loads(output.read_text())['files']), 2)
        with self.assertRaises(SystemExit):
            main(['--collection', 'c', '--root', str(self.root), '--output', str(output), '--host', 'nuc'])


if __name__ == '__main__':
    unittest.main()
