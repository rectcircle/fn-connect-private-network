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

from linux_support import ROOT, TARGETS, BUNDLES, build_matrix, ci_matrix, package_name, release_packages, target_profile


def module(name):
    spec=importlib.util.spec_from_file_location(name,ROOT/'scripts'/name)
    result=importlib.util.module_from_spec(spec);spec.loader.exec_module(result)
    return result


builder=module('linux-package.py')
inspector=module('check-linux-packages.py')


class LinuxSupportTest(unittest.TestCase):
    def test_distribution_dependency_and_architecture_contract(self):
        self.assertEqual(target_profile('debian12')['uiDependencies'][0],'libgtk-3-0')
        self.assertEqual(target_profile('debian13')['uiDependencies'][0],'libgtk-3-0t64')
        self.assertIn('policykit-1',target_profile('ubuntu22.04')['uiDependencies'])
        self.assertNotIn('systemd-resolved',target_profile('ubuntu22.04')['coreDependencies'])
        with self.assertRaises(ValueError):target_profile('archlinux','arm64')
        with self.assertRaises(ValueError):target_profile('fedora42')
        matrix=ci_matrix()['include']
        self.assertEqual(len(matrix),sum(len(p['architectures']) for p in TARGETS.values()))
        names=[name for *_,name in release_packages('1.1.2')]
        self.assertEqual(len(names),len(set(names)))
        self.assertEqual(len(names),10)
        self.assertEqual(len(build_matrix()["include"]),5)

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
            for target in ('debian12','fedora43','archlinux'):
                for component in ('core','ui'):
                    with self.subTest(target=target,component=component):
                        config=builder.configuration(component,target,'amd64','1.1.2',str(binary),directory)
                        file=directory/'config.json';file.write_text(json.dumps(config))
                        output=directory/package_name(component,'1.1.2',target,'amd64')
                        subprocess.run([nfpm,'package','-f',str(file),'-p',target_profile(target)['format'],'-t',str(output)],cwd=ROOT,check=True,capture_output=True)
                        fields=inspector.inspect(output)
                        self.assertEqual(fields['Package'],'fncpn-'+component)
                        self.assertEqual(fields['Version'],'1.1.2')
                        self.assertEqual(fields['Architecture'],'amd64')


if __name__=='__main__':unittest.main()
