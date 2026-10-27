"""Prepare reusable HarmonyOS tools without replacing the host Go or shell settings."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import posixpath
import re
import shutil
import stat
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def clean_env():
    env = dict(os.environ)
    for key in ('GOOS', 'GOARCH', 'CGO_ENABLED', 'CC', 'CXX', 'SDKROOT', 'GOROOT'):
        env.pop(key, None)
    env['GOTOOLCHAIN'] = 'local'
    return env


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            result.update(block)
    return result.hexdigest()


def extract(archive, destination):
    """Extract files first, then validated internal links (including LLVM aliases)."""
    with zipfile.ZipFile(archive) as bundle:
        links = {}
        names = {i.filename.rstrip('/') for i in bundle.infolist()}
        for item in bundle.infolist():
            path = Path(item.filename)
            if path.is_absolute() or '..' in path.parts or '\\' in item.filename:
                raise ValueError(f'Unsafe archive entry: {item.filename}')
            if stat.S_ISLNK(item.external_attr >> 16):
                value = bundle.read(item).decode()
                resolved = posixpath.normpath(posixpath.join(str(path.parent), value))
                if (value.startswith('/') or '\\' in value or resolved.startswith('../')
                        or resolved not in names):
                    raise ValueError(f'Unsafe archive link: {item.filename}')
                links[item.filename] = value
        for name in names:
            if any(str(parent) in links for parent in Path(name).parents):
                raise ValueError(f'Unsafe archive link ancestor: {name}')
        for name in links:
            visited = set()
            target = name
            while target in links:
                if target in visited:
                    raise ValueError(f'Unsafe archive link cycle: {name}')
                visited.add(target)
                target = posixpath.normpath(posixpath.join(str(Path(target).parent), links[target]))
        for item in bundle.infolist():
            if item.filename in links:
                continue
            target = Path(bundle.extract(item, destination))
            if target.is_file() and (item.external_attr >> 16) & 0o111:
                target.chmod(0o755)
        for name, value in links.items():
            target = destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            target.symlink_to(value)


def locate(cli):
    tools = {}
    paths = {'ohpm': 'bin/ohpm', 'hvigorw': 'bin/hvigorw',
             'hdc': 'sdk/default/openharmony/toolchains/hdc',
             'clang': 'sdk/default/openharmony/native/llvm/bin/clang',
             'node': 'tool/node/bin/node'}
    for name, relative in paths.items():
        executable = cli / relative
        if not executable.is_file() or not os.access(executable, os.X_OK):
            raise ValueError(f'Missing executable in CLI bundle: {relative}')
        tools[name] = str(executable)
    if not list(cli.rglob('ohos.toolchain.cmake')):
        raise ValueError('Native SDK missing ohos.toolchain.cmake')
    return tools


def install(archive, checksum, version, base, cache):
    if not re.fullmatch(r'[0-9a-fA-F]{64}', checksum):
        raise ValueError('SHA256 must be 64 hexadecimal characters from official metadata')
    if not re.fullmatch(r'[0-9]+(?:\.[0-9]+){2,4}', version):
        raise ValueError('Invalid CLI version')
    checksum = checksum.lower()
    print(f'校验安装包：{archive}', flush=True)
    if digest(archive) != checksum:
        raise ValueError('Archive SHA256 mismatch')
    target = base / version
    marker = {'version': version, 'sha256': checksum, 'hostArchitecture': platform.machine()}
    if target.exists():
        if json.loads((target / '.harmonyos-install.json').read_text()) != marker:
            raise ValueError(f'Existing installation differs; refusing to overwrite: {target}')
        locate(target)
        return target
    base.mkdir(parents=True, exist_ok=True)
    cache.mkdir(parents=True, exist_ok=True)
    cached = cache / f'{checksum}.zip'
    if not cached.exists() or digest(cached) != checksum:
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as stream:
            temporary = Path(stream.name)
        try:
            shutil.copyfile(archive, temporary)
            if digest(temporary) != checksum:
                raise ValueError('Archive changed while copying')
            temporary.replace(cached)
        finally:
            temporary.unlink(missing_ok=True)
    with tempfile.TemporaryDirectory(prefix='.install-', dir=base) as staging:
        staging = Path(staging)
        print(f'解压到临时目录，完成后提交至：{target}', flush=True)
        extract(cached, staging)
        candidates = [staging] + list(staging.glob('*/'))
        cli = next((p for p in candidates if (p / 'bin').is_dir() and (p / 'sdk').is_dir()), None)
        if cli is None:
            raise ValueError('Expected official command-line-tools bundle with bin/ and sdk/')
        locate(cli)
        (cli / '.harmonyos-install.json').write_text(json.dumps(marker, indent=2) + '\n')
        # A wrapper directory permits atomic rename even for a flat ZIP.
        if cli == staging:
            payload = staging / '.payload'
            payload.mkdir()
            for entry in list(staging.iterdir()):
                if entry != payload:
                    entry.rename(payload / entry.name)
            cli = payload
        cli.rename(target)
    return target


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true', help='Read-only inventory; no installation')
    parser.add_argument('--cli-archive', type=Path, help='Manually downloaded official ZIP; pinned ARM64 version and SHA256 are automatic')
    parser.add_argument('--cli-sha256', help='Official archive SHA256')
    parser.add_argument('--cli-version')
    parser.add_argument('--cli-root', type=Path, help='Inspect an existing official CLI installation')
    args = parser.parse_args()
    if platform.system() != 'Darwin':
        parser.error('macOS required')
    if args.check and args.cli_archive:
        parser.error('--check does not install archives')
    base = Path.home() / '.local/opt/harmonyos/command-line-tools'
    report = {'host': platform.platform(), 'architecture': platform.machine(),
              'stages': {}, 'ready': False, 'cliReady': False}
    cli = args.cli_root
    if args.cli_archive:
        pin = json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())['cli']
        if platform.machine() == pin['architecture']:
            args.cli_sha256 = args.cli_sha256 or pin['sha256']
            args.cli_version = args.cli_version or pin['version']
        if not args.cli_sha256 or not args.cli_version:
            parser.error('--cli-archive requires --cli-sha256 and --cli-version')
        cli = install(args.cli_archive.resolve(), args.cli_sha256, args.cli_version,
                      base, Path.home() / '.cache/harmonyos/downloads')
    if cli is None:
        installs = sorted(base.glob('*/.harmonyos-install.json'))
        if len(installs) == 1:
            cli = installs[0].parent
    try:
        if cli is None:
            raise ValueError('Provide official CLI archive + version + SHA256, or --cli-root')
        tools = locate(cli.resolve())
        tool_env = clean_env()
        tool_env['DEVECO_NODE_HOME'] = str(Path(tools['node']).parent.parent)
        tool_env['DEVECO_SDK_HOME'] = str(cli.resolve() / 'sdk')
        tool_env['PATH'] = str(Path(tools['node']).parent) + os.pathsep + tool_env.get('PATH', '')
        versions = {}
        for name, option in (('ohpm', '--version'), ('hvigorw', '--version'),
                             ('hdc', 'version'), ('clang', '--version'), ('node', '--version')):
            result = subprocess.run([tools[name], option], env=tool_env,
                                    text=True, capture_output=True, check=True, timeout=60)
            versions[name] = result.stdout.strip()
        if not args.check:
            native = cli.resolve() / 'sdk/default/openharmony/native'
            with tempfile.TemporaryDirectory(prefix='harmonyos-native-smoke-') as temporary:
                source = Path(temporary) / 'smoke.c'
                output = Path(temporary) / 'smoke.o'
                source.write_text('#include <stdint.h>\nint32_t harmonyos_smoke(void) { return 42; }\n')
                subprocess.run([tools['clang'], '--target=aarch64-linux-ohos',
                                '--sysroot=' + str(native / 'sysroot'), '-fPIC', '-c',
                                str(source), '-o', str(output)], env=tool_env,
                               check=True, capture_output=True, text=True, timeout=60)
                header = output.read_bytes()[:20]
                if header[:6] != b'\x7fELF\x02\x01' or header[18:20] != b'\xb7\x00':
                    raise ValueError('Native smoke did not produce an ARM64 ELF object')
            report['stages']['nativeCompile'] = {'status': 'passed', 'target': 'aarch64-linux-ohos',
                                                'scope': 'C object compilation only; no HAP/device execution'}
        report['cliReady'] = True
        report['stages']['cli'] = {'status': 'passed', 'root': str(cli.resolve()),
                                   'tools': tools, 'versions': versions}
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        report['stages']['cli'] = {'status': 'blocked', 'reason': str(exc)}
    host_go = shutil.which('go')
    try:
        if host_go is None:
            raise ValueError('Host Go 1.27.1 not found on PATH')
        version = subprocess.run([host_go, 'env', 'GOVERSION'], env=clean_env(),
                                 text=True, capture_output=True, check=True, timeout=30).stdout.strip()
        expected = json.loads(((ROOT / 'apps/harmonyos/toolchains.json')).read_text())['hostGoVersion']
        if version != expected:
            raise ValueError(f'Host Go requires {expected}, found {version}')
        report['stages']['hostGo'] = {'status': 'passed', 'path': host_go, 'version': version}
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        report['stages']['hostGo'] = {'status': 'blocked', 'reason': str(exc)}
    go_ok = False
    try:
        if not report['cliReady'] or report['stages']['hostGo']['status'] != 'passed':
            raise ValueError('CLI and host Go must pass before preparing community Go')
        if args.check:
            from go_toolchain import inspect_installation
            report['stages']['goPort'] = inspect_installation()
            go_ok = True
        else:
            from go_toolchain import prepare
            go_ok = prepare(cli.resolve())
            go_report = ROOT / '.dev/harmonyos/go-smoke/report.json'
            report['stages']['goPort'] = json.loads(go_report.read_text())
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        report['stages']['goPort'] = {'status': 'blocked', 'reason': str(exc)}
    report['ready'] = report['cliReady'] and go_ok
    report['stages']['hapBuild'] = {'status': 'not-run'}
    report['stages']['deviceRuntime'] = {'status': 'not-run'}
    print(json.dumps(report, ensure_ascii=False, indent=2))
    if not args.check:
        output = ROOT / '.dev/harmonyos/environment.json'
        output.parent.mkdir(parents=True, exist_ok=True)
        temporary = output.with_suffix('.tmp')
        temporary.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
        temporary.replace(output)
    return 0 if report['ready'] else 1


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ValueError, OSError, zipfile.BadZipFile) as error:
        raise SystemExit(str(error))
