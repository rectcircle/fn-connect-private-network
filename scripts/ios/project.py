"""Generate the small Xcode project without third-party project generators."""
import hashlib
import json
import platform
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
objects={}
def identifier(name):return hashlib.sha256(name.encode()).hexdigest()[:24].upper()
def add(label,**properties):
    key=identifier(label);objects[key]=properties;return key

def encode(value,level=0):
    if isinstance(value,dict):return '{\n'+''.join('\t'*(level+1)+json.dumps(k)+' = '+encode(v,level+1)+';\n' for k,v in value.items())+'\t'*level+'}'
    if isinstance(value,list):return '('+', '.join(encode(v,level) for v in value)+')'
    return json.dumps(str(value))

def main():
    products=add('products',isa='PBXGroup',children=[identifier('app-product'),identifier('extension-product')],name='Products',sourceTree='<group>')
    swift=add('swift',isa='PBXFileReference',lastKnownFileType='sourcecode.swift',path='PacketTunnel/PacketTunnelProvider.swift',sourceTree='<group>')
    group=add('main',isa='PBXGroup',children=[swift,products],sourceTree='<group>')
    for name,path,kind in [('app-product','FnCPN.app','wrapper.application'),('extension-product','PacketTunnel.appex','wrapper.app-extension')]:
        add(name,isa='PBXFileReference',explicitFileType=kind,path=path,sourceTree='BUILT_PRODUCTS_DIR')
    add('swift-build',isa='PBXBuildFile',fileRef=swift)
    sources=add('extension-sources',isa='PBXSourcesBuildPhase',buildActionMask='2147483647',files=[identifier('swift-build')],runOnlyForDeploymentPostprocessing=0)
    embed_file=add('embed-file',isa='PBXBuildFile',fileRef=identifier('extension-product'),settings={'ATTRIBUTES':['RemoveHeadersOnCopy']})
    embed=add('embed',isa='PBXCopyFilesBuildPhase',buildActionMask='2147483647',dstPath='',dstSubfolderSpec=13,files=[embed_file],name='Embed App Extensions',runOnlyForDeploymentPostprocessing=0)
    proxy=add('proxy',isa='PBXContainerItemProxy',containerPortal=identifier('project'),proxyType=1,remoteGlobalIDString=identifier('extension-target'),remoteInfo='PacketTunnel')
    dependency=add('dependency',isa='PBXTargetDependency',target=identifier('extension-target'),targetProxy=proxy)
    for component in ['app','core']:
        script='set -eu\npython3 "$SRCROOT/../scripts/ios/build.py" --component '+component+' --sdk "$PLATFORM_NAME" --out "$SRCROOT/../.dev/ios/$PLATFORM_NAME/'+component+'"\n'
        if component=='app':script+='mkdir -p "$TARGET_BUILD_DIR/$WRAPPER_NAME"\ncp "$SRCROOT/../.dev/ios/$PLATFORM_NAME/app/FnCPN" "$TARGET_BUILD_DIR/$EXECUTABLE_PATH"\n'
        if component=='app':
            for locale in ['en','zh-Hans']:
                script+='mkdir -p "$TARGET_BUILD_DIR/$WRAPPER_NAME/'+locale+'.lproj"\ncp "$SRCROOT/App/'+locale+'.lproj/InfoPlist.strings" "$TARGET_BUILD_DIR/$WRAPPER_NAME/'+locale+'.lproj/InfoPlist.strings"\n'
            for file in ['AppIcon120.png','AppIcon180.png','AppIcon152.png','AppIcon167.png','THIRD_PARTY_NOTICES.md']:
                script+='cp "$SRCROOT/../.dev/ios/$PLATFORM_NAME/app/'+file+'" "$TARGET_BUILD_DIR/$WRAPPER_NAME/'+file+'"\n'
        add(component+'-phase',isa='PBXShellScriptBuildPhase',alwaysOutOfDate=1,buildActionMask='2147483647',files=[],inputPaths=[],outputPaths=(["$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/Bridging.h","$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/FnCPNCore.h","$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/bridge.h","$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/FnCPNCore.a"] if component=='core' else ["$(TARGET_BUILD_DIR)/$(EXECUTABLE_PATH)"]),runOnlyForDeploymentPostprocessing=0,shellPath='/bin/bash',shellScript=script,name='Build shared Go '+component)
    for target in ['project','app','extension']:
        configurations=[]
        for config in ['Debug','Release']:
            settings={}
            if target=='project':settings={'IPHONEOS_DEPLOYMENT_TARGET':'15.0','SDKROOT':'iphoneos','SWIFT_VERSION':'5.0','CLANG_ENABLE_MODULES':'YES','ENABLE_USER_SCRIPT_SANDBOXING':'NO','ONLY_ACTIVE_ARCH':'YES','ARCHS[sdk=iphonesimulator*]':'$(NATIVE_ARCH_ACTUAL)','ARCHS[sdk=iphoneos*]':'arm64','TARGETED_DEVICE_FAMILY':'1,2','FNCPN_BUNDLE_ID':'cn.rectcircle.fncpn','CODE_SIGN_STYLE':'Automatic','CURRENT_PROJECT_VERSION':(ROOT/'ios/BUILD_NUMBER').read_text().strip(),'MARKETING_VERSION':(ROOT/'internal/version/VERSION').read_text().strip()}
            else:
                settings={'PRODUCT_NAME':'FnCPN' if target=='app' else 'PacketTunnel','INFOPLIST_FILE':'App/Info.plist' if target=='app' else 'PacketTunnel/Info.plist','PRODUCT_BUNDLE_IDENTIFIER':'$(FNCPN_BUNDLE_ID)' + ('.PacketTunnel' if target=='extension' else ''),'CODE_SIGN_ENTITLEMENTS':'App/App.entitlements' if target=='app' else 'PacketTunnel/PacketTunnel.entitlements'}
                if target=='extension':settings.update({'APPLICATION_EXTENSION_API_ONLY':'YES','SKIP_INSTALL':'YES','SWIFT_OBJC_BRIDGING_HEADER':'$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/Bridging.h','OTHER_LDFLAGS':['$(inherited)','$(SRCROOT)/../.dev/ios/$(PLATFORM_NAME)/core/FnCPNCore.a','-framework','CoreFoundation','-framework','Security','-framework','Network','-lresolv'],'LD_RUNPATH_SEARCH_PATHS':'$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks'})
            configurations.append(add(target+config,isa='XCBuildConfiguration',buildSettings=settings,name=config))
        add(target+'-configs',isa='XCConfigurationList',buildConfigurations=configurations,defaultConfigurationIsVisible=0,defaultConfigurationName='Debug')
    add('extension-target',isa='PBXNativeTarget',buildConfigurationList=identifier('extension-configs'),buildPhases=[identifier('core-phase'),sources],buildRules=[],dependencies=[],name='PacketTunnel',productName='PacketTunnel',productReference=identifier('extension-product'),productType='com.apple.product-type.app-extension')
    add('app-target',isa='PBXNativeTarget',buildConfigurationList=identifier('app-configs'),buildPhases=[identifier('app-phase'),embed],buildRules=[],dependencies=[dependency],name='FnCPN',productName='FnCPN',productReference=identifier('app-product'),productType='com.apple.product-type.application')
    project=add('project',isa='PBXProject',attributes={'LastUpgradeCheck':'1600'},buildConfigurationList=identifier('project-configs'),compatibilityVersion='Xcode 14.0',developmentRegion='en',knownRegions=['en','zh-Hans'],mainGroup=group,productRefGroup=products,projectDirPath='',projectRoot='',targets=[identifier('app-target'),identifier('extension-target')])
    output=ROOT/'ios/FnCPN.xcodeproj/project.pbxproj';output.parent.mkdir(parents=True,exist_ok=True)
    output.write_text('// !$*UTF8*$!\n'+encode({'archiveVersion':1,'classes':{},'objectVersion':56,'objects':objects,'rootObject':project})+'\n')
if __name__=='__main__':main()
