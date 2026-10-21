"""Single source of truth for the tested Linux release/build target inventory."""
import json
import pathlib

ROOT = pathlib.Path(__file__).resolve().parent.parent
CATALOG = json.loads((ROOT / 'packaging/linux/targets.json').read_text())
TARGETS = {target['id']: target for target in CATALOG['targets']}
BUNDLES = CATALOG['bundles']
EXTENSIONS = {'deb': 'deb', 'rpm': 'rpm', 'archlinux': 'pkg.tar.zst'}


def target_profile(name, architecture=None):
    if name not in TARGETS:
        raise ValueError(f'Unknown Linux target {name!r}; choose: {", ".join(TARGETS)}')
    profile = TARGETS[name]
    if architecture and architecture not in profile['architectures']:
        raise ValueError(f'{name} does not support architecture {architecture}')
    return profile


def package_profile(name, architecture=None):
    bundle = name if name in BUNDLES else target_profile(name)['format']
    definition = BUNDLES[bundle]
    if architecture and architecture not in definition['architectures']:
        raise ValueError(f'{bundle} does not support architecture {architecture}')
    return dict(TARGETS[definition['baseline']], **{key:value for key,value in definition.items() if key != 'baseline'}, bundle=bundle, baseline=definition['baseline'])


def package_name(component, version, target, architecture):
    profile = package_profile(target, architecture)
    return f'fncpn-{component}-{version}-linux-{architecture}.{EXTENSIONS[profile["format"]]}'


def release_packages(version):
    return [(bundle, arch, component, package_name(component, version, bundle, arch))
            for bundle, profile in BUNDLES.items() for arch in profile['architectures']
            for component in ('core', 'ui')]


def build_matrix():
    entries = []
    for bundle, definition in BUNDLES.items():
        entries.extend(dict(item, target=bundle) for item in ci_matrix()['include'] if item['target'] == definition['baseline'])
    return {'include': entries}


def ci_matrix():
    return {'include': [dict(target=target, arch=arch, image=profile['image'],
                             format=profile['format'],
                             family='deb' if profile['format']=='deb' else
                                    'arch' if profile['format']=='archlinux' else
                                    'suse' if profile['osID'].startswith('opensuse') else 'fedora',
                             runner='ubuntu-24.04-arm' if arch=='arm64' else 'ubuntu-24.04')
                        for target, profile in TARGETS.items() for arch in profile['architectures']]}


if __name__ == '__main__':
    import sys
    if sys.argv[1:] == ['build-matrix']:
        print(json.dumps(build_matrix(), separators=(',', ':')))
    elif sys.argv[1:] == ['matrix']:
        print(json.dumps(ci_matrix(), separators=(',', ':')))
    else:
        name = sys.argv[1] if len(sys.argv)>1 else 'deb'
        print(json.dumps(package_profile(name, sys.argv[2] if len(sys.argv)>2 else None)))
