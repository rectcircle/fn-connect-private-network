"""Single source of truth for the tested Linux release/build target inventory."""
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
CATALOG = json.loads((ROOT / 'packaging/linux/targets.json').read_text())
TARGETS = {target['id']: target for target in CATALOG['targets']}
BUNDLES = CATALOG['bundles']
EXPERIMENTAL_TARGETS = {p['id']: dict(TARGETS[p['base']], **p, experimental=True)
                        for p in CATALOG.get('experimentalTargets', [])}
EXTENSIONS = {'deb': 'deb', 'rpm': 'rpm', 'archlinux': 'pkg.tar.zst'}


def target_profile(name, architecture=None):
    if name not in TARGETS and name not in EXPERIMENTAL_TARGETS:
        raise ValueError(f'Unknown Linux target {name!r}; choose: {", ".join(TARGETS)}')
    profile = TARGETS[name] if name in TARGETS else EXPERIMENTAL_TARGETS[name]
    if architecture and architecture not in profile['architectures']:
        raise ValueError(f'{name} does not support architecture {architecture}')
    return profile


def package_profile(name, architecture=None):
    if name in EXPERIMENTAL_TARGETS:
        return dict(target_profile(name, architecture), bundle=name, baseline=name)
    profile = None if name in BUNDLES else target_profile(name, architecture)
    bundle = name if profile is None else profile.get('bundle', profile['format'])
    definition = BUNDLES[bundle]
    if architecture and architecture not in definition['architectures']:
        raise ValueError(f'{bundle} does not support architecture {architecture}')
    return dict(TARGETS[definition['baseline']], **{key:value for key,value in definition.items() if key != 'baseline'}, bundle=bundle, baseline=definition['baseline'])


def package_name(component, version, target, architecture):
    profile = package_profile(target, architecture)
    return f'fncpn-{component}-{version}-linux-{architecture}.{EXTENSIONS[profile["format"]]}'


def release_packages(version):
    return [(bundle, arch, component, package_name(component, version, bundle, arch))
            for bundle, profile in BUNDLES.items()
            if tuple(map(int, version.split('.'))) >= tuple(map(int, profile.get('since', '0.0.0').split('.')))
            for arch in profile['architectures']
            for component in ('core', 'ui')]


def validate_loongarch_publication(manifest):
    if tuple(map(int, manifest['version'].split('.'))) < (1, 1, 8):
        return
    proof = manifest.get('build', {}).get('loongarchValidation', {})
    expected = {a['file']: a['sha256'] for a in manifest['artifacts']
                if a['file'].endswith('-linux-loong64.deb')}
    if not isinstance(proof, dict):
        proof = {}
    if (proof.get('level') != 'full' or proof.get('commit') != manifest['commit']
            or len(expected) != 2 or proof.get('artifacts') != expected):
        raise ValueError('LoongArch runtime acceptance is required before publication; rebuild with loongarch_full_validation=true')


def experimental_matrix(selected='both'):
    if selected not in ('both', 'rpm', 'archlinux'):
        raise ValueError('Unknown experimental package format: ' + selected)
    return {'include': [{'target': name, 'image': profile['image'],
                         'repository_probe': profile.get('repositoryProbe', '')}
                        for name, profile in EXPERIMENTAL_TARGETS.items()
                        if selected == 'both' or profile['format'] == selected]}


def build_matrix():
    entries = []
    for bundle, definition in BUNDLES.items():
        if definition.get('execution', 'native') != 'native':
            continue  # QEMU full-system builds use the dedicated reusable job.
        entries.extend(dict(item, target=bundle) for item in ci_matrix()['include'] if item['target'] == definition['baseline'])
    return {'include': entries}


def ci_matrix(execution=None):
    return {'include': [dict(target=target, arch=arch, image=profile['image'],
                             format=profile['format'],
                             family='deb' if profile['format']=='deb' else
                                    'arch' if profile['format']=='archlinux' else
                                    'suse' if profile['osID'].startswith('opensuse') else 'fedora',
                             runner='ubuntu-26.04' if arch=='loong64' else 'ubuntu-24.04-arm' if arch=='arm64' else 'ubuntu-24.04')
                        for target, profile in TARGETS.items() for arch in profile['architectures']
                        if execution is None or profile.get('execution', 'native') == execution]}


if __name__ == '__main__':
    import sys
    if sys.argv[1:] == ['build-matrix']:
        print(json.dumps(build_matrix(), separators=(',', ':')))
    elif sys.argv[1:] == ['matrix']:
        print(json.dumps(ci_matrix(), separators=(',', ':')))
    elif sys.argv[1:] == ['native-matrix']:
        print(json.dumps(ci_matrix('native'), separators=(',', ':')))
    else:
        name = sys.argv[1] if len(sys.argv)>1 else 'deb'
        print(json.dumps(package_profile(name, sys.argv[2] if len(sys.argv)>2 else None)))
