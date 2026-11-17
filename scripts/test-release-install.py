"""Verify the published one-command entry and ov updater on a clean GitHub VM.
Credentials stay in memory, including subprocess capture; output only test results.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import ssl
import subprocess
import sys
import tempfile
import traceback
import time
import urllib.request

if os.environ.get('GITHUB_ACTIONS') != 'true' or os.geteuid() != 0:
    raise SystemExit('仅在隔离 CI root 主机运行此测试。')
assert not Path('/opt/opsview').exists() and not Path('/var/lib/opsview').exists()
tag = os.environ['RELEASE_TAG']
assert re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?', tag)
base = 'https://github.com/gzdzh-cn/dzh-OpsView/releases/download/' + tag
port = 18790
scheme = 'https'

def report_failure(kind, error, tb):
    location = ' -> '.join(f'{Path(f.filename).name}:{f.lineno}' for f in traceback.extract_tb(tb))
    print(f'::error::Published install verification {kind.__name__} at {location}', flush=True)
    sys.__excepthook__(kind, error, tb)
sys.excepthook = report_failure

def download(url):
    with urllib.request.urlopen(url, timeout=120) as response:
        return response.read()

def run(args):
    result = subprocess.run(list(map(str,args)), capture_output=True, text=True, timeout=300)
    assert result.returncode == 0, f'command failed: {Path(args[0]).name}, status {result.returncode}'
    return result.stdout + result.stderr

def login():
    request=urllib.request.Request(f'{scheme}://127.0.0.1:{port}/api/v1/auth/login',
        data=json.dumps(dict(username=username,password=password,code='')).encode(),
        headers={'Content-Type':'application/json'})
    with urllib.request.urlopen(request,context=ssl._create_unverified_context()) as response:
        assert json.load(response)['csrf']

def confirm_access(output):
    token = re.search(r'#runtime-confirm=([a-f0-9]{64})',output).group(1)
    for attempt in range(30):
        try:
            request=urllib.request.Request(f'{scheme}://127.0.0.1:{port}/api/v1/runtime/confirm',data=json.dumps({'token':token}).encode(),headers={'Content-Type':'application/json'})
            with urllib.request.urlopen(request,context=ssl._create_unverified_context(),timeout=2) as response:
                assert json.load(response)['confirmed']
            return
        except OSError:
            time.sleep(1)
    raise AssertionError('CLI reconnect confirmation failed')

with tempfile.TemporaryDirectory() as folder:
    sums=download(base+'/SHA256SUMS').decode()
    records=[line.split()[0] for line in sums.splitlines() if line.split()[-1]=='install.sh']
    assert len(records)==1
    url = base+'/install.sh' if '-' in tag else 'https://github.com/gzdzh-cn/dzh-OpsView/releases/latest/download/install.sh'
    for attempt in range(8):
        installer = download(url)
        if hashlib.sha256(installer).hexdigest()==records[0]:
            break
        time.sleep(5)
    else:
        raise AssertionError('public installer checksum does not match this release')
    script=Path(folder)/'install.sh';script.write_bytes(installer)
    output=run(['bash',script,'--version',tag,'--public-host','panel.example.com'])
    username=re.search(r'用户名：(ov_[a-f0-9]{12})',output).group(1)
    password=re.search(r'密码：([A-Za-z0-9_-]{32})',output).group(1)
    assert 'OpsView 安装完成' in output
    for step in range(1,9):
        assert f'[{step}/8 ' in output
    del output
    login()
    assert run(['opsview','version']).strip()==tag.removeprefix('v')
    info=run(['ov','info']);assert username in info and password not in info
    assert 'ssl off' in run(['ov'])
    output=run(['ov','port','18882','--source','127.0.0.1','--yes']);port=18882
    confirm_access(output)
    del output
    login()
    output=run(['ov','ssl','off','--yes']);scheme='http'
    confirm_access(output)
    del output
    login()
    run(['ov','update','--version',tag])
    assert json.loads(Path('/etc/opsview/panel.json').read_text())['port']==port
    assert json.loads(Path('/etc/opsview/panel.json').read_text())['protocol']=='http'
    login()
    for log in Path('/var/log/opsview').glob('*.log'):
        assert password not in log.read_text()
    assert password not in run(['journalctl','-u','opsview-panel','-u','opsview-executor','--no-pager'])
    print('PASS: public release installer checksum, all eight stages, random login, ov, HTTP and changed-port network update')
