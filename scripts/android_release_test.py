import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import android_release


class AndroidReleaseTests(unittest.TestCase):
    def fixture(self, root):
        (root / 'android').mkdir()
        for abi, name in zip(android_release.ABIS, android_release.packages('1.1.4')):
            data = abi.encode()
            path = root / 'android' / name
            path.write_bytes(data)
            record = {'variant':'release','apk':name,'sha256':hashlib.sha256(data).hexdigest(),
                      'build':{'version':'1.1.4','commit':'same','dirty':False,'versionCode':100},
                      'signingCertificateSha256':android_release.certificate_fingerprint(),
                      'nativeSha256':{abi:'hash'},'checks':sorted(android_release.CHECKS)}
            path.with_name(name+'.verification.json').write_text(json.dumps(record))

    def test_rejects_foreign_dirty_debug_or_unsigned_candidates(self):
        mutations = [lambda x:None, lambda x:x.update(variant='debug'),
                     lambda x:x['build'].update(commit='other'),lambda x:x['build'].update(dirty=True),
                     lambda x:x.update(signingCertificateSha256='wrong'),lambda x:x.update(sha256='wrong'),
                     lambda x:x.update(checks=[]),lambda x:x.update(nativeSha256={'wrong':'hash'}),
                     lambda x:x['build'].update(versionCode=101)]
        for i, mutate in enumerate(mutations):
            with self.subTest(i=i), tempfile.TemporaryDirectory() as temp:
                root=Path(temp);self.fixture(root)
                evidence=next((root/'android').glob('*.verification.json'))
                value=json.loads(evidence.read_text());mutate(value);evidence.write_text(json.dumps(value))
                if i:
                    with self.assertRaises(ValueError):android_release.candidates(root,'1.1.4','same')
                else:
                    artifacts,build=android_release.candidates(root,'1.1.4','same')
                    manifest={'artifacts':artifacts,'build':{'android':build}}
                    android_release.validate_inventory(manifest,'1.1.4')
                    manifest['artifacts'].pop()
                    with self.assertRaises(ValueError):android_release.validate_inventory(manifest,'1.1.4')

    def test_historical_release_inventory_unchanged(self):
        self.assertEqual(android_release.packages('1.1.3'),[])
        android_release.validate_inventory({'artifacts':[]},'1.1.3')

if __name__=='__main__':unittest.main()
