"""Build Go artifacts for the Xcode app/extension targets using the selected SDK."""
import argparse
import os
from pathlib import Path
import platform
import re
import shutil
import shlex
import subprocess
import fyne_scene

ROOT = Path(__file__).resolve().parents[2]

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--component',choices=['app','core'],required=True)
    parser.add_argument('--sdk',choices=['iphoneos','iphonesimulator'],required=True)
    parser.add_argument('--out',type=Path,required=True)
    args=parser.parse_args()
    args.out.mkdir(parents=True,exist_ok=True)
    arch='arm64' if args.sdk=='iphoneos' or platform.machine()=='arm64' else 'amd64'
    clang_arch='x86_64' if arch=='amd64' else arch
    sdk=subprocess.check_output(['xcrun','--sdk',args.sdk,'--show-sdk-path'],text=True).strip()
    target=f'{clang_arch}-apple-ios15.0'+('-simulator' if args.sdk=='iphonesimulator' else '')
    cc=args.out/'clang-wrapper'
    cc.write_text('#!/bin/sh\nfor arg do\n case "${arg##*/}" in ui_ios.m) set -- -fobjc-arc "$@"; break ;; esac\ndone\nexec xcrun --sdk '+args.sdk+' clang -isysroot '+shlex.quote(sdk)+' -target '+target+' "$@"\n')
    cc.chmod(0o755)
    env=dict(os.environ,GOOS='ios',GOARCH=arch,CGO_ENABLED='1',CC=str(cc),CXX=str(cc),GOTOOLCHAIN='local')
    for key in ('SDKROOT','CGO_CFLAGS','CGO_LDFLAGS','GOFLAGS'):
        env.pop(key,None)
    if subprocess.check_output(['go','env','GOVERSION'],env=env,text=True).strip()!='go1.27.1':
        raise SystemExit('Go 1.27.1 is required; run the environment setup first')
    # Enable ARC only for our UIKit adapter; leave upstream C/Objective-C flags unchanged.
    tags='no_emoji,migrated_fynedo'+(',fncpn_ios_simulator' if args.sdk=='iphonesimulator' and args.component=='app' else '')
    command=['go','build','-trimpath','-tags',tags]
    if args.component=='core':command+=['-buildmode=c-archive','-o',str(args.out/'FnCPNCore.a'),'./apps/ios/go']
    else:
        modfile=fyne_scene.prepare('go',ROOT/'apps/shared/fyne',args.out,env)
        ldflags='-w'
        if args.sdk=='iphonesimulator':
            # Simulator securityd reads the simulated iOS identity from this
            # section. Do not give restricted iOS entitlements to macOS AMFI.
            ldflags+=' -extldflags=-Wl,-sectcreate,__TEXT,__entitlements,'+str(ROOT/'apps/ios/App/Simulator.entitlements')
        command+=['-modfile',str(modfile),'-ldflags',ldflags,'-o',str(args.out/'FnCPN'),'./']
    subprocess.run(command,cwd=(ROOT if args.component=='core' else ROOT/'apps/shared/fyne'),env=env,check=True)
    if args.component=='app':
        icon_env=dict(os.environ,GOOS='darwin',GOARCH=platform.machine().replace('x86_64','amd64'),CGO_ENABLED='0',GOTOOLCHAIN='local')
        icon_env.pop('GOFLAGS',None)
        subprocess.run(['go','run',str(ROOT/'scripts/ios/icon.go'),str(ROOT/'apps/shared/fyne/assets/appicon.svg'),str(args.out.resolve())],cwd=ROOT/'apps/shared/fyne',env=icon_env,check=True)
        (args.out/'THIRD_PARTY_NOTICES.md').write_text((ROOT/'THIRD_PARTY_NOTICES.md').read_text()+'\n'+(ROOT/'apps/shared/fyne/THIRD_PARTY_NOTICES.md').read_text())
    if args.component=='core':
        shutil.copy2(ROOT/'apps/ios/go/bridge.h',args.out/'bridge.h')
        protocol=re.search(r'const ProtocolVersion = (\d+)',(ROOT/'internal/ipc/protocol.go').read_text()).group(1)
        (args.out/'Bridging.h').write_text('#include "FnCPNCore.h"\n#define FNCPNProtocolVersion '+protocol+'\n')


if __name__=='__main__':main()
