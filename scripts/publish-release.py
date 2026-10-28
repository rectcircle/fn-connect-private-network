#!/usr/bin/env python3
"""Upload/retry drafts, verify downloaded bytes, optionally make the release public."""
import argparse
import hashlib
import json
import pathlib
from pathlib import PurePosixPath
import re
import subprocess
import tempfile
from linux_support import release_packages, validate_loongarch_publication
from android_release import validate_inventory as validate_android_inventory
from ios_release import validate_inventory as validate_ios_inventory, verify_ipa
from harmonyos_release import validate_inventory as validate_harmonyos_inventory, verify_hap
from rpm_signing import signing_identity
from release_assets import assets, download_asset, upload_asset, find_release


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def api_json(endpoint, payload):
    with tempfile.TemporaryDirectory(prefix='fncpn-release-request-') as temporary:
        request = pathlib.Path(temporary) / 'request.json'
        request.write_text(json.dumps(payload), encoding='utf-8')
        return json.loads(run('gh', 'api', endpoint, '--method', 'PATCH' if '/releases/' in endpoint else 'POST',
                              '--input', str(request)))


def remote_tag(repository, tag):
    # Listing distinguishes an absent ref from authentication/network failures.
    refs = json.loads(run('gh', 'api', f'repos/{repository}/git/matching-refs/tags/{tag}'))
    ref = next((r for r in refs if r['ref'] == 'refs/tags/' + tag), None)
    if ref is None:
        return None
    obj = ref['object']
    while obj['type'] == 'tag':
        obj = json.loads(run('gh', 'api', f"repos/{repository}/git/tags/{obj['sha']}"))['object']
    return obj['sha']


def release_notes(version):
    """Use the version's changelog entry, or Unreleased while preparing a draft."""
    changelog = pathlib.Path('CHANGELOG.md').read_text(encoding='utf-8')
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
    if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', version):
        raise SystemExit('Invalid product version; expected MAJOR.MINOR.PATCH')
    tag = 'v' + version
    manifest_name = f'release-{version}.json'
    sums_name = f'SHA256SUMS-{version}'
    manifest = json.loads((directory / manifest_name).read_text())
    names = [a['file'] for a in manifest['artifacts']] + [manifest_name, sums_name]
    if len(names) != len(set(names)) or any(pathlib.Path(n).name != n for n in names):
        raise SystemExit('Invalid release asset names')
    sources = {}
    for artifact in manifest['artifacts']:
        relative = PurePosixPath(artifact.get('path', artifact['file']))
        if ('\\' in str(relative) or ':' in str(relative) or relative.is_absolute() or
                not relative.parts or any(part in ('', '.', '..') for part in relative.parts)):
            raise SystemExit('Invalid release artifact path: ' + str(relative))
        source = (directory / pathlib.Path(*relative.parts)).resolve()
        # `gh release download` flattens remote assets into its target directory;
        # accept that layout when verifying/re-publishing an existing draft.
        downloaded_asset = (directory / artifact['file']).resolve()
        if not source.is_file() and downloaded_asset.is_file():
            source = downloaded_asset
        if not source.is_relative_to(directory):
            raise SystemExit('Release artifact path escapes inventory directory: ' + str(relative))
        sources[artifact['file']] = source
    required_windows_assets = {
        f'fncpn-{version}-windows-arm64-unsigned.msi',
        f'fncpn-{version}-windows-x64-unsigned.msi',
    }
    if not required_windows_assets.issubset(sources):
        missing = ', '.join(sorted(required_windows_assets - set(sources)))
        raise SystemExit('Release inventory is missing required Windows MSI assets: ' + missing)
    if tuple(map(int, version.split('.'))) >= (1, 1, 2):
        required_linux_assets = {name for _,_,_,name in release_packages(version)}
        if not required_linux_assets.issubset(sources):
            raise SystemExit('Release inventory is missing required Linux Core/UI assets')
        if manifest.get('build', {}).get('rpmSigning') != signing_identity():
            raise SystemExit('Release inventory is missing matching RPM signing evidence')
    validate_android_inventory(manifest, version)
    validate_ios_inventory(manifest, version)
    validate_harmonyos_inventory(manifest, version)
    for name in sources:
        if name.endswith(".hap"):
            verify_hap(sources[name], version, manifest["build"]["harmonyos"]["buildNumber"])
        if name.endswith(".ipa"):
            verify_ipa(sources[name], version, manifest["build"]["ios"]["buildNumber"])
    commit = run('git', 'rev-parse', 'HEAD')
    if manifest['version'] != version or manifest['commit'] != commit:
        raise SystemExit('Release inventory does not match checkout')
    if run('git', 'status', '--porcelain'):
        raise SystemExit('Release requires a clean checkout')
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise SystemExit('Candidate must be a full commit SHA')
    tagged_commit = remote_tag(repository, tag)
    if tagged_commit is not None and tagged_commit != commit:
        raise SystemExit('Remote release tag does not match checkout')
    expected_sums = ''.join(f"{a['sha256']}  {a['file']}\n" for a in manifest['artifacts'])
    if (directory / sums_name).read_text() != expected_sums:
        raise SystemExit('Checksum inventory does not match manifest')
    for artifact in manifest['artifacts']:
        if hashlib.sha256(sources[artifact['file']].read_bytes()).hexdigest() != artifact['sha256']:
            raise SystemExit('Artifact checksum mismatch: ' + artifact['file'])
    if make_public:
        try:
            validate_loongarch_publication(manifest)
        except ValueError as error:
            raise SystemExit(str(error))
    # Distinguish not-found from auth/network errors before creating a release.
    release = find_release(repository, tag, run)
    existing_release = release is not None
    if release is None and make_public:
        raise SystemExit("Publication requires an existing release draft")
    if release is None or release['draft']:
        if not make_public and tagged_commit is not None:
            raise SystemExit('Draft builds require an absent version tag')
        if make_public and release['target_commitish'] != commit:
            raise SystemExit('Draft candidate commit does not match checkout')
    elif tagged_commit != commit:
        raise SystemExit('Public release requires its immutable version tag')
    if release is None:
        release = api_json(f'repos/{repository}/releases', {
            'tag_name': tag, 'target_commitish': commit, 'draft': True,
            'name': f'FnCPN {version}', 'body': release_notes(version)})
    draft = release['draft']
    if draft and not make_public:
        if existing_release:
            api_json(f"repos/{repository}/releases/{release['id']}", {
                'tag_name': tag, 'target_commitish': commit, 'body': release_notes(version)})
        remote_assets = assets(repository, release['id'], run)
        legacy = {f'fncpn-{version}-harmonyos-arm64-release-unsigned.hap',
                  f'FnCPN-{version}-arm64-unsigned.pkg',
                  f'FnCPN-{version}-arm64-unsigned.msi', f'FnCPN-{version}-x64-unsigned.msi',
                  f'fncpn-{version}-arm.fpk', f'fncpn-{version}-x86.fpk'}
        for asset in remote_assets:
            if asset['name'] in set(names) | legacy:
                run('gh', 'api', f"repos/{repository}/releases/assets/{asset['id']}", '--method', 'DELETE')
        for name in names:
            upload_asset(repository, release['id'], name, sources.get(name, directory / name), run)
    # Never mutate a public release. Re-runs succeed only if every byte matches.
    with tempfile.TemporaryDirectory(prefix='fncpn-release-verify-') as temporary:
        for asset in assets(repository, release['id'], run):
            download_asset(repository, asset, temporary)
        downloaded = pathlib.Path(temporary)
        if {p.name for p in downloaded.iterdir()} != set(names):
            raise SystemExit('Unexpected or missing remote assets; release remains unpublished')
        for name in names:
            expected_source = sources.get(name, directory / name)
            if (downloaded / name).read_bytes() != expected_source.read_bytes():
                raise SystemExit('Remote asset differs: ' + name)
    if draft and make_public:
        # Create the public identity only after all original bytes were verified.
        # A retry after tag creation may reuse the same ref, never move it.
        if tagged_commit is None:
            api_json(f'repos/{repository}/git/refs', {'ref': 'refs/tags/' + tag, 'sha': commit})
        if remote_tag(repository, tag) != commit:
            raise SystemExit('Publication tag does not match verified candidate')
        release = api_json(f"repos/{repository}/releases/{release['id']}", {'tag_name': tag, 'draft': False, 'make_latest': 'true'})
    print(release['html_url'])


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory')
    parser.add_argument('--repo', default='rectcircle/fn-connect-private-network')
    parser.add_argument('--publish', action='store_true')
    args = parser.parse_args()
    publish(args.directory, args.repo, args.publish)
