import json
from pathlib import Path
import struct
import tempfile
import unittest
import zlib

from check_report import check


def png(rgb):
    def chunk(kind, body):
        return struct.pack('>I', len(body)) + kind + body + struct.pack('>I', zlib.crc32(kind + body))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 1, 1, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(b'\0' + bytes(rgb))) + chunk(b'IEND', b''))


class ReportTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.path = self.root / 'report.json'
        self.report = dict(schema_version=1, probe='self_target_click', platform='windows', passed=True,
                           input_attempted=True, accepted_events=3, target_event_observed=True,
                           target_color_changed=True, before_present_qpc=100, input_started_qpc=200,
                           input_finished_qpc=300, after_present_qpc=250, image={'width': 1, 'height': 1},
                           before_rgb=[20, 180, 60], after_rgb=[20, 60, 210])
        (self.root / 'before.png').write_bytes(png(self.report['before_rgb']))
        (self.root / 'after.png').write_bytes(png(self.report['after_rgb']))

    def run_check(self):
        self.path.write_text(json.dumps(self.report), encoding='utf-8')
        return check(self.path)

    def test_frame_before_api_return_is_valid(self):
        self.assertTrue(self.run_check()[0])

    def test_macos_integer_ticks_as_strings(self):
        self.report.update(platform='macos', input_status='submitted', accessibility_granted=True,
                           screen_recording_granted=True, before_display_ticks='100', input_started_ticks='200',
                           input_finished_ticks='300', after_display_ticks='400')
        self.assertTrue(self.run_check()[0])

    def test_stale_frame_rejected(self):
        self.report['after_present_qpc'] = 199
        with self.assertRaisesRegex(ValueError, 'timestamps'):
            self.run_check()

    def test_input_acceptance_is_not_success(self):
        self.report['target_event_observed'] = False
        with self.assertRaisesRegex(ValueError, 'target_event_observed'):
            self.run_check()

    def test_reused_image_rejected(self):
        (self.root / 'after.png').write_bytes((self.root / 'before.png').read_bytes())
        with self.assertRaisesRegex(ValueError, 'identical'):
            self.run_check()

    def test_missing_image_rejected(self):
        (self.root / 'after.png').unlink()
        with self.assertRaises(FileNotFoundError):
            self.run_check()

    def test_failure_does_not_need_images(self):
        self.report.update(passed=False, error='permission denied', input_attempted=False)
        (self.root / 'before.png').unlink()
        self.assertFalse(self.run_check()[0])

    def test_bad_dimensions_rejected(self):
        self.report['image']['width'] = 2
        with self.assertRaisesRegex(ValueError, 'dimensions'):
            self.run_check()


if __name__ == '__main__':
    unittest.main()
