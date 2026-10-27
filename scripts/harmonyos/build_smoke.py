"""Generate and build an unsigned minimal HAP using installed tools only."""
import argparse
import json
import zipfile
import os
from pathlib import Path
import shutil
import subprocess
from setup_macos import ROOT, clean_env, cli_root, cli_pin
from go_toolchain import inspect_installation


def write(root, name, value):
    path=root/name
    path.parent.mkdir(parents=True,exist_ok=True)
    path.write_text(json.dumps(value,indent=2)+'\n')


def main():
    argparse.ArgumentParser(description=__doc__).parse_args()
    version=(ROOT/'internal/version/VERSION').read_text().strip()
    pin=json.loads((ROOT / 'apps/harmonyos/toolchains.json').read_text())
    go=inspect_installation()['go']
    cli=cli_root()
    project=ROOT/'.dev/harmonyos/hap-smoke'
    source=ROOT/'apps/harmonyos/diagnostics'
    project.mkdir(parents=True,exist_ok=True)
    env=clean_env()
    env.update(DEVECO_SDK_HOME=str(cli/'sdk'),DEVECO_NODE_HOME=str(cli/'tool/node'),
               PATH=str(cli/'tool/node/bin')+os.pathsep+env.get('PATH',''))
    # Exported Go functions are built from checked-in source, not generated probes.
    libs=project/'entry/libs/arm64-v8a'
    libs.mkdir(parents=True,exist_ok=True)
    cc=ROOT/'.dev/harmonyos/go-smoke/clang-ohos'
    if not cc.exists():
        raise SystemExit('Run setup-harmonyos-macos.sh first to prepare the compiler wrapper')
    target_env=dict(env,GOOS='openharmony',GOARCH='arm64',CGO_ENABLED='1',CC=str(cc),GOTOOLCHAIN='local')
    subprocess.run([go,'build','-buildmode=c-shared','-o',str(libs/'libharmonygo.so'),*[str(p) for p in sorted((source/'go').glob('*.go')) if not p.name.endswith('_test.go')]],cwd=ROOT,env=target_env,check=True)
    write(project,'oh-package.json5',{'modelVersion':'6.0.0','name':'harmony-runtime-smoke','version':'1.0.0','dependencies':{}})
    write(project,'hvigor/hvigor-config.json5',{'modelVersion':'6.0.0','dependencies':{}})
    write(project,'build-profile.json5',{'app':{'signingConfigs':[],'products':[{'name':'default','compatibleSdkVersion':'6.0.0(20)','compileSdkVersion':'26.0.0','targetSdkVersion':'26.0.0','runtimeOS':'HarmonyOS'}],'buildModeSet':[{'name':'debug'}]},'modules':[{'name':'entry','srcPath':'./entry','targets':[{'name':'default','applyToProducts':['default']}]}]})
    write(project,'AppScope/app.json5',{'app':{'bundleName':'cn.rectcircle.fncpn.harmonysmoke','vendor':'rectcircle','versionCode':1,'versionName':version,'icon':'$media:icon','label':'$string:app_name'}})
    write(project,'AppScope/resources/base/element/string.json',{'string':[{'name':'app_name','value':'Go runtime verification'}]})
    icon=project/'AppScope/resources/base/media/icon.svg';icon.parent.mkdir(parents=True,exist_ok=True);icon.write_text('<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96"><rect width="96" height="96" rx="16" fill="#1976d2"/></svg>')
    for scope,task in [('', 'appTasks'),('entry/', 'hapTasks')]:
        (project/(scope+'hvigorfile.ts')).write_text("import { "+task+" } from '@ohos/hvigor-ohos-plugin';\nexport default {system: "+task+", plugins: []};\n")
    write(project,'entry/oh-package.json5',{'name':'entry','version':'1.0.0','dependencies':{'libharmonybridge.so':'file:./src/main/cpp/types/libharmonybridge'}})
    write(project,'entry/build-profile.json5',{'apiType':'stageMode','buildOption':{'externalNativeOptions':{'path':'./src/main/cpp/CMakeLists.txt','arguments':'','cppFlags':'','abiFilters':['arm64-v8a']}},'targets':[{'name':'default'}]})
    write(project,'entry/src/main/module.json5',{'module':{'name':'entry','type':'entry','mainElement':'EntryAbility','deviceTypes':['phone','tablet'],'deliveryWithInstall':True,'installationFree':False,'pages':'$profile:main_pages','abilities':[{'name':'EntryAbility','srcEntry':'./ets/entryability/EntryAbility.ets','exported':True,'label':'$string:app_name','icon':'$media:icon','startWindowIcon':'$media:icon','startWindowBackground':'$color:start_background','skills':[{'entities':['entity.system.home'],'actions':['action.system.home']}]}],'extensionAbilities':[{'name':'VpnProbeAbility','srcEntry':'./ets/vpnability/VpnProbeAbility.ets','type':'vpn','exported':False}],'requestPermissions':[{'name':'ohos.permission.INTERNET'}]}})
    write(project,'entry/src/main/resources/base/profile/main_pages.json',{'src':['pages/Index']})
    write(project,'entry/src/main/resources/base/element/string.json',{'string':[{'name':'app_name','value':'Go runtime verification'}]})
    write(project,'entry/src/main/resources/base/element/color.json',{'color':[{'name':'start_background','value':'#FFFFFF'}]})
    media=project/'entry/src/main/resources/base/media';media.mkdir(parents=True,exist_ok=True);shutil.copyfile(icon,media/'icon.svg')
    for name,dest in [('EntryAbility.ets','entryability'),('Index.ets','pages'),('VpnProbeAbility.ets','vpnability')]:
        folder=project/'entry/src/main/ets'/dest;folder.mkdir(parents=True,exist_ok=True);shutil.copyfile(source/'ets'/name,folder/name)
    cpp=project/'entry/src/main/cpp';cpp.mkdir(parents=True,exist_ok=True)
    for name in ['bridge.cpp','CMakeLists.txt']:shutil.copyfile(source/'cpp'/name,cpp/name)
    write(project,'entry/src/main/cpp/types/libharmonybridge/oh-package.json5',{'name':'libharmonybridge.so','version':'1.0.0','types':'./Index.d.ts'})
    (cpp/'types/libharmonybridge/Index.d.ts').write_text('export const run: (url: string) => Promise<string>;\nexport const vpn: (action: number, fd: number) => Promise<string>;\n')
    subprocess.run([cli/'bin/ohpm','install'],cwd=project,env=env,check=True)
    subprocess.run([cli/'bin/hvigorw','--mode','module','-p','product=default','-p','module=entry@default','assembleHap','--no-daemon'],cwd=project,env=env,check=True)
    hap=project/'entry/build/default/outputs/default/entry-default-unsigned.hap'
    with zipfile.ZipFile(hap) as archive:
        names=archive.namelist()
        for library in ('libharmonybridge.so','libharmonygo.so'):
            if not any(name.endswith('/'+library) for name in names):
                raise RuntimeError('Missing native library in HAP: '+library)
        if 'ets/modules.abc' not in names:
            raise RuntimeError('Missing compiled ArkTS in HAP')
    print('Verified unsigned HAP: '+str(hap))

if __name__=='__main__':main()
