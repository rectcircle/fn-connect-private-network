#!/usr/bin/env python3
"""Build distribution packages from shared payloads and explicit target profiles."""
import json
import os
import pathlib
import re
import subprocess
import sys
import tempfile

from linux_support import ROOT, package_name, package_profile


def script_body(name, argument=None):
    text = (ROOT/'packaging/linux/scripts'/name).read_text()
    # Each package manager supplies different arguments. Translate explicitly,
    # rather than passing RPM counts or pacman version strings to dpkg hooks.
    return ('set -- '+argument+'\n' if argument else '') + text.removeprefix('#!/bin/sh\n')


def lifecycle(component, manager, directory):
    scripts = {}
    def add(stage, body):
        path = directory/(component+'-'+stage)
        path.write_text('#!/bin/sh\nset -eu\n'+body+'\n')
        scripts[stage] = str(path)
    if manager == 'deb':
        if component == 'core':
            for stage, name in [('preinstall','prerm'),('postinstall','postinst'),('preremove','prerm'),('postremove','postrm')]:
                add(stage, script_body(name))
        else:
            add('preinstall', script_body('ui-prerm','upgrade'))
            add('preremove', script_body('ui-prerm'))
            add('postinstall', script_body('ui-postinst'))
    elif manager == 'rpm':
        if component == 'core':
            add('preinstall', 'if [ "${1:-1}" -gt 1 ]; then\n'+script_body('prerm','upgrade')+'\nfi')
            add('preremove', 'if [ "${1:-0}" = 0 ]; then\n'+script_body('prerm','remove')+'\nfi')
            add('postremove', 'if [ "${1:-0}" = 0 ]; then\n'+script_body('postrm')+'\nfi')
            # Restart only after both new packages are in place. The old RPM's
            # %preun/%postun during upgrade must never stop the new services.
            add('posttrans', script_body('postinst','configure'))
        else:
            add('preinstall', script_body('ui-prerm','upgrade'))
            add('preremove', 'if [ "${1:-0}" = 0 ]; then\n'+script_body('ui-prerm','remove')+'\nfi')
            add('postinstall', script_body('ui-postinst'))
    else:
        if component == 'core':
            add('preupgrade', script_body('prerm','upgrade'))
            add('preremove', script_body('prerm','remove'))
            add('postinstall', script_body('postinst','configure'))
            add('postupgrade', script_body('postinst','configure'))
            add('postremove', script_body('postrm'))
        else:
            add('preupgrade', script_body('ui-prerm','upgrade'))
            add('preremove', script_body('ui-prerm','remove'))
            add('postinstall', script_body('ui-postinst'))
            add('postupgrade', script_body('ui-postinst'))
    generic = {k:v for k,v in scripts.items() if k in ('preinstall','postinstall','preremove','postremove')}
    special = {k:v for k,v in scripts.items() if k not in generic}
    return generic, special


def elf_dependencies(binary, profile):
    """Capture the actual UI ABI requirements; never guess them from the OS age."""
    if profile['format'] == 'deb':
        with tempfile.TemporaryDirectory(prefix='fncpn-shlibs-') as temporary:
            directory = pathlib.Path(temporary)
            (directory/'debian').mkdir()
            (directory/'debian/control').write_text('Source: fncpn\nSection: net\nPriority: optional\nMaintainer: rectcircle <rectcircle96@gmail.com>\n\nPackage: fncpn-ui\nArchitecture: any\nDescription: FnCPN UI\n')
            output = subprocess.check_output(['dpkg-shlibdeps','-O','-e'+str(pathlib.Path(binary).resolve())],cwd=directory,text=True)
        line = next(line for line in output.splitlines() if line.startswith('shlibs:Depends='))
        dependencies = line.split('=',1)[1].split(', ')
        renamed = {'libgtk-3-0', 'libglib2.0-0', 'libatk1.0-0', 'libatk-bridge2.0-0', 'libasound2', 'libcups2', 'libpng16-16'}
        return [re.sub(r'^(\S+)(.*)$', lambda m: m[0]+' | '+m[1]+'t64'+m[2] if m[1] in renamed else m[0], item) for item in dependencies]
    output = subprocess.check_output(['readelf','--version-info',binary],text=True)
    if profile['format'] == 'archlinux':
        versions = re.findall(r'\bGLIBC_([0-9]+(?:\.[0-9]+)+)',output)
        if not versions:
            raise ValueError('UI has no inspectable glibc version requirements')
        minimum = max(versions,key=lambda v:tuple(map(int,v.split('.'))))
        return ['glibc>='+minimum]
    dynamic = subprocess.check_output(['readelf','--dynamic',binary],text=True)
    dependencies = {name+'()(64bit)' for name in re.findall(r'Shared library: \[([^\]]+)\]',dynamic)}
    library = None
    for line in output.splitlines():
        match = re.search(r'File: (\S+)',line)
        if match: library = match[1]
        match = re.search(r'Name: (\S+)',line)
        if library and match:
            dependencies.add(library+'('+match[1]+')(64bit)')
    return sorted(dependencies)


def configuration(component, target, arch, version, binary, directory, runtime_dependencies=()):
    profile = package_profile(target,arch)
    config = json.loads((ROOT/f'packaging/linux/nfpm-{component}.json').read_text())
    config.update(arch=arch,version=version)
    dependencies = list(profile['coreDependencies' if component=='core' else 'uiDependencies'])
    if component == 'ui':
        separator = f'fncpn-core (= {version})' if profile['format']=='deb' else f'fncpn-core = {version}' if profile['format']=='rpm' else f'fncpn-core={version}'
        dependencies.insert(0,separator)
        # Drop redundant unversioned entries when the ABI scanner found a bound.
        dependencies = [value for value in dependencies if not any(value+' ' in item for item in runtime_dependencies)]
        dependencies.extend(runtime_dependencies)
    config['depends'] = sorted(set(dependencies))
    variable = '${FNCPN_'+component.upper()+'_BINARY}'
    for item in config['contents']:
        if item['src']==variable: item['src']=str(pathlib.Path(binary).resolve())
    generic,special = lifecycle(component,profile['format'],directory)
    if arch == 'loong64' and profile['format'] == 'deb':
        preinstall = pathlib.Path(generic['preinstall'])
        preinstall.write_text('#!/bin/sh\n' + script_body('check-loongarch') + '\n' + preinstall.read_text().removeprefix('#!/bin/sh\n'))
    config['scripts'] = generic
    if profile['format']=='deb':
        config['section']='net';config['priority']='optional'
        if component=='ui':config['recommends']=['libnotify-bin']
    elif profile['format']=='rpm':
        config['rpm']={'scripts':special}
        if profile.get('packageArch'): config['rpm']['arch'] = profile['packageArch']
    else:
        config['archlinux']={'scripts':special,'pkgbase':'fncpn-'+component}
        if profile.get('packageArch'): config['archlinux']['arch'] = profile['packageArch']
    return config


def main():
    component,target,arch,binary,out = sys.argv[1:]
    if component not in ('core','ui'): raise SystemExit('component must be core or ui')
    profile = package_profile(target,arch)
    version = (ROOT/'internal/version/VERSION').read_text().strip()
    output = pathlib.Path(out).resolve()/package_name(component,version,target,arch)
    if os.environ.get('RELEASE')=='1' and output.exists(): raise SystemExit('Refusing to overwrite release package')
    runtime = elf_dependencies(binary,profile) if component=='ui' else []
    with tempfile.TemporaryDirectory(prefix='fncpn-package-') as temporary:
        directory=pathlib.Path(temporary)
        config=configuration(component,target,arch,version,binary,directory,runtime)
        path=directory/'nfpm.json';path.write_text(json.dumps(config))
        subprocess.run([os.environ.get('NFPM',str(pathlib.Path.home()/'go/bin/nfpm')),'package','-f',str(path),'--packager',profile['format'],'--target',str(output)],cwd=ROOT,check=True)
    print(output)


if __name__ == '__main__': main()
