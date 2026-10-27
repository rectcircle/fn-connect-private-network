"""Collect a built HAP and optionally sign it using external developer credentials."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile
from setup_macos import ROOT, cli_root, cli_pin
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from harmonyos_release import verify_hap, CHECKS


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sign',action='store_true',help='Sign with external certificate/profile/keystore; prompts for passwords')
    parser.add_argument('--application-tunnel',action='store_true',help='Collect simulator variant with real authentication and application-only tunnel')
    parser.add_argument('--release',action='store_true',help='Require a verified release-mode build')
    args=parser.parse_args()
    product='simulator' if args.application_tunnel else 'default'
    target_name='default'
    app=ROOT/'apps/harmonyos';pin=json.loads((app/'toolchains.json').read_text())
    version=(ROOT/'internal/version/VERSION').read_text().strip();number=int((app/'BUILD_NUMBER').read_text())
    matches=list((app/'entry/build'/product/('outputs/'+target_name)).glob('*-unsigned.hap'))
    if len(matches)!=1:raise SystemExit('Build requested variant first: bash scripts/build-harmonyos.sh'+(' --application-tunnel' if args.application_tunnel else ''))
    source=matches[0]
    with zipfile.ZipFile(source) as archive:
        actual=json.loads(archive.read('module.json'))['app']
    if actual['versionName'] != version or actual['versionCode'] != number:
        raise SystemExit('HAP version differs from source. Rebuild before packaging.')
    metadata=source.parent/'fncpn-build.json'
    build_info=json.loads(metadata.read_text()) if metadata.is_file() else {}
    if args.release and (build_info.get('buildMode')!='release' or build_info.get('sha256')!=hashlib.sha256(source.read_bytes()).hexdigest()):
        raise SystemExit('Build release candidate first: bash scripts/build-harmonyos.sh --release')
    root=ROOT/'dist/harmonyos';root.mkdir(parents=True,exist_ok=True)
    suffix=('-application-tunnel' if args.application_tunnel else '')+('-release' if args.release else '')
    package_suffix='-application-tunnel' if args.application_tunnel else ''
    output=root/f'fncpn-{version}-harmonyos-arm64{package_suffix}-unsigned.hap'
    shutil.copyfile(source,output)
    checks = []
    if args.release and not args.application_tunnel:
        verify_hap(source,version,number)
        checks = sorted(CHECKS)
    if args.sign:
        if args.application_tunnel or not args.release:
            raise SystemExit('Release signing requires the default system-VPN release product')
        verify_hap(source,version,number)
        required=['HARMONYOS_KEYSTORE','HARMONYOS_KEY_ALIAS','HARMONYOS_APP_CERT','HARMONYOS_PROFILE']
        missing=[name for name in required if not os.environ.get(name)]
        if missing:raise SystemExit('Missing signing configuration: '+', '.join(missing))
        for name in ['HARMONYOS_KEYSTORE','HARMONYOS_APP_CERT','HARMONYOS_PROFILE']:
            if not Path(os.environ[name]).is_file():raise SystemExit('Signing file missing: '+name)
        cli=cli_root()
        jar=cli/'sdk/default/openharmony/toolchains/lib/hap-sign-tool.jar'
        signed=output.with_name(output.name.replace('-unsigned',''))
        subprocess.run(['java','-jar',jar,'sign-app','-mode','localSign',
                        '-keyAlias',os.environ['HARMONYOS_KEY_ALIAS'],'-keystoreFile',os.environ['HARMONYOS_KEYSTORE'],
                        '-appCertFile',os.environ['HARMONYOS_APP_CERT'],'-profileFile',os.environ['HARMONYOS_PROFILE'],
                        '-inFile',output,'-outFile',signed,'-signAlg','SHA256withECDSA','-compatibleVersion','20',
                        '-pwdInputMode','1'],check=True)
        if not signed.is_file():raise SystemExit('Signer did not produce a HAP')
        subprocess.run(['java','-jar',jar,'verify-app','-inFile',signed,'-outCertChain',root/'certificate-chain.cer','-outProfile',root/'profile.p7b'],check=True)
        output=signed
    manifest={'productVersion':version,'buildNumber':number,'commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True).strip(),
              'dirty':bool(subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True).strip()),
              'cli':cli_pin()['version'],'go':pin['goPort'],'abi':'arm64-v8a','product':product,'applicationTunnel':args.application_tunnel,'signed':args.sign,'buildMode':build_info.get('buildMode','unknown'),
              'checks':checks if not args.sign else sorted((CHECKS - {'unsigned-package'}) | {'official-signature-verification'}),
              'filename':output.name,'sha256':hashlib.sha256(output.read_bytes()).hexdigest()}
    (root/('build-manifest'+suffix+'.json')).write_text(json.dumps(manifest,indent=2)+'\n')
    shutil.copyfile(ROOT/'THIRD_PARTY_NOTICES.md',root/'THIRD_PARTY_NOTICES.md')
    print(output)


if __name__=='__main__':main()
