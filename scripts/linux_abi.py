"""Inspect release ELF files against the libraries of the current target distro."""
import pathlib
import re
import subprocess


def output(*args):
    return subprocess.check_output(args, text=True, stderr=subprocess.STDOUT)


def version_needs(text):
    result = {}
    library = None
    active = False
    for line in text.splitlines():
        if line.startswith('Version '):
            active = line.startswith('Version needs section')
        if not active:
            continue
        match = re.search(r'File:\s+(\S+)', line)
        if match:
            library = match[1]
            result.setdefault(library, set())
        match = re.search(r'Name:\s+(\S+)', line)
        if match and library:
            result[library].add(match[1])
    return result


def version_definitions(text):
    active = False
    result = set()
    for line in text.splitlines():
        if line.startswith('Version '):
            active = line.startswith('Version definition section')
        if active:
            match = re.search(r'Name:\s+(\S+)', line)
            if match:
                result.add(match[1])
    return result


def version_key(value):
    return tuple(int(part) for part in value.split('_', 1)[1].split('.'))


def inspect(core, ui):
    core_headers = output('readelf', '-W', '-l', str(core))
    core_dynamic = output('readelf', '-W', '-d', str(core))
    if 'INTERP' in core_headers or '(NEEDED)' in core_dynamic:
        raise ValueError('Core unexpectedly depends on a dynamic loader/library')
    headers = output('readelf', '-W', '-l', str(ui))
    interpreter = re.search(r'Requesting program interpreter: (.+?)\]', headers)
    if not interpreter or not pathlib.Path(interpreter[1]).is_file():
        raise ValueError('UI dynamic loader unavailable on target')
    dynamic = output('readelf', '-W', '-d', str(ui))
    if '(RPATH)' in dynamic or '(RUNPATH)' in dynamic:
        raise ValueError('Distribution UI must resolve system libraries without embedded RPATH/RUNPATH')
    linked = output('ldd', '-r', str(ui))
    if 'not found' in linked or 'undefined symbol:' in linked:
        raise ValueError(linked)
    providers = dict(re.findall(r'^\s*(\S+)\s+=>\s+(/\S+)', linked, re.M))
    providers[pathlib.Path(interpreter[1]).name] = interpreter[1]
    needs = version_needs(output('readelf', '-W', '--version-info', str(ui)))
    for library, required in needs.items():
        if 'GLIBC_PRIVATE' in required:
            raise ValueError('Release UI requires private glibc symbols')
        path = providers.get(library)
        if not path:
            raise ValueError('Missing ABI provider: ' + library)
        available = version_definitions(output('readelf', '-W', '--version-info', path))
        missing = required - available
        if missing:
            raise ValueError(f'{library} missing required versions: {sorted(missing)}')
    maxima = {}
    for prefix in ('GLIBC_', 'GLIBCXX_', 'CXXABI_'):
        versions = [v for values in needs.values() for v in values
                    if re.fullmatch(re.escape(prefix) + r'\d+(?:\.\d+)*', v)]
        if versions:
            maxima[prefix.rstrip('_')] = max(versions, key=version_key)
    return {'coreStatic': True, 'interpreter': interpreter[1],
            'needed': re.findall(r'\(NEEDED\).*\[(.*?)\]', dynamic),
            'requiredVersions': {k: sorted(v) for k, v in needs.items()},
            'highestRequiredVersions': maxima, 'resolvedLibraries': providers,
            'glibc': output('getconf', 'GNU_LIBC_VERSION').strip(), 'ldd': linked}
