#!/usr/bin/env python3
"""Mirror immutable GitHub Releases with the pinned Quark CLI; never install its Skill."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import urllib.request
import zipfile
from release_assets import find_release, download_release
from linux_support import release_packages
from ios_release import packages as ios_packages, validate_inventory as validate_ios_inventory, verify_ipa
from harmonyos_release import packages as harmonyos_packages, validate_inventory as validate_harmonyos_inventory, verify_hap
from android_release import packages as android_packages, validate_inventory as validate_android_inventory

REPOSITORY = 'rectcircle/fn-connect-private-network'
CLI_VERSION = '1.0.24-29460f9'
CLI_URL = 'https://pdds.quark.cn/download/stfile/fflliomikfgjfhnhk/quarkclouddrive-1.0.24.zip'
CLI_SHA256 = 'a4847bb6e4d6bafb21eb28ba0d9b34b643b6ba5aed44d1a25c86c7f5ba3efb8a'
RELEASES_FID = '47a5e8ca554b4d67ac7c058df6b41caf'
FNCPN_FID = '87f2e2db011840a998f847ec3bc7f0a5'
DEFAULT_CLI = Path.home() / '.local/share/quark-cli/1.0.24/quark-drive.cjs'
DEFAULT_AUTH = DEFAULT_CLI.parent.parent / 'codex/config.json'


def failure_codes(result):
    """Allowlist numeric result codes and known network errors, never vendor text."""
    codes = set()
    raw = (result.stdout or '') + '\n' + (result.stderr or '')
    for line in raw.splitlines():
        try:
            row = json.loads(line)
        except ValueError:
            continue
        if isinstance(row, dict):
            for key in ('code', 'status', 'errno', 'statusCode'):
                value = row.get(key)
                if type(value) is int:
                    codes.add(f'{key}={value}')
    for code in ('ENOENT', 'EACCES', 'ETIMEDOUT', 'ECONNRESET', 'ENOTFOUND', 'ECONNREFUSED',
                 'EAI_AGAIN', 'CERT_HAS_EXPIRED', 'UNABLE_TO_VERIFY_LEAF_SIGNATURE'):
        if re.search(r'\b' + code + r'\b', raw):
            codes.add(code)
    return ', '.join(sorted(codes)) or 'no safe error code returned'


def checked(args, *, input=None, env=None, cwd=None, timeout=300, operation=None):
    # Never print subprocess output: vendor errors can contain credentials or signed URLs.
    try:
        result = subprocess.run(args, input=input, text=True, capture_output=True,
                                env=env, cwd=cwd, timeout=timeout)
    except subprocess.TimeoutExpired:
        raise RuntimeError(f'{operation or args[0]} timed out') from None
    if result.returncode:
        raise RuntimeError(f'{operation or args[0]} failed (exit {result.returncode}; '
                           f'{failure_codes(result)}); no credentials logged')
    return result.stdout


def gh(*args, **kwargs):
    return checked(['gh', *args], **kwargs)


def version_from_tag(tag):
    if not re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', tag):
        raise ValueError('Expected vMAJOR.MINOR.PATCH')
    return tag[1:]


def minimal_auth(config):
    """Export the selected Codex account only, never unrelated accounts or browser state."""
    uid = config.get('agent_auth', {}).get('codex', {}).get('userId')
    if not isinstance(uid, str) or not uid or not isinstance(config.get(uid), dict):
        raise ValueError('Missing Codex authorization; log in locally first')
    account = config[uid]
    for key in ('accessToken', 'refreshToken', 'userId'):
        if not isinstance(account.get(key), str) or not account[key]:
            raise ValueError('Incomplete Quark authorization')
    if account['userId'] != uid or not isinstance(config.get('deviceId'), str) or not config['deviceId']:
        raise ValueError('Invalid Quark identity')
    return {'deviceId': config['deviceId'], 'currentUserId': uid,
            'agent_auth': {'codex': {'userId': uid, 'login_time': 0}},
            uid: {k: account[k] for k in ('accessToken', 'refreshToken', 'clientToken', 'userId', 'update_time')
                  if k in account}}


def write_auth(path, config):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)
    # Set mode before writing any bytes, including when replacing an existing file.
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, 'w') as stream:
        os.fchmod(stream.fileno(), 0o600)
        json.dump(minimal_auth(config), stream)


def install_cli(root):
    with urllib.request.urlopen(CLI_URL, timeout=60) as response:
        archive = response.read()
    if hashlib.sha256(archive).hexdigest() != CLI_SHA256:
        raise ValueError('Quark CLI archive checksum mismatch')
    scripts = root / 'scripts'
    scripts.mkdir(parents=True)
    with zipfile.ZipFile(io.BytesIO(archive)) as bundle:
        # Deliberately do not extract SKILL.md, references or install/update scripts.
        for name in ('quark-drive.cjs', 'hash-worker.cjs'):
            (scripts / name).write_bytes(bundle.read('scripts/' + name))
    entry = scripts / 'quark-drive.cjs'
    if checked(['node', str(entry), '--version']).strip() != CLI_VERSION:
        raise ValueError('Unexpected Quark CLI version')
    return entry


class Quark:
    def __init__(self, entry, work):
        self.entry, self.work = Path(entry), Path(work)
        self.work.mkdir(parents=True, exist_ok=True)
        self.env = dict(os.environ, CODEX_CI='1')
        # The exported secret is restored to the CLI's Codex channel, including on Linux.

    def call(self, *args):
        print(f'Quark operation: {args[0]}', flush=True)
        output = checked(['node', str(self.entry), *map(str, args)], env=self.env,
                         cwd=self.work, timeout=1800, operation=f'Quark {args[0]}')
        results = []
        for line in output.splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if isinstance(row, dict) and row.get('type') == 'result':
                results.append(row)
        if not results or results[-1].get('code') != 0:
            raise RuntimeError(f'Quark {args[0]} did not return success; reauthorize locally if needed')
        return results[-1].get('data', {})

    def browse(self, fid):
        result = self.call('browse', '--parent-fid', fid, '--all', '--task-dir', self.work)
        path = Path(result['file_path']).resolve()
        if not path.is_relative_to(self.work.resolve()):
            raise ValueError('Unexpected browse result path')
        rows = [json.loads(line) for line in path.read_text().splitlines()]
        if result.get('hasMore') or len(rows) != result['count']:
            raise ValueError('Incomplete Quark directory listing')
        return rows

    def folder(self, parent, name, create=False):
        matches = [f for f in self.browse(parent) if f['filename'] == name]
        if len(matches) > 1 or (matches and str(matches[0]['file_type']) != '0'):
            raise ValueError('Duplicate name or non-directory at mirror destination')
        if matches:
            return matches[0]['fid']
        if create:
            return self.call('create-folder', '--parent-fid', parent, '--dir-path', name)['fid']
        return None


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def inventory(directory, tag, release, commit):
    version = version_from_tag(tag)
    manifest_name, sums_name = f'release-{version}.json', f'SHA256SUMS-{version}'
    manifest = json.loads((directory / manifest_name).read_text())
    if manifest.get('version') != version or manifest.get('commit') != commit or manifest.get('channel') != 'stable':
        raise ValueError('Release manifest version/commit/channel mismatch')
    artifacts = manifest['artifacts']
    required = {f'fncpn-{version}-{system}-{arch}{suffix}'
                for system, suffix in [('macos', '-unsigned.pkg'), ('windows', '-unsigned.msi'), ('fnos', '.fpk')]
                for arch in ('arm64', 'x64')}
    if tuple(map(int, version.split('.'))) >= (1, 1, 2):
        required.update(name for _, _, _, name in release_packages(version))
    required.update(android_packages(version))
    required.update(ios_packages(version))
    required.update(harmonyos_packages(version))
    validate_harmonyos_inventory(manifest, version)
    for name in harmonyos_packages(version):
        verify_hap(directory / name, version, manifest["build"]["harmonyos"]["buildNumber"])
    validate_ios_inventory(manifest, version)
    for name in ios_packages(version):
        verify_ipa(directory / name, version, manifest["build"]["ios"]["buildNumber"])
    validate_android_inventory(manifest, version)
    if len(artifacts) != len(required) or {a['file'] for a in artifacts} != required:
        raise ValueError('Release installers do not match the supported platform inventory')
    names = required | {manifest_name, sums_name}
    assets = release['assets']
    if len(assets) != len(names) or {a['name'] for a in assets} != names:
        raise ValueError('Unexpected or missing GitHub Release assets')
    if {p.name for p in directory.iterdir()} != names:
        raise ValueError('Unexpected or missing downloaded assets')
    for artifact in artifacts:
        if digest(directory / artifact['file']) != artifact['sha256']:
            raise ValueError('Installer checksum mismatch: ' + artifact['file'])
    expected_sums = ''.join(f"{a['sha256']}  {a['file']}\n" for a in artifacts)
    if (directory / sums_name).read_text() != expected_sums:
        raise ValueError('Checksum file differs from manifest')
    for asset in assets:
        if asset['size'] != (directory / asset['name']).stat().st_size:
            raise ValueError('GitHub asset size mismatch')
        # GitHub supplies SHA256 for current releases, including the inventory files.
        if asset.get('digest') != 'sha256:' + digest(directory / asset['name']):
            raise ValueError('GitHub asset digest missing or mismatched')
    return {name: directory / name for name in names}


def readback(quark, rows, sources, output):
    for row in rows:
        name = row['filename']
        if name not in sources or str(row['file_type']) != '1':
            raise ValueError('Unexpected file in version directory')
        expected = sources[name]
        if row['size'] != expected.stat().st_size:
            raise ValueError('Remote file size differs: ' + name)
        target = output / name
        if target.exists():
            target.unlink()  # Local scratch file only; never delete or overwrite remote assets.
        quark.call('download', '--fid', row['fid'], '--output-dir', output)
        if not target.is_file() or digest(target) != digest(expected):
            raise ValueError('Remote file checksum differs: ' + name)
        print('Verified: ' + name, flush=True)


def unique_rows(rows):
    names = [r['filename'] for r in rows]
    if len(names) != len(set(names)):
        raise ValueError('Duplicate files in version directory')
    return set(names)


def sync(quark, tag, sources, output, parent=FNCPN_FID, staging_parent=RELEASES_FID):
    """Verify public versions without mutation; resume incomplete uploads outside the shared tree."""
    final = quark.folder(parent, tag)
    if final:
        rows = quark.browse(final)
        if unique_rows(rows) != set(sources):
            raise ValueError('Existing public mirror has a different inventory; refusing to modify it')
        readback(quark, rows, sources, output)
        return
    staging = quark.folder(staging_parent, 'fncpn-staging', create=True)
    candidate = quark.folder(staging, tag, create=True)
    rows = quark.browse(candidate)
    existing = unique_rows(rows)
    readback(quark, rows, sources, output)  # Fail on conflict BEFORE any upload.
    missing = sorted(set(sources) - existing)
    if missing:
        quark.call('upload', *(sources[name] for name in missing), '--parent-fid', candidate)
    rows = quark.browse(candidate)
    if unique_rows(rows) != set(sources):
        raise ValueError('Uploaded inventory is incomplete')
    readback(quark, [r for r in rows if r['filename'] in missing], sources, output)
    if quark.folder(parent, tag):
        raise ValueError('Destination appeared during upload; refusing to overwrite')
    quark.call('move', candidate, '--target-fid', parent)
    final = quark.folder(parent, tag)
    if not final or unique_rows(quark.browse(final)) != set(sources):
        raise ValueError('Could not verify final mirror directory')


def mirror(tag, repo):
    version_from_tag(tag)
    release = json.loads(gh('release', 'view', tag, '--repo', repo,
                            '--json', 'tagName,isDraft,isPrerelease,assets'))
    if release['tagName'] != tag or release['isDraft'] or release['isPrerelease']:
        raise ValueError('Only public stable releases can be mirrored')
    remote = json.loads(gh('api', f'repos/{repo}/git/ref/tags/{tag}'))['object']
    for _ in range(5):
        if remote['type'] == 'commit':
            break
        if remote['type'] != 'tag':
            raise ValueError('Invalid release tag')
        remote = json.loads(gh('api', f'repos/{repo}/git/tags/{remote["sha"]}'))['object']
    if remote['type'] != 'commit':
        raise ValueError('Release tag does not resolve to a commit')
    auth = minimal_auth(json.loads(os.environ.pop('QUARK_AUTH')))
    with tempfile.TemporaryDirectory(prefix='fncpn-quark-') as temporary:
        root = Path(temporary)
        sources_dir, output = root / 'source', root / 'readback'
        sources_dir.mkdir()
        output.mkdir()
        gh('release', 'download', tag, '--repo', repo, '--dir', str(sources_dir))
        sources = inventory(sources_dir, tag, release, remote['sha'])
        entry = install_cli(root / 'runtime')
        write_auth(root / 'runtime/codex/config.json', auth)
        quark = Quark(entry, root / 'work')
        quark.call('get-user-info')
        sync(quark, tag, sources, output)
    print(f'Verified mirror: /releases/fncpn/{tag} ({len(sources)} files)')


def dispatch(tag, repo, mode, cli, auth_path):
    version_from_tag(tag)
    cli, auth_path = cli.expanduser().resolve(), auth_path.expanduser().resolve()
    # Refresh the real local authorization before exporting it; do not log its content.
    if auth_path != cli.parent.parent / 'codex/config.json':
        raise ValueError('--auth-config must match the CLI root/codex/config.json')
    if checked(['node', str(cli), '--version']).strip() != CLI_VERSION:
        raise ValueError('Use the pinned Quark CLI version before exporting authorization')
    with tempfile.TemporaryDirectory(prefix='fncpn-quark-auth-check-') as temporary:
        Quark(cli, temporary).call('get-user-info')
    auth = minimal_auth(json.loads(auth_path.read_text()))
    release = find_release(repo, tag)
    if release is None:
        raise ValueError('Release not found')
    if release['prerelease'] or (mode == 'mirror' and release['draft']):
        raise ValueError('Mirror retries require a public stable release')
    # stdin keeps authorization out of argv, shell history and workflow inputs.
    gh('secret', 'set', 'QUARK_AUTH', '--repo', repo, input=json.dumps(auth))
    workflow = 'release.yml' if mode == 'publish' else 'mirror-quark.yml'
    args = ['workflow', 'run', workflow, '--repo', repo, '--ref', 'main']
    if mode == 'publish':
        with tempfile.TemporaryDirectory(prefix='fncpn-candidate-') as temporary:
            name = f'release-{version_from_tag(tag)}.json'
            download_release(repo, tag, temporary, name=name)
            candidate = json.loads((Path(temporary) / name).read_text())
        commit = candidate['commit']
        if candidate['version'] != version_from_tag(tag) or not re.fullmatch(r'[0-9a-f]{40}', commit):
            raise ValueError('Invalid draft candidate identity')
        args += ['-f', f'version={version_from_tag(tag)}', '-f', f'commit={commit}', '-f', 'publish=true']
    else:
        args += ['-f', f'tag={tag}']
    gh(*args)
    print(f'Dispatched {workflow} for {tag}; credentials were sent via QUARK_AUTH Secret.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    for command in ('mirror', 'dispatch'):
        p = sub.add_parser(command)
        p.add_argument('tag')
        p.add_argument('--repo', default=REPOSITORY)
        if command == 'dispatch':
            p.add_argument('--mode', choices=('publish', 'mirror'), default='publish')
            p.add_argument('--cli', type=Path, default=DEFAULT_CLI)
            p.add_argument('--auth-config', type=Path, default=DEFAULT_AUTH)
    args = parser.parse_args()
    try:
        if args.command == 'mirror':
            mirror(args.tag, args.repo)
        else:
            dispatch(args.tag, args.repo, args.mode, args.cli, args.auth_config)
    except (ValueError, RuntimeError, KeyError, OSError, subprocess.SubprocessError) as error:
        # Never include configuration, raw CLI output, HTTP errors or signed URLs.
        if isinstance(error, (ValueError, RuntimeError)) and not isinstance(error, json.JSONDecodeError):
            parser.exit(1, str(error) + '\n')
        parser.exit(1, 'Quark release operation failed; check authorization/network and retry.\n')


if __name__ == '__main__':
    main()
