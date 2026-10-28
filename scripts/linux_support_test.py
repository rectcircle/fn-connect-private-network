#!/usr/bin/env python3
"""Validate target inventory, native package metadata and lifecycle semantics."""
import importlib.util
import json
import os
import pathlib
import shutil
import struct
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from linux_support import ROOT, TARGETS, BUNDLES, EXPERIMENTAL_TARGETS, experimental_matrix, build_matrix, ci_matrix, package_name, package_profile, release_packages, target_profile, validate_loongarch_publication


def module(name):
    spec=importlib.util.spec_from_file_location(name,ROOT/'scripts'/name)
    result=importlib.util.module_from_spec(spec);spec.loader.exec_module(result)
    return result


builder=module('linux-package.py')
inspector=module('check-linux-packages.py')


class LinuxSupportTest(unittest.TestCase):
    def test_loongarch_has_an_independent_deb_baseline(self):
        profile = package_profile('debian-forky', 'loong64')
        self.assertEqual(profile['bundle'], 'deb-loong64')
        self.assertEqual(profile['format'], 'deb')
        self.assertEqual(profile['baseline'], 'debian-forky')
        self.assertEqual(profile['osCodename'], 'forky')
        for target in ('deb', 'debian12', 'debian13', 'rpm', 'archlinux'):
            with self.subTest(target=target), self.assertRaises(ValueError):
                package_profile(target, 'loong64')
        self.assertFalse(any(item['arch'] == 'loong64' for item in ci_matrix('native')['include']))
        self.assertEqual(package_name('ui','1.1.8','deb-loong64','loong64'), 'fncpn-ui-1.1.8-linux-loong64.deb')

    def test_loongarch_preinstall_refuses_foreign_abi_before_maintenance(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = pathlib.Path(temporary)
            release = directory / 'os-release'
            loader = directory / 'loader'
            loader.touch()
            guard = (ROOT / 'packaging/linux/scripts/check-loongarch').read_text().replace(
                '/etc/os-release', str(release)).replace('/lib64/ld-linux-loongarch-lp64d.so.1', str(loader))
            for arch, kernel, codename, success in [('loong64','6.12.0','forky',True),
                    ('loongarch64','6.12.0','forky',False), ('loong64','5.10.0','forky',False),
                    ('loong64','6.12.0','trixie',False)]:
                release.write_text('ID=debian\nVERSION_CODENAME='+codename+'\n')
                prefix = f'uname() {{ case "$1" in -m) echo loongarch64 ;; -r) echo {kernel} ;; esac; }}; dpkg() {{ echo {arch}; }};\n'
                result = subprocess.run(['sh', '-c', prefix + guard + '\necho maintenance-reached'], capture_output=True, text=True)
                self.assertEqual(result.returncode == 0, success, result.stderr)
                self.assertEqual('maintenance-reached' in result.stdout, success)

    def test_distribution_dependency_and_architecture_contract(self):
        self.assertEqual(target_profile('debian12')['uiDependencies'][0],'libgtk-3-0')
        self.assertEqual(target_profile('debian13')['uiDependencies'][0],'libgtk-3-0t64')
        self.assertIn('policykit-1',target_profile('ubuntu22.04')['uiDependencies'])
        self.assertNotIn('systemd-resolved',target_profile('ubuntu22.04')['coreDependencies'])
        with self.assertRaises(ValueError):target_profile('archlinux','arm64')
        with self.assertRaises(ValueError):target_profile('fedora42')
        matrix=ci_matrix()['include']
        self.assertEqual(len(matrix),sum(len(p['architectures']) for p in TARGETS.values()))
        names=[name for *_,name in release_packages('1.1.8')]
        self.assertEqual(len(names),len(set(names)))
        self.assertEqual(len(names),12)
        self.assertEqual(len(build_matrix()["include"]),5)

    def test_experimental_format_can_be_retried_independently(self):
        self.assertEqual(len(experimental_matrix()['include']), 2)
        for selected in ('rpm', 'archlinux'):
            entries = experimental_matrix(selected)['include']
            self.assertEqual(len(entries), 1)
            self.assertEqual(package_profile(entries[0]['target'])['format'], selected)
        with self.assertRaises(ValueError): experimental_matrix('unsupported')

    def test_experimental_loongarch_formats_are_not_release_targets(self):
        for target, expected in [('fedora-remix43-loong64', 'loongarch64'), ('archlinux-loong64', 'loong64')]:
            self.assertIn(target, EXPERIMENTAL_TARGETS)
            self.assertNotIn(target, TARGETS)
            self.assertNotIn(target, BUNDLES)
            profile = package_profile(target, 'loong64')
            self.assertTrue(profile['experimental'])
            with self.assertRaises(ValueError): package_profile(target, 'amd64')
            with tempfile.TemporaryDirectory() as temporary:
                for component in ('core', 'ui'):
                    config = builder.configuration(component, target, 'loong64', '1.1.8', '/tmp/fixture', pathlib.Path(temporary))
                    self.assertEqual(config[profile['format']]['arch'], expected)
                    for script in config['scripts'].values():
                        self.assertNotIn('Debian forky', pathlib.Path(script).read_text())
        self.assertEqual(len(release_packages('1.1.8')), 12)
        self.assertTrue(all(item['target'] not in EXPERIMENTAL_TARGETS for item in ci_matrix()['include']))

    def test_build_only_loongarch_draft_cannot_be_published(self):
        files = {package_name(component, '1.1.8', 'deb-loong64', 'loong64'): 'hash-' + component
                 for component in ('core', 'ui')}
        manifest = {'version': '1.1.8', 'commit': 'candidate',
                    'artifacts': [{'file': name, 'sha256': digest} for name, digest in files.items()],
                    'build': {}}
        for proof in (None, {'level': 'build'}, {'level': 'full', 'commit': 'foreign', 'artifacts': files},
                      {'level': 'full', 'commit': 'candidate', 'artifacts': {}}):
            manifest['build']['loongarchValidation'] = proof
            with self.assertRaisesRegex(ValueError, 'runtime acceptance'):
                validate_loongarch_publication(manifest)
        manifest['build']['loongarchValidation'] = {'level': 'full', 'commit': 'candidate', 'artifacts': files}
        validate_loongarch_publication(manifest)
        validate_loongarch_publication({'version': '1.1.7'})

    def test_historical_releases_do_not_gain_loongarch_assets(self):
        for version in ('1.1.2', '1.1.7'):
            rows = release_packages(version)
            self.assertEqual(len(rows), 10)
            self.assertFalse(any(arch == 'loong64' for _, arch, _, _ in rows))
        rows = release_packages('1.1.8')
        self.assertEqual(len(rows), 12)
        self.assertEqual({component for _, arch, component, _ in rows if arch == 'loong64'}, {'core', 'ui'})

    def test_package_metadata_must_match_requested_build_target(self):
        fields={'Package':'fncpn-core','Version':'1.1.2','Architecture':'amd64'}
        path=pathlib.Path(package_name('core','1.1.2','debian12','amd64'))
        inspector.validate_target(fields,path,'debian12','amd64','1.1.2')
        with self.assertRaises(ValueError):inspector.validate_target(fields,path,'debian12','arm64','1.1.2')
        with self.assertRaises(ValueError):inspector.validate_target(fields,path,'fedora43','amd64','1.1.2')

    def test_matching_versions_and_scripts_for_every_target(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory=pathlib.Path(temporary)
            for target,profile in BUNDLES.items():
                for arch in profile['architectures']:
                    for component in ('core','ui'):
                        config=builder.configuration(component,target,arch,'1.1.2','/tmp/fixture',directory)
                        if component=='ui':
                            dependency=next(value for value in config['depends'] if value.startswith('fncpn-core'))
                            self.assertIn('1.1.2',dependency)
                        scripts=list(config['scripts'].values())+list(config.get(target,{}).get('scripts',{}).values())
                        for script in scripts:subprocess.run(['sh','-n',script],check=True)

    def test_package_managers_preserve_running_root_services_during_upgrade(self):
        for manager,pre,pre_arg,post,post_arg in [('rpm','preinstall','2','posttrans','1'),('deb','preinstall','upgrade','postinstall','configure'),('archlinux','preupgrade','1.1.2','postupgrade','1.1.2')]:
            with self.subTest(manager=manager), tempfile.TemporaryDirectory() as temporary:
                directory=pathlib.Path(temporary)
                generic,special=builder.lifecycle('core',manager,directory)
                command_dir=directory/'bin';command_dir.mkdir()
                log=directory/'commands'
                fake='#!/bin/sh\nprintf "%s\\n" "$*" >> "$COMMAND_LOG"\ncase "$1" in list-units) echo "fncpn-client-privileged@1000.service loaded active running fixture" ;; esac\n'
                for name in ('systemctl','restorecon'):(command_dir/name).write_text(fake)
                for name in ('pkill','pgrep'):(command_dir/name).write_text('#!/bin/sh\nexit 1\n')
                for file in command_dir.iterdir():file.chmod(0o755)
                (directory/'run/systemd/system').mkdir(parents=True)
                env=dict(os.environ,PATH=str(command_dir)+os.pathsep+os.environ['PATH'],COMMAND_LOG=str(log))
                def run(stage,argument):
                    path=pathlib.Path((generic|special)[stage])
                    content=path.read_text().replace('/var/lib/fncpn',str(directory/'var/lib/fncpn')).replace('/run/',str(directory/'run')+'/').replace('/etc/systemd/',str(directory/'etc/systemd')+'/')
                    path.write_text(content)
                    subprocess.run(['sh',str(path),argument],env=env,check=True)
                if manager=='rpm':
                    run('preremove','1')
                    self.assertFalse(log.exists(),'old RPM upgrade removal stopped new service')
                run(pre,pre_arg)
                self.assertTrue(log.exists(),pathlib.Path((generic|special)[pre]).read_text())
                self.assertIn('stop fncpn-client-privileged@*.service',log.read_text())
                self.assertTrue((directory/'var/lib/fncpn/package-maintenance/active-root-units').is_file())
                run(post,post_arg)
                self.assertIn('start fncpn-client-privileged@1000.service',log.read_text())
                self.assertFalse((directory/'var/lib/fncpn/package-maintenance/active-root-units').exists())



    def test_ui_abi_dependencies_are_extracted_instead_of_assumed(self):
        versions='File: libc.so.6 Cnt: 2\nName: GLIBC_2.9\nName: GLIBC_2.34\n'
        dynamic='Shared library: [libc.so.6]\nShared library: [libgtk-3.so.0]\n'
        with patch.object(builder.subprocess,'check_output',side_effect=[versions,dynamic]):
            requirements=builder.elf_dependencies('/tmp/fixture',target_profile('fedora43'))
            self.assertIn('libc.so.6(GLIBC_2.34)(64bit)',requirements)
            self.assertIn('libgtk-3.so.0()(64bit)',requirements)
        with patch.object(builder.subprocess,'check_output',return_value=versions):
            self.assertEqual(builder.elf_dependencies('/tmp/fixture',target_profile('archlinux')),['glibc>=2.34'])
        with patch.object(builder.subprocess,'check_output',return_value='shlibs:Depends=libc6 (>= 2.34), libgtk-3-0 (>= 3.24)\n'):
            self.assertEqual(builder.elf_dependencies('/tmp/fixture',target_profile('debian12')),['libc6 (>= 2.34)','libgtk-3-0 (>= 3.24) | libgtk-3-0t64 (>= 3.24)'])

    def test_native_package_roundtrip(self):
        nfpm=os.environ.get('NFPM',str(pathlib.Path.home()/'go/bin/nfpm'))
        if not pathlib.Path(nfpm).is_file():self.skipTest('pinned nFPM is required for archive roundtrip')
        with tempfile.TemporaryDirectory(prefix='fncpn-package-fixture-') as temporary:
            directory=pathlib.Path(temporary)
            # Header-only, non-executable test data: never published as a product binary.
            binary=directory/'fixture-elf'
            elf=bytearray(64);elf[:6]=b'\x7fELF\x02\x01';struct.pack_into('<H',elf,18,62)
            binary.write_bytes(elf);binary.chmod(0o755)
            for target, arch, machine in [('debian12','amd64',62),('fedora43','amd64',62),('archlinux','amd64',62),('debian-forky','loong64',258)]:
                struct.pack_into('<H',elf,18,machine)
                binary.write_bytes(elf)
                for component in ('core','ui'):
                    with self.subTest(target=target,component=component):
                        config=builder.configuration(component,target,arch,'1.1.2',str(binary),directory)
                        file=directory/'config.json';file.write_text(json.dumps(config))
                        output=directory/package_name(component,'1.1.2',target,arch)
                        subprocess.run([nfpm,'package','-f',str(file),'-p',target_profile(target)['format'],'-t',str(output)],cwd=ROOT,check=True,capture_output=True)
                        fields=inspector.inspect(output)
                        self.assertEqual(fields['Package'],'fncpn-'+component)
                        self.assertEqual(fields['Version'],'1.1.2')
                        self.assertEqual(fields['Architecture'],arch)


if __name__=='__main__':unittest.main()
