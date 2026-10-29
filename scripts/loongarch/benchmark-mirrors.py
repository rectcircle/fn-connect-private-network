#!/usr/bin/env python3
"""Manual same-runner throughput comparison; never changes package repositories."""
import hashlib
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import xml.etree.ElementTree as ET

RELEASE = 'openEuler-24.03-LTS-SP4'
REPOSITORIES = ('OS/loongarch64', 'everything/loongarch64', 'EPOL/main/loongarch64', 'update/loongarch64')
MIRRORS = {
    'official': 'https://repo.openeuler.org',
    'Huawei Cloud': 'https://mirrors.huaweicloud.com/openeuler',
    'xTom US': 'https://mirrors.xtom.com/openeuler',
    'xTom DE': 'https://mirrors.xtom.de/openeuler',
    'Accum SE': 'https://mirror.accum.se/mirror/openeuler.org',
    'USTC': 'https://mirrors.ustc.edu.cn/openeuler',
    'TUNA': 'https://mirrors.tuna.tsinghua.edu.cn/openeuler',
}
# Same ~10 MiB RPM for every source. SHA256/size read from the official OS
# primary metadata on 2026-10-29; this benchmark does not install or trust packages.
SAMPLE = 'OS/loongarch64/Packages/python3-3.11.6-32.oe2403sp4.loongarch64.rpm'
SAMPLE_SHA256 = '2f0fff68621090b3964fa4fbb4678f01e18ac4f1f1c277bd6e52a8fdcd2a85b0'
SAMPLE_SIZE = 10668213
REFERENCE_METADATA = 'https://repo.openeuler.org/openEuler-24.03-LTS-SP4/OS/loongarch64/repodata/7171c190724c5b0abce7bb1cf6723f8ef4ef9f0a7956a19c7a305f423bd26afd-primary.xml.zst'


def fetch(url, path, seconds):
    result = subprocess.run(['curl', '-4', '--fail', '--silent', '--show-error', '--location',
        '--proto', '=https', '--proto-redir', '=https', '--connect-timeout', '6', '--max-time', str(seconds),
        '--output', str(path), '--write-out', '%{json}', url], capture_output=True, text=True, timeout=seconds+10)
    try:
        record = json.loads(result.stdout)
    except json.JSONDecodeError:
        record = {}
    return {'exitCode': result.returncode, 'httpCode': record.get('http_code'),
            'seconds': record.get('time_total'), 'bytesPerSecond': record.get('speed_download', 0),
            'downloadedBytes': record.get('size_download', 0), 'effectiveURL': record.get('url_effective'),
            'remoteIP': record.get('remote_ip'), 'error': result.stderr.strip()}


def valid_metadata(path):
    try:
        root = ET.parse(path).getroot()
        return root.tag == '{http://linux.duke.edu/metadata/repo}repomd' and bool(list(root))
    except (ET.ParseError, OSError):
        return False


def valid_sample(path):
    return (path.is_file() and path.stat().st_size == SAMPLE_SIZE
            and hashlib.sha256(path.read_bytes()).hexdigest() == SAMPLE_SHA256)


def benchmark():
    out = Path('dist/openeuler-mirror-benchmark')
    out.mkdir(parents=True, exist_ok=True)
    report = {'release': RELEASE, 'architecture': 'loongarch64', 'sample': SAMPLE,
              'sampleSHA256': SAMPLE_SHA256, 'sampleBytes': SAMPLE_SIZE, 'referenceMetadata': REFERENCE_METADATA,
              'runner': {key: os.environ.get(key) for key in ('RUNNER_OS', 'RUNNER_ARCH', 'ImageOS', 'ImageVersion')},
              'notes': ['same runner; sequential sources; two full RPM downloads; no Actions download cache',
                        'checksums and repository availability are not installation/signature acceptance',
                        'a single runner location/time cannot prove global performance'], 'mirrors': []}
    reference = {}
    with tempfile.TemporaryDirectory() as temporary:
        path = Path(temporary)/'download'
        for name, base in MIRRORS.items():
            print('Testing mirror:', name, base, flush=True)
            row = {'name': name, 'baseURL': base, 'repositories': {}, 'downloads': []}
            for repository in REPOSITORIES:
                path.unlink(missing_ok=True)
                result = fetch(f'{base}/{RELEASE}/{repository}/repodata/repomd.xml', path, 12)
                result['valid'] = result['exitCode'] == 0 and valid_metadata(path)
                if result['valid']:
                    result['sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
                    if name == 'official': reference[repository] = result['sha256']
                result['matchesOfficial'] = result['valid'] and result.get('sha256') == reference.get(repository)
                row['repositories'][repository] = result
                print(' ', repository, result['httpCode'], 'valid:', result['valid'], flush=True)
            if all(value['valid'] for value in row['repositories'].values()):
                for attempt in range(2):
                    path.unlink(missing_ok=True)
                    result = fetch(f'{base}/{RELEASE}/{SAMPLE}', path, 60)
                    result['verified'] = result['exitCode'] == 0 and valid_sample(path)
                    row['downloads'].append(result)
                    print(' ', 'RPM', attempt+1, 'verified:', result['verified'], 'MiB/s:', round(result['bytesPerSecond']/1024**2, 2), flush=True)
            speeds = [r['bytesPerSecond'] for r in row['downloads'] if r['verified']]
            row['medianMiBPerSecond'] = statistics.median(speeds)/1024**2 if speeds else None
            row['comparable'] = len(speeds) == 2 and all(r['matchesOfficial'] for r in row['repositories'].values())
            report['mirrors'].append(row)
            (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    lines = ['# openEuler LoongArch mirror benchmark', '',
             'Same GitHub runner; two complete ~10 MiB RPM downloads per available source. No repository configuration changed.', '',
             '| Source | Repositories | Metadata matching official | Verified RPMs | Median MiB/s |', '| --- | --- | --- | --- | --- |']
    for row in report['mirrors']:
        speed = '-' if row['medianMiBPerSecond'] is None else f"{row['medianMiBPerSecond']:.2f}"
        lines.append(f"| {row['name']} | {sum(r['valid'] for r in row['repositories'].values())}/4 | {sum(r['matchesOfficial'] for r in row['repositories'].values())}/4 | {sum(r['verified'] for r in row['downloads'])}/2 | {speed} |")
    lines += ['', 'Only complete matching-metadata results are comparable. Missing/outdated mirrors and download failures remain in results.json.',
              'No packages are installed. A faster source still needs signed-package and installation acceptance before adoption.']
    summary = '\n'.join(lines)+'\n'
    (out/'summary.md').write_text(summary)
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(os.environ['GITHUB_STEP_SUMMARY'], 'a') as stream: stream.write(summary)
    if not any(row['comparable'] for row in report['mirrors']):
        raise RuntimeError('No complete comparable source; inspect availability/download errors')


if __name__ == '__main__':
    benchmark()
