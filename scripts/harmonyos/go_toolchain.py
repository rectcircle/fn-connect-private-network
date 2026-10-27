"""Pinned community Go bootstrap and ARM64 cross-compilation probes."""
import json
import os
from pathlib import Path
import platform
import shlex
import subprocess
import tempfile

from setup_macos import ROOT, clean_env


def run(args, **kwargs):
    print('+ ' + shlex.join(map(str, args)), flush=True)
    return subprocess.run(list(map(str, args)), check=True, **kwargs)


def _prepare(cli_root=None):
    pin = json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())
    port = pin['goPort']
    commit = port['installCommit']
    base = Path.home() / '.local/opt/harmonyos/go'
    target = base / (port['version'] + '-' + commit)
    env = clean_env()
    for key in ('GOFLAGS', 'GOEXPERIMENT', 'GOBIN', 'GOWORK', 'GOHOSTOS', 'GOHOSTARCH',
                'GOROOT_BOOTSTRAP', 'CGO_CFLAGS', 'CGO_LDFLAGS', 'DEVECO_NODE_HOME'):
        env.pop(key, None)
    env.update(GOENV='off', GOTOOLCHAIN='local', CGO_ENABLED='0', GOMAXPROCS='4')
    env['GOROOT_BOOTSTRAP'] = subprocess.check_output(['go', 'env', 'GOROOT'],
                                                    text=True, env=env).strip()
    marker = {'commit': commit, 'version': port['version'], 'host': platform.machine()}
    if target.exists():
        if json.loads((target / '.harmonyos-go-install.json').read_text()) != marker:
            raise ValueError('Existing Go installation mismatch: ' + str(target))
    else:
        base.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='.build-', dir=base) as staging:
            source = Path(staging) / 'source'
            run(['git', 'init', source])
            run(['git', '-C', source, 'fetch', '--depth=1', port['repository'], commit])
            run(['git', '-C', source, 'checkout', '--detach', 'FETCH_HEAD'])
            actual = subprocess.check_output(['git', '-C', source, 'rev-parse', 'HEAD'], text=True).strip()
            if actual != commit:
                raise ValueError('Go source commit mismatch')
            run(['bash', 'make.bash'], cwd=source / 'src', env=env)
            version = subprocess.check_output([source / 'bin/go', 'env', 'GOVERSION'],
                                              text=True, env=env).strip()
            if version != port['version']:
                raise ValueError('Go identity mismatch: ' + version)
            targets = subprocess.check_output([source / 'bin/go', 'tool', 'dist', 'list'],
                                              text=True, env=env).splitlines()
            if not {'openharmony/arm64', 'openharmony/amd64'} <= set(targets):
                raise ValueError('Missing OpenHarmony targets')
            (source / '.harmonyos-go-install.json').write_text(json.dumps(marker, indent=2) + '\n')
            source.rename(target)
    go = target / 'bin/go'
    run([go, 'version'], env=env)
    cli = cli_root or Path.home() / '.local/opt/harmonyos/command-line-tools' / pin['cli']['version']
    native = cli / 'sdk/default/openharmony/native'
    work = ROOT / '.dev/harmonyos/go-smoke'
    work.mkdir(parents=True, exist_ok=True)
    cc = work / 'clang-ohos'
    cc.write_text('#!/bin/sh\nexec ' + shlex.quote(str(native / 'llvm/bin/clang'))
                  + ' --target=aarch64-linux-ohos --sysroot='
                  + shlex.quote(str(native / 'sysroot')) + ' "$@"\n')
    cc.chmod(0o755)
    env.pop('GOROOT_BOOTSTRAP', None)
    env.update(GOOS='openharmony', GOARCH='arm64', CGO_ENABLED='1', CC=str(cc),
               GOCACHE=str(work / 'cache'), GOWORK='off')
    report = {'commit': commit, 'go': str(go), 'version': port['version'],
              'target': 'openharmony/arm64', 'deviceRuntime': 'not-run', 'probes': {}}
    probes = {
        'minimal': 'import "C"\n//export HarmonySmoke\nfunc HarmonySmoke() C.int { return 42 }\n',
        'clienthost': 'import "C"\nimport "github.com/rectcircle/fn-connect-private-network/internal/clienthost"\nvar _ = clienthost.New\n',
        'wireguard': 'import "C"\nimport "golang.zx2c4.com/wireguard/device"\nimport "golang.zx2c4.com/wireguard/conn"\nvar _ = device.NewDevice\nvar _ = conn.NewDefaultBind\n',
    }
    for name, imports in probes.items():
        directory = work / name
        directory.mkdir(exist_ok=True)
        source = directory / 'main.go'
        source.write_text('package main\n' + imports + '\nfunc main() {}\n')
        output = directory / ('lib' + name + '.so')
        result = subprocess.run([go, 'build', '-buildmode=c-shared', '-o', output, source],
                                cwd=ROOT, env=env, text=True, capture_output=True)
        (directory / 'build.log').write_text(result.stdout + result.stderr)
        report['probes'][name] = {'status': 'passed' if result.returncode == 0 else 'failed',
                                  'log': str(directory / 'build.log')}
        if result.returncode == 0:
            readelf = native / 'llvm/bin/llvm-readelf'
            inspection = subprocess.check_output([readelf, '-h', '-d', '-r', output], text=True)
            (directory / 'elf.txt').write_text(inspection)
            report['probes'][name]['elf'] = str(directory / 'elf.txt')
            if 'AArch64' not in inspection or 'TPREL' in inspection or 'STATIC_TLS' in inspection:
                report['probes'][name]['status'] = 'failed-elf'
        print(name + ': ' + report['probes'][name]['status'], flush=True)
    (work / 'report.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))
    return all(p['status'] == 'passed' for p in report['probes'].values())


def inspect_installation():
    """Inspect installed identity/targets without bootstrapping or compiling probes."""
    port = json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())['goPort']
    target = Path.home() / '.local/opt/harmonyos/go' / (port['version'] + '-' + port['installCommit'])
    expected = {'commit': port['installCommit'], 'version': port['version'], 'host': platform.machine()}
    if json.loads((target / '.harmonyos-go-install.json').read_text()) != expected:
        raise ValueError('Community Go installation identity mismatch')
    env = clean_env()
    env.update(GOENV='off')
    version = subprocess.check_output([target / 'bin/go', 'env', 'GOVERSION'], text=True, env=env).strip()
    targets = subprocess.check_output([target / 'bin/go', 'tool', 'dist', 'list'], text=True, env=env).splitlines()
    if version != port['version'] or not {'openharmony/arm64', 'openharmony/amd64'} <= set(targets):
        raise ValueError('Community Go version or target mismatch')
    return {'status': 'passed', 'version': version, 'commit': port['installCommit'],
            'go': str(target / 'bin/go'), 'scope': 'installation inspection; probes not run'}


def prepare(cli_root=None):
    base = Path.home() / '.local/opt/harmonyos/go'
    base.mkdir(parents=True, exist_ok=True)
    lock = base / '.install-lock'
    try:
        lock.mkdir()
    except FileExistsError:
        raise ValueError(f'Go setup already running; if interrupted, verify no process remains before removing {lock}')
    try:
        return _prepare(cli_root)
    finally:
        lock.rmdir()


if __name__ == '__main__':
    raise SystemExit(0 if prepare() else 1)
