#!/usr/bin/env python3
"""Promote only fully verified, published-package candidates; never invent checksums."""
import json,os,pathlib
root=pathlib.Path(__file__).resolve().parents[1]
entries=json.loads((root/'packages/catalog/versions.json').read_text())
reports=[json.loads(p.read_text()) for p in (root/'dist/reports').glob('*.json')]
assert len(reports)==24, f'expected 24 acceptance reports, received {len(reports)}'
tag=os.environ['SOFTWARE_TAG'];repo=os.environ['GITHUB_REPOSITORY']
seen=set()
for report in reports:
    key=(report['catalogId'],report['system'])
    assert key not in seen;seen.add(key)
    assert report['binaryVerified'] and report['protocolVerified'] and report['runtimeVerified']
    assert report['systemdVerified']
    entry=next(e for e in entries if e['id']==report['catalogId'])
    systems=entry.setdefault('verifiedSystems',{}).setdefault('binary',[])
    if report['system'] not in systems: systems.append(report['system'])
    if not report['official']:
        name=report['catalogId']+'-'+report['system'].replace(':','-')+'.tar.gz'
        entry.setdefault('binaries',[]).append({'system':report['system'],'source':f'https://github.com/{repo}/releases/download/{tag}/{name}','sha256':report['sha256'],'buildPrefix':report['buildPrefix'],'runtimePackages':report['runtimePackages']})
        source=entry['verifiedSystems'].setdefault('source',[])
        if report['system'] not in source: source.append(report['system'])
        entry['methods']=['binary','source']
    else: entry['methods']=['binary']
    entry['recommended']=True
    entry['recipeRevision']='2'
    entry.pop('disabledReason',None)
(root/'dist/verified-catalog.json').write_text(json.dumps(entries,ensure_ascii=False,indent=2)+'\n')
(root/'dist/native-acceptance.json').write_text(json.dumps({'softwareTag':tag,'commit':os.environ['GITHUB_SHA'],'reports':reports},ensure_ascii=False,indent=2)+'\n')
