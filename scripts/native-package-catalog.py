#!/usr/bin/env python3
"""Promote only fully verified, published-package candidates; never invent checksums."""
import json,os,pathlib
root=pathlib.Path(__file__).resolve().parents[1]
entries=json.loads((root/'packages/catalog/versions.json').read_text())
reports=[json.loads(p.read_text()) for p in (root/'dist/packages').glob('*.tar.gz.json')]
targets=json.loads((root/'packages/catalog/native-targets.json').read_text())
systems={'ubuntu:22.04','ubuntu:24.04','debian:12','debian:13'}
expected={(entry,system) for entry in targets['entries'] for system in systems}
assert len(reports)==len(expected), f'expected {len(expected)} acceptance reports, received {len(reports)}'
tag=os.environ['SOFTWARE_TAG'];repo=os.environ['GITHUB_REPOSITORY']
seen=set()
for report in reports:
    key=(report['catalogId'],report['system'])
    assert key in expected and key not in seen;seen.add(key)
    assert report['binaryVerified'] and report['protocolVerified'] and report['runtimeVerified']
    assert report['systemdVerified']
    entry=next(e for e in entries if e['id']==report['catalogId'])
    systems=entry.setdefault('verifiedSystems',{}).setdefault('binary',[])
    if report['system'] not in systems: systems.append(report['system'])
    if not report['official']:
        name=report['catalogId']+'-'+report['system'].replace(':','-')+'.tar.gz'
        entry['binaries']=[a for a in entry.get('binaries',[]) if a['system'] != report['system']]
        entry['binaries'].append({'system':report['system'],'source':f'https://github.com/{repo}/releases/download/{tag}/{name}','sha256':report['sha256'],'buildPrefix':report['buildPrefix'],'runtimePackages':report['runtimePackages']})
        source=entry['verifiedSystems'].setdefault('source',[])
        if report['system'] not in source: source.append(report['system'])
        entry['methods']=['binary','source']
    else: entry['methods']=['binary']
    entry['recipeRevision']='3'
    entry.pop('disabledReason',None)
assert seen==expected
(root/'dist/verified-catalog.json').write_text(json.dumps(entries,ensure_ascii=False,indent=2)+'\n')
(root/'dist/native-acceptance.json').write_text(json.dumps({'softwareTag':tag,'commit':os.environ['GITHUB_SHA'],'reports':reports},ensure_ascii=False,indent=2)+'\n')
