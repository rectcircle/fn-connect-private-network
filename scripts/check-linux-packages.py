#!/usr/bin/env python3
"""Inspect actual deb/RPM/Arch metadata, payloads and ELF without installing them."""
import gzip
import io
import pathlib
import struct
import subprocess
import sys
import tarfile
from linux_support import package_name


def normalized(name):
    while name.startswith('./'): name=name[2:]
    return name.lstrip('/')


def ar_members(data):
    if not data.startswith(b'!<arch>\n'): raise ValueError('Not an ar archive')
    offset=8;result={}
    while offset<len(data):
        header=data[offset:offset+60]
        if len(header)!=60 or header[58:60]!=b'`\n': raise ValueError('Invalid ar header')
        name=header[:16].decode().strip().rstrip('/');size=int(header[48:58]);offset+=60
        result[name]=data[offset:offset+size];offset+=size+(size%2)
    return result


def tar_members(data):
    with tarfile.open(fileobj=io.BytesIO(data),mode='r:*') as archive:
        return {normalized(m.name):(m.mode,m.uid,m.gid,archive.extractfile(m).read() if m.isfile() else None)
                for m in archive.getmembers()}


def rpm_header(data,offset):
    if data[offset:offset+4]!=b'\x8e\xad\xe8\x01':raise ValueError('Invalid RPM header')
    count,size=struct.unpack('>II',data[offset+8:offset+16])
    store=offset+16+16*count;tags={}
    for i in range(count):
        tag,kind,start,length=struct.unpack('>IIII',data[offset+16+16*i:offset+32+16*i])
        value=data[store+start:store+size]
        if kind in (6,8,9):tags[tag]=[s.decode() for s in value.split(b'\0')[:length]]
        elif kind==4:tags[tag]=list(struct.unpack('>'+str(length)+'I',value[:4*length]))
    return tags,store+size


def rpm_payload(data):
    _,end=rpm_header(data,96)
    tags,end=rpm_header(data,(end+7)//8*8)
    payload=gzip.decompress(data[end:])
    result={};offset=0
    while offset<len(payload):
        if payload[offset:offset+6] not in (b'070701',b'070702'):raise ValueError('Invalid RPM cpio payload')
        fields=[int(payload[offset+6+i*8:offset+14+i*8],16) for i in range(13)]
        mode,uid,gid,size,namesize=fields[1],fields[2],fields[3],fields[6],fields[11]
        name=payload[offset+110:offset+110+namesize-1].decode()
        if name=='TRAILER!!!':break
        offset=(offset+110+namesize+3)//4*4
        result[normalized(name)]=(mode,uid,gid,payload[offset:offset+size])
        offset=(offset+size+3)//4*4
    requires=[]
    for name,version,flags in zip(tags.get(1049,[]),tags.get(1050,[]),tags.get(1048,[])):
        requires.append(name+' = '+version if flags&8 and not flags&6 else name)
    return {'Package':tags[1000][0],'Version':tags[1001][0],
            'Architecture':tags[1022][0],'Depends':', '.join(requires)},result


def inspect(path, *, installed_root=None):
    data=path.read_bytes()
    if path.suffix=='.deb':
        archive=ar_members(data)
        controls=tar_members(next(v for k,v in archive.items() if k.startswith('control.tar')))
        fields={line.split(': ',1)[0]:line.split(': ',1)[1]
                for line in controls['control'][3].decode().splitlines() if ': ' in line and not line.startswith(' ')}
        payload=tar_members(next(v for k,v in archive.items() if k.startswith('data.tar')))
        manager='deb'
    elif path.suffix=='.rpm':
        fields,payload=rpm_payload(data);manager='rpm'
    else:
        manager='archlinux'
        try:
            from compression import zstd
            data=zstd.decompress(data)
        except ImportError:
            # Python <3.14 in distro CI images uses the system libarchive decoder.
            # Repack selected files as an uncompressed tar for the same verifier.
            process=subprocess.run(['bsdtar','-cf','-','--format=ustar','@'+str(path)],capture_output=True,check=True)
            data=process.stdout
        payload=tar_members(data)
        info=payload['.PKGINFO'][3].decode()
        values={}
        for line in info.splitlines():
            if ' = ' in line:
                k,v=line.split(' = ',1);values.setdefault(k,[]).append(v)
        fields={'Package':values['pkgname'][0],'Version':values['pkgver'][0].rsplit('-',1)[0],
                'Architecture':values['arch'][0],'Depends':', '.join(values.get('depend',[]))}
    architecture={'x86_64':'amd64','aarch64':'arm64','loongarch64':'loong64'}.get(fields['Architecture'],fields['Architecture'])
    fields['Architecture']=architecture
    name=fields['Package']
    if name not in ('fncpn-core','fncpn-ui'):raise ValueError('Unexpected package '+name)
    binary='usr/bin/fncpn' if name=='fncpn-core' else 'usr/bin/fncpn-ui'
    if binary not in payload:raise ValueError('Missing '+binary)
    mode,uid,gid,elf=payload[binary]
    if installed_root is not None and (pathlib.Path(installed_root)/binary).read_bytes() != elf:
        raise ValueError('Installed binary differs from package payload: '+binary)
    if len(elf)<20 or elf[:6]!=b'\x7fELF\x02\x01' or struct.unpack('<H',elf[18:20])[0]!={'arm64':183,'amd64':62,'loong64':258}.get(architecture):raise ValueError('Wrong ELF architecture')
    if mode&0o111==0 or uid!=0 or gid!=0:raise ValueError('Invalid binary owner or mode')
    expected=['usr/lib/systemd/user/fncpn-client.service','usr/lib/systemd/system/fncpn-client-privileged@.service',
              'usr/libexec/fncpn/enable-client','usr/libexec/fncpn/wait-ready',
              'usr/share/polkit-1/actions/cn.rectcircle.fncpn.enable-client.policy'] if name=='fncpn-core' else ['usr/share/applications/fncpn.desktop']
    for required in expected:
        if required not in payload:raise ValueError('Missing '+required)
    if name=='fncpn-ui':
        version=fields['Version']
        expression=f'fncpn-core (= {version})' if manager=='deb' else f'fncpn-core = {version}' if manager=='rpm' else f'fncpn-core={version}'
        if expression not in fields['Depends'].split(', '):raise ValueError('UI must depend on exact Core version')
    return fields


def validate_target(fields, path, target, arch, version):
    component = fields['Package'].removeprefix('fncpn-')
    if fields['Architecture'] != arch or fields['Version'] != version:
        raise ValueError('Package metadata does not match target architecture/version')
    if path.name != package_name(component,version,target,arch):
        raise ValueError('Package name/format does not match target')


if __name__=='__main__':
    target=pathlib.Path(sys.argv[1])
    paths=sorted(p for p in target.iterdir() if p.name.endswith(('.deb','.rpm','.pkg.tar.zst'))) if target.is_dir() else [target]
    if not paths:raise SystemExit('No Linux packages found')
    version=(pathlib.Path(__file__).resolve().parent.parent/'internal/version/VERSION').read_text().strip()
    for path in paths:
        fields=inspect(path)
        if fields['Version']!=version:raise SystemExit('Package version does not match source: '+str(path))
        if len(sys.argv)>3:validate_target(fields,path,sys.argv[2],sys.argv[3],version)
        print(f"Verified {fields['Package']} {fields['Version']} {fields['Architecture']}: {path}")
