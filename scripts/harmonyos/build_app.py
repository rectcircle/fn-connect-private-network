"""Build the checked-in HarmonyOS application with pinned installed tools."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import zipfile
import shutil
from setup_macos import ROOT, clean_env
from go_toolchain import inspect_installation


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--application-tunnel', action='store_true', help='Build simulator variant: real authentication and application-only tunnel, no system VPN')
    parser.add_argument('--release', action='store_true', help='Build release mode for signed device candidates')
    args=parser.parse_args()
    product='simulator' if args.application_tunnel else 'default'
    target_name='default'
    app=ROOT/'apps/harmonyos'
    pin=json.loads((app/'toolchains.json').read_text())
    cli=Path.home()/'.local/opt/harmonyos/command-line-tools'/pin['cli']['version']
    native=cli/'sdk/default/openharmony/native'
    env=clean_env()
    env.update(DEVECO_SDK_HOME=str(cli/'sdk'),DEVECO_NODE_HOME=str(cli/'tool/node'),PATH=str(cli/'tool/node/bin')+os.pathsep+env.get('PATH',''))
    build=ROOT/'.dev/harmonyos/app';build.mkdir(parents=True,exist_ok=True)
    import shlex
    cc=build/'clang-ohos'
    cc.write_text('#!/bin/sh\nexec '+shlex.quote(str(native/'llvm/bin/clang'))+' --target=aarch64-linux-ohos --sysroot='+shlex.quote(str(native/'sysroot'))+' "$@"\n');cc.chmod(0o755)
    libs=app/'entry/libs/arm64-v8a';libs.mkdir(parents=True,exist_ok=True)
    go=inspect_installation()['go']
    notices=app/'entry/src/main/resources/rawfile';notices.mkdir(parents=True,exist_ok=True)
    shutil.copyfile(ROOT/'THIRD_PARTY_NOTICES.md',notices/'THIRD_PARTY_NOTICES.md')
    shutil.copyfile(Path(go).resolve().parent.parent/'LICENSE',notices/'COMMUNITY_GO_LICENSE')
    target=dict(env,GOOS='openharmony',GOARCH='arm64',CGO_ENABLED='1',CC=str(cc),GOTOOLCHAIN='local')
    subprocess.run([go,'build','-buildmode=c-shared','-o',libs/'libfncpn.so','./apps/harmonyos/go'],cwd=ROOT,env=target,check=True)
    version=(ROOT/'internal/version/VERSION').read_text().strip()
    info=app/'AppScope/app.json5';data=json.loads(info.read_text());data['app']['versionName']=version;data['app']['versionCode']=int((app/'BUILD_NUMBER').read_text().strip());info.write_text(json.dumps(data,indent=2)+'\n')
    for qualifier in ['base', 'en_US']:
        strings=app/('entry/src/main/resources/'+qualifier+'/element/string.json')
        rows=json.loads(strings.read_text())
        for item in rows['string']:
            if item['name']=='product_version':item['value']=version
        strings.write_text(json.dumps(rows,indent=2)+'\n')
    subprocess.run([cli/'bin/ohpm','install'],cwd=app,env=env,check=True)
    subprocess.run([cli/'bin/hvigorw','--mode','module','-p','product='+product,'-p','module=entry@'+target_name,'-p','buildMode='+('release' if args.release else 'debug'),'assembleHap','--no-daemon'],cwd=app,env=env,check=True)
    outputs=app/'entry/build'/product/('outputs/'+target_name)
    matches=list(outputs.glob('*-unsigned.hap'))
    if len(matches)!=1: raise RuntimeError('Expected exactly one unsigned HAP in '+str(outputs))
    hap=matches[0]
    with zipfile.ZipFile(hap) as archive:
        names=archive.namelist()
        module=json.loads(archive.read('module.json'))
        if module['app']['versionName'] != version or module['app']['versionCode'] != int((app/'BUILD_NUMBER').read_text()): raise RuntimeError('HAP version metadata does not match source')
        expected=['phone','tablet','2in1']
        if sorted(module['module']['deviceTypes']) != sorted(expected): raise RuntimeError('Unexpected device distribution')
        if not all(any(n.endswith('/'+library) for n in names) for library in ['libfncpn.so','libfncpnbridge.so']):raise RuntimeError('Native libraries missing')
    (outputs/'fncpn-build.json').write_text(json.dumps({'buildMode': 'release' if args.release else 'debug', 'sha256': hashlib.sha256(hap.read_bytes()).hexdigest()})+'\n')
    print(hap)

if __name__=='__main__':main()
