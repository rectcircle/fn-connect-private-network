#!/usr/bin/env python3
"""Exercise release inventories without building or replacing real packages."""
import hashlib, json, os, pathlib, subprocess, sys, tempfile, unittest
from linux_support import TARGETS, BUNDLES, package_name
from rpm_signing import signing_identity
SCRIPT = pathlib.Path(__file__).with_name('release-manifest.py')
class ReleaseInventoryTest(unittest.TestCase):
    def create_windows_candidates(self, root, version='1.0.0', commit='commit', fingerprint='a' * 64):
        for architecture in ('arm64', 'x64'):
            candidate = root / 'windows' / architecture
            candidate.mkdir(parents=True)
            package = f'fncpn-{version}-windows-{architecture}-unsigned.msi'
            contents = f'{architecture} package'.encode()
            (candidate / package).write_bytes(contents)
            (candidate / 'build-manifest.json').write_text(json.dumps({
                'productVersion': version,
                'commit': commit,
                'workingTreeDirty': False,
                'sourceFingerprint': fingerprint,
                'architecture': architecture,
                'signed': False,
                'artifacts': [{'path': package, 'sha256': hashlib.sha256(contents).hexdigest()}],
            }))

    def test_inventory_is_exact_and_release_cannot_be_overwritten(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            names = ['fncpn-1.0.0-macos-arm64-unsigned.pkg', 'fncpn-1.0.0-macos-x64-unsigned.pkg', 'fncpn-1.0.0-fnos-arm64.fpk', 'fncpn-1.0.0-fnos-x64.fpk']
            for name in names:
                (root / name).write_bytes(b'package')
            self.create_windows_candidates(root)
            (root / 'fncpn-0.1.0-fnos-arm64.fpk').write_bytes(b'historical')
            command = [sys.executable, str(SCRIPT), str(root), '1.0.0', 'commit', 'arm64']
            environment = dict(os.environ, RELEASE='1')
            empty_path = root / 'empty-bin'
            empty_path.mkdir()
            environment['PATH'] = str(empty_path)
            subprocess.run(command, env=environment, check=True, capture_output=True)
            before = (root / 'release-1.0.0.json').read_bytes()
            record = json.loads(before)
            all_names = names + ['fncpn-1.0.0-windows-arm64-unsigned.msi', 'fncpn-1.0.0-windows-x64-unsigned.msi']
            self.assertEqual([a['file'] for a in record['artifacts']], all_names)
            self.assertEqual(record['artifacts'][4]['path'], 'windows/arm64/fncpn-1.0.0-windows-arm64-unsigned.msi')
            self.assertEqual(record['build']['windowsArchitectures'], ['arm64', 'x64'])
            self.assertEqual(record['commit'], 'commit')
            self.assertEqual(record['build']['go'], 'unavailable')
            self.assertEqual(record['build']['xcode'], 'unavailable')
            self.assertEqual(record['build']['sdk'], 'unavailable')
            self.assertNotEqual(subprocess.run(command, env=environment, capture_output=True).returncode, 0)
            self.assertEqual((root / 'release-1.0.0.json').read_bytes(), before)

    def test_windows_candidates_must_match_and_be_clean(self):
        for mutation, message in (
            (lambda manifest: manifest.update(commit='other'), 'version and commit'),
            (lambda manifest: manifest.update(workingTreeDirty=True), 'clean, unsigned'),
            (lambda manifest: manifest.update(architecture='x64'), 'matching-architecture'),
            (lambda manifest: manifest.update(sourceFingerprint='b' * 64), 'different source fingerprints'),
            (lambda manifest: manifest['artifacts'][0].update(sha256='0' * 64), 'checksum does not match'),
        ):
            with self.subTest(message=message), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                for name in ('fncpn-1.0.0-macos-arm64-unsigned.pkg', 'fncpn-1.0.0-macos-x64-unsigned.pkg', 'fncpn-1.0.0-fnos-arm64.fpk', 'fncpn-1.0.0-fnos-x64.fpk'):
                    (root / name).write_bytes(b'package')
                self.create_windows_candidates(root)
                manifest_path = root / 'windows/arm64/build-manifest.json'
                manifest = json.loads(manifest_path.read_text())
                mutation(manifest)
                manifest_path.write_text(json.dumps(manifest))
                result = subprocess.run([sys.executable, str(SCRIPT), str(root), '1.0.0', 'commit', 'arm64'], capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse((root / 'release-1.0.0.json').exists())
    def test_linux_candidates_are_exact_and_tied_to_release(self):
        for mutation, message in (
            (lambda m: None, None),
            (lambda m: m.update(commit='foreign'), 'clean release'),
            (lambda m: m.update(workingTreeDirty=True), 'clean release'),
            (lambda m: m.update(architecture='arm64'), 'clean release'),
            (lambda m: m['artifacts'].pop(), 'exactly matching'),
            (lambda m: m['artifacts'][0].update(sha256='0'*64), 'checksum'),
            (lambda m: m.update(_testEvidence='rpm-unsigned'), 'RPM signing evidence'),
            (lambda m: m.update(_testEvidence='no-rpm-signature'), 'compatibility evidence does not match'),
            (lambda m: m.update(_testEvidence='missing'), 'Missing Linux compatibility evidence'),
            (lambda m: m.update(_testEvidence='foreign'), 'compatibility evidence does not match'),
            (lambda m: m.update(_testEvidence='failed'), 'compatibility evidence does not match'),
            (lambda m: m.update(_testEvidence='no-ui'), 'compatibility evidence does not match'),
            (lambda m: m.update(_testEvidence='no-loongarch-kernel'), 'compatibility evidence does not match'),
        ):
            with self.subTest(message=message), tempfile.TemporaryDirectory() as directory:
                root = pathlib.Path(directory)
                version = '1.1.2'
                for name in (f'fncpn-{version}-macos-arm64-unsigned.pkg', f'fncpn-{version}-macos-x64-unsigned.pkg', f'fncpn-{version}-fnos-arm64.fpk', f'fncpn-{version}-fnos-x64.fpk'):
                    (root/name).write_bytes(b'fixture')
                self.create_windows_candidates(root,version)
                for target, profile in BUNDLES.items():
                    for architecture in profile['architectures']:
                        out = root/'linux'/target/architecture
                        out.mkdir(parents=True)
                        entries=[]
                        for component in ('core','ui'):
                            name=package_name(component,version,target,architecture)
                            (out/name).write_bytes(b'fixture')
                            entries.append({'path':name,'sha256':hashlib.sha256(b'fixture').hexdigest()})
                        manifest={'productVersion':version,'commit':'commit','architecture':architecture,'distribution':target,'packageFormat':profile.get('format',target),'workingTreeDirty':False,'artifacts':entries}
                        if target=='rpm': manifest['rpmSigning']=signing_identity()
                        if target=='deb' and architecture=='amd64': mutation(manifest)
                        (out/'build-manifest.json').write_text(json.dumps(manifest))
                mode=json.loads((root/'linux/deb/amd64/build-manifest.json').read_text()).get('_testEvidence')
                if mode=='rpm-unsigned':
                    path=root/'linux/rpm/amd64/build-manifest.json'
                    record=json.loads(path.read_text());record.pop('rpmSigning');path.write_text(json.dumps(record))
                (root/'linux-compat').mkdir()
                for distro, profile in TARGETS.items():
                    for architecture in profile['architectures']:
                        evidence={'target':distro,'architecture':architecture,'bundle':profile['format'],'productVersion':version,'commit':'commit','passed':True,'checks':['package-metadata','dependency-resolution','core-version','ui-dynamic-linking','core-only-install','elf-abi','ui-frontend-handshake','package-reinstall','package-remove'],'artifacts':{package_name(component,version,distro,architecture):hashlib.sha256(b'fixture').hexdigest() for component in ('core','ui')}}
                        if architecture == 'loong64':
                            evidence['checks'] += ['systemd-services','package-upgrade','upgrade-state-preserved','kernel-network','real-resolved']
                            if mode == 'no-loongarch-kernel': evidence['checks'].remove('kernel-network')
                        if profile['format']=='rpm':
                            evidence['rpmSigning']=signing_identity()
                            if mode!='no-rpm-signature': evidence['checks'].append('rpm-signature')
                        if mode == 'foreign': evidence['commit']='other'
                        if mode == 'failed': evidence['passed']=False
                        if mode == 'no-ui': evidence['checks'].remove('ui-frontend-handshake')
                        if mode != 'missing': (root/'linux-compat'/f'{distro}-{architecture}.json').write_text(json.dumps(evidence))
                result=subprocess.run([sys.executable,str(SCRIPT),str(root),version,'commit','arm64'],capture_output=True,text=True)
                if message:
                    self.assertNotEqual(result.returncode,0)
                    self.assertIn(message,result.stderr)
                    self.assertFalse((root/f'release-{version}.json').exists())
                else:
                    self.assertEqual(result.returncode,0,result.stderr)
                    record=json.loads((root/f'release-{version}.json').read_text())
                    # Candidate construction uses today's target catalog; historical
                    # published inventories separately follow each bundle's since version.
                    package_count = sum(2 * len(p['architectures']) for p in BUNDLES.values())
                    self.assertEqual(len(record['artifacts']),6+package_count)
                    self.assertEqual(len([a for a in record['artifacts'] if a.get('path','').startswith('linux/')]),package_count)

if __name__ == '__main__':
    unittest.main()
