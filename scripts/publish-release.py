#!/usr/bin/env python3
"""Upload/retry drafts, verify downloaded bytes, optionally make the release public."""
import argparse
import hashlib
import json
import pathlib
import re
import subprocess
import tempfile


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def release_notes(version):
    """Use the version's changelog entry, or Unreleased while preparing a draft."""
    changelog = pathlib.Path('CHANGELOG.md').read_text()
    sections = {}
    for match in re.finditer(r'^## \[([^\]]+)\][^\n]*\n(.*?)(?=^## |\Z)', changelog, re.M | re.S):
        sections[match.group(1)] = match.group(2).strip()
    notes = sections.get(version, sections.get('Unreleased', ''))
    if not notes:
        raise SystemExit('Missing release notes in CHANGELOG.md')
    return notes + '\n'


def publish(directory, repository, make_public=False):
    directory = pathlib.Path(directory).resolve()
    version = pathlib.Path('internal/version/VERSION').read_text().strip()
    tag = 'v' + version
    manifest_name = f'release-{version}.json'
    sums_name = f'SHA256SUMS-{version}'
    manifest = json.loads((directory / manifest_name).read_text())
    commit = run('git', 'rev-parse', 'HEAD')
    if manifest['version'] != version or manifest['commit'] != commit:
        raise SystemExit('Release inventory does not match checkout')
    if run('git', 'status', '--porcelain'):
        raise SystemExit('Release requires a clean checkout')
    if run('git', 'rev-parse', tag + '^{commit}') != commit:
        raise SystemExit('Release tag does not match checkout')
    remote = json.loads(run('gh', 'api', f'repos/{repository}/git/ref/tags/{tag}'))['object']
    while remote['type'] == 'tag':
        remote = json.loads(run('gh', 'api', f"repos/{repository}/git/tags/{remote['sha']}"))['object']
    if remote['sha'] != commit:
        raise SystemExit('Remote release tag does not match checkout')
    names = [a['file'] for a in manifest['artifacts']] + [manifest_name, sums_name]
    if len(names) != len(set(names)) or any(pathlib.Path(n).name != n for n in names):
        raise SystemExit('Invalid release asset names')
    expected_sums = ''.join(f"{a['sha256']}  {a['file']}\n" for a in manifest['artifacts'])
    if (directory / sums_name).read_text() != expected_sums:
        raise SystemExit('Checksum inventory does not match manifest')
    for artifact in manifest['artifacts']:
        if hashlib.sha256((directory / artifact['file']).read_bytes()).hexdigest() != artifact['sha256']:
            raise SystemExit('Artifact checksum mismatch: ' + artifact['file'])
    # Distinguish not-found from auth/network errors before creating a release.
    releases = json.loads(run('gh', 'api', '--paginate', '--slurp', f'repos/{repository}/releases'))
    release = next((r for page in releases for r in page if r['tag_name'] == tag), None)
    if release is None:
        with tempfile.TemporaryDirectory(prefix='fncpn-release-notes-') as temporary:
            notes = pathlib.Path(temporary) / 'notes.md'
            notes.write_text(release_notes(version))
            args = ['gh', 'release', 'create', tag, '--repo', repository, '--verify-tag', '--draft',
                    '--title', f'FnCPN {version}', '--notes-file', str(notes)]
            if '-rc.' in version:
                args.append('--prerelease')
            run(*args)
        release = json.loads(run('gh', 'release', 'view', tag, '--repo', repository, '--json', 'isDraft,assets'))
        draft = release['isDraft']
    else:
        draft = release['draft']
    if draft:
        run('gh', 'release', 'upload', tag, '--repo', repository, '--clobber',
            *(str(directory / name) for name in names))
    # Never mutate a public release. Re-runs succeed only if every byte matches.
    with tempfile.TemporaryDirectory(prefix='fncpn-release-verify-') as temporary:
        run('gh', 'release', 'download', tag, '--repo', repository, '--dir', temporary)
        downloaded = pathlib.Path(temporary)
        if {p.name for p in downloaded.iterdir()} != set(names):
            raise SystemExit('Unexpected or missing remote assets; release remains unpublished')
        for name in names:
            if (downloaded / name).read_bytes() != (directory / name).read_bytes():
                raise SystemExit('Remote asset differs: ' + name)
    if draft and make_public:
        run('gh', 'release', 'edit', tag, '--repo', repository, '--draft=false',
            '--latest=false' if '-rc.' in version else '--latest')
    print(run('gh', 'release', 'view', tag, '--repo', repository, '--json', 'url', '--jq', '.url'))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory')
    parser.add_argument('--repo', default='rectcircle/fn-connect-private-network')
    parser.add_argument('--publish', action='store_true')
    args = parser.parse_args()
    publish(args.directory, args.repo, args.publish)
