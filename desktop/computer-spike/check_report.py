#!/usr/bin/env python3
"""Check P0 report consistency locally; this does not authenticate native evidence."""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import sys


def require(condition, message):
    if not condition:
        raise ValueError(message)


def ticks(report, name):
    value = report[name]
    require(type(value) is int or (isinstance(value, str) and value.isdecimal()), f'invalid {name}')
    result = int(value)
    require(result > 0, f'nonpositive {name}')
    return result


def check(path):
    path = Path(path)
    require(path.stat().st_size <= 1_000_000, 'report too large')
    report = json.loads(path.read_text(encoding='utf-8'))
    require(report.get('schema_version') == 1 and report.get('probe') == 'self_target_click', 'unknown report schema/probe')
    require(type(report.get('passed')) is bool, 'missing boolean passed field')
    if not report['passed']:
        return False, {'error': report.get('error', 'native probe did not pass'),
                       'input_attempted': report.get('input_attempted'), 'stop_error': report.get('stop_error')}
    for field in ('input_attempted', 'target_event_observed', 'target_color_changed'):
        require(report.get(field) is True, f'{field} is not true')
    require(not report.get('error') and not report.get('stop_error') and not report.get('cancelled'), 'PASS contains an error or cancellation')
    platform = report.get('platform')
    if platform == 'windows':
        require(report.get('accepted_events') == 3, 'SendInput did not accept all three events')
        names = ('before_present_qpc', 'input_started_qpc', 'input_finished_qpc', 'after_present_qpc')
    elif platform == 'macos':
        require(report.get('input_status') == 'submitted', 'CGEvent was not submitted')
        require(report.get('accessibility_granted') is True and report.get('screen_recording_granted') is True, 'missing native permissions')
        names = ('before_display_ticks', 'input_started_ticks', 'input_finished_ticks', 'after_display_ticks')
    else:
        raise ValueError('unknown platform')
    before, started, finished, after = (ticks(report, name) for name in names)
    # A displayed result can precede the API return; the observed event and blue target
    # provide the additional causal checks. Do not require after > finished.
    require(before < started <= finished and after > started, 'stale or inconsistent capture/input timestamps')
    old, new = report['before_rgb'], report['after_rgb']
    for rgb in (old, new):
        require(isinstance(rgb, list) and len(rgb) == 3 and all(type(v) is int and 0 <= v <= 255 for v in rgb), 'invalid RGB')
    require(old[1] > old[0] + 50 and old[1] > old[2] + 50, 'initial sample is not green')
    require(new[2] > new[0] + 50 and new[2] > new[1] + 30, 'result sample is not blue')
    expected = (report['image']['width'], report['image']['height'])
    require(all(type(v) is int and v > 0 for v in expected) and expected[0] * expected[1] <= 40_000_000, 'invalid image size')
    hashes = {}
    for name in ('before.png', 'after.png'):
        image = path.parent / name
        require(image.stat().st_size <= 200_000_000, f'{name} too large')
        data = image.read_bytes()
        require(len(data) >= 33 and data[:8] == b'\x89PNG\r\n\x1a\n' and data[8:16] == b'\0\0\0\rIHDR', f'{name} has no PNG header')
        require(struct.unpack('>II', data[16:24]) == expected, f'{name} dimensions do not match report')
        hashes[name] = hashlib.sha256(data).hexdigest()
    require(hashes['before.png'] != hashes['after.png'], 'before/after files are identical')
    return True, {'platform': platform, 'sha256': hashes}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('report', type=Path, help='path to report.json; before.png and after.png must be beside it')
    args = parser.parse_args()
    try:
        passed, detail = check(args.report)
    except (OSError, ValueError, KeyError, TypeError) as error:
        print(f'INVALID REPORT: {error}', file=sys.stderr)
        return 2
    print('CONSISTENT PASS (self-reported; inspect screenshots)' if passed else 'NOT PASSED')
    print(json.dumps(detail, ensure_ascii=False, indent=2))
    return 0 if passed else 1


if __name__ == '__main__':
    sys.exit(main())
