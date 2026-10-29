"""Single source of truth for the tested Linux release/build target inventory."""
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
CATALOG = json.loads((ROOT / 'packaging/linux/targets.json').read_text())
TARGETS = {target['id']: target for target in CATALOG['targets']}
BUNDLES = CATALOG['bundles']
COMPATIBILITY_TARGETS = {p['id']: dict(TARGETS[p['base']], **p, compatibilityCandidate=True)
                         for p in CATALOG.get('compatibilityCandidates', [])}
EXTENSIONS = {'deb': 'deb', 'rpm': 'rpm', 'archlinux': 'pkg.tar.zst'}


def target_profile(name, architecture=None):
    if name not in TARGETS and name not in COMPATIBILITY_TARGETS:
        raise ValueError(f'Unknown Linux target {name!r}; choose: {", ".join(TARGETS)}')
    profile = (TARGETS | COMPATIBILITY_TARGETS)[name]
    if architecture and architecture not in profile['architectures']:
        raise ValueError(f'{name} does not support architecture {architecture}')
    return profile


def package_profile(name, architecture=None):
    if name in COMPATIBILITY_TARGETS:
        return dict(target_profile(name, architecture), bundle=name, baseline=name)
    profile = None if name in BUNDLES else target_profile(name, architecture)
    bundle = name if profile is None else profile.get('bundle', profile['format'])
    definition = BUNDLES[bundle]
    if architecture and architecture not in definition['architectures']:
        raise ValueError(f'{bundle} does not support architecture {architecture}')
    return dict(TARGETS[definition['baseline']], **{key:value for key,value in definition.items() if key != 'baseline'}, bundle=bundle, baseline=definition['baseline'])


def runtime_profile(name, architecture=None):
    # Distribution acceptance must inspect the requested userspace rather than
    # the shared package's compilation baseline.
    return package_profile(name, architecture) if name in BUNDLES else target_profile(name, architecture)


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
                if '-linux-loong64.' in a['file']}
    if not isinstance(proof, dict):
        proof = {}
    from linux_acceptance import validate_evidence
    targets = {name for name, p in TARGETS.items() if 'loong64' in p['architectures']}
    try:
        if (proof.get('level') != 'full' or proof.get('commit') != manifest['commit']
                or len(expected) != 6 or proof.get('artifacts') != expected
                or set(proof.get('targets', {})) != targets):
            raise ValueError('Incomplete LoongArch acceptance inventory')
        for name in targets:
            validate_evidence(proof['targets'][name], name, 'loong64', manifest['version'], manifest['commit'], expected)
    except (ValueError, KeyError, TypeError) as error:
        raise ValueError('LoongArch runtime acceptance is required before publication; rebuild with loongarch_full_validation=true') from error



def loongarch_build_matrix(selected='both'):
    if selected not in ('both', 'rpm', 'archlinux'):
        raise ValueError('Unknown LoongArch package format: ' + selected)
    profiles = {name: package_profile(name) for name, definition in BUNDLES.items()
                if definition.get('execution') == 'qemu' and package_profile(name)['format'] != 'deb'}
    return {'include': [{'target': name, 'image': profile['image'],
                         'repository_probe': profile.get('repositoryProbe', ''),
                         'image_archive': profile.get('imageArchive', ''),
                         'image_archive_sha256': profile.get('imageArchiveSHA256', '')}
                        for name, profile in profiles.items()
                        if selected == 'both' or profile['format'] == selected]}



def loongarch_acceptance_matrix(selected='supported'):
    choices = {'debian': 'debian-forky', 'openeuler': 'openeuler-loong64', 'archlinux': 'archlinux-loong64',
               'deepin': 'deepin25-loong64', 'anolis': 'anolis23-loong64'}
    if selected != 'supported' and selected not in choices:
        raise ValueError('Unknown acceptance target: ' + selected)
    names = [value for key, value in choices.items() if key != 'anolis'] if selected == 'supported' else [choices[selected]]
    return {'include': [{'target': name, 'bundle': package_profile(COMPATIBILITY_TARGETS.get(name, {}).get('base', name))['bundle']} for name in names]}


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
