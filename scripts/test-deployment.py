"""Real systemd deployment regression. Never print subprocess output containing credentials."""
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import pwd
import re
import secrets
import shutil
import socket
import ssl
import subprocess
import tempfile
import sys
import traceback
import time
import urllib.error
import urllib.request

def report_failure(kind, error, tb):
    # A public annotation gives the failing assertion location without exposing captured output.
    frames = traceback.extract_tb(tb)
    location = ' -> '.join(f'{Path(frame.filename).name}:{frame.lineno}' for frame in frames if Path(frame.filename).name == 'test-deployment.py') or 'unknown'
    message = f'{kind.__name__} at {location}'
    if isinstance(error, AssertionError) and str(error).startswith('command exit mismatch:'):
        message += ' ' + str(error)
    print('::error::' + message, flush=True)
    sys.__excepthook__(kind, error, tb)
sys.excepthook = report_failure

if os.environ.get('GITHUB_ACTIONS') != 'true' or os.geteuid() != 0:
    raise SystemExit('仅在隔离 CI root 主机运行此测试。')
assert not Path('/opt/opsview').exists() and not Path('/var/lib/opsview').exists(), 'CI host must be clean'

ROOT = Path(__file__).resolve().parents[1]
RELEASE = ROOT / 'dist/opsview-linux-amd64'
CONFIG = Path('/etc/opsview/panel.json')
PANEL = Path('/var/lib/opsview/panel')
PORT = 18790
CTX = ssl._create_unverified_context()

def run(args, *, ok=True, data=None, user=None):
    if user:
        args = ['runuser', '-u', user, '--'] + list(map(str, args))
    result = subprocess.run(list(map(str, args)), input=data, capture_output=True, text=True, timeout=240)
    if (result.returncode == 0) != ok:
        # Do not include stdout/stderr: either may contain generated credentials.
        phase = re.findall(r'\[[1-8]/8 [^\]\r\n]+\]', result.stdout + result.stderr)
        phase = phase[-1] if phase else 'no deployment phase'
        raise AssertionError(f'command exit mismatch: {Path(args[0]).name}, status {result.returncode}, phase {phase}')
    return result.stdout + result.stderr

def api(path, payload=None, opener=None, scheme='https'):
    req = urllib.request.Request(f'{scheme}://127.0.0.1:{PORT}/api/v1/{path}',
        data=json.dumps(payload).encode() if payload is not None else None,
        headers={'Content-Type': 'application/json'})
    if opener:
        response = opener.open(req)
    else:
        response = urllib.request.urlopen(req, context=CTX)
    with response:
        return json.load(response)

def login(username, password, scheme='https'):
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPSHandler(context=CTX), urllib.request.HTTPCookieProcessor(jar))
    assert api('auth/login', dict(username=username, password=password, code=''), opener, scheme).get('csrf')
    return opener

def authenticated(opener, scheme='https'):
    return api('auth', opener=opener, scheme=scheme)['authenticated']

def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def confirm_access(output, scheme='https'):
    token = re.search(r'#runtime-confirm=([a-f0-9]{64})',output).group(1)
    # A browser reconnects through the new listener. CI uses loopback to avoid external DNS.
    for _ in range(30):
        try:
            request = urllib.request.Request(f'{scheme}://127.0.0.1:{PORT}/api/v1/runtime/confirm',
                data=json.dumps({'token':token}).encode(),headers={'Content-Type':'application/json'})
            with urllib.request.urlopen(request,context=CTX,timeout=2) as response:
                assert json.load(response)['confirmed']
            return
        except (OSError,urllib.error.HTTPError): time.sleep(1)
    raise AssertionError('new listener could not confirm access change')

def ssl_off_menu(answer):
    import pty
    import select
    master, slave = pty.openpty()
    proc = subprocess.Popen(['ov'], stdin=slave, stdout=slave, stderr=slave)
    os.close(slave)
    terminal = b''

    def read_until(prompt):
        nonlocal terminal
        deadline = time.monotonic() + 15
        while prompt not in terminal.decode(errors='ignore'):
            assert time.monotonic() < deadline, 'SSL menu prompt timed out'
            ready, _, _ = select.select([master], [], [], 1)
            if ready:
                terminal += os.read(master, 4096)

    try:
        read_until('请输入编号：')
        assert '15. 关闭 SSL 证书（切换 HTTP）' in terminal.decode()
        terminal = b''
        os.write(master, b'15\n')
        read_until('[y/N]：')
        assert '关闭 SSL 后将使用 HTTP' in terminal.decode()
        terminal = b''
        os.write(master, (answer + '\n').encode())
        read_until('请输入编号：')
        assert '用法：ov ssl' not in terminal.decode()
        os.write(master, b'0\n')
        assert proc.wait(timeout=15) == 0
        return terminal.decode()
    finally:
        if proc.poll() is None:
            proc.kill()
            proc.wait(timeout=5)
        os.close(master)

with tempfile.TemporaryDirectory() as stage:
    stage = Path(stage)
    # Reject an unrelated ov before mutating services or data.
    alien = Path('/usr/local/bin/ov')
    alien.write_text('#!/bin/sh\nexit 0\n')
    alien.chmod(0o755)
    output = run(['bash', RELEASE/'deploy/install.sh', '--public-host', 'panel.example.com'], ok=False)
    assert '命令' in output and not PANEL.exists()
    alien.unlink()

    output = run(['bash', RELEASE/'deploy/install.sh', '--public-host', 'panel.example.com'])
    assert 'OpsView 安装完成' in output
    assert 'https://panel.example.com:18790' in output
    assert 'https://<服务器' not in output
    username = re.search(r'用户名：(ov_[a-f0-9]{12})', output).group(1)
    password = re.search(r'密码：([A-Za-z0-9_-]{32})', output).group(1)
    assert output.index('[5/8') < output.index('[6/8') < output.index('[7/8') < output.index('[8/8')
    del output
    assert not (PANEL/'setup.token').exists()
    assert (PANEL/'opsview.db').stat().st_uid == pwd.getpwnam('opsview').pw_uid
    assert CONFIG.stat().st_uid == 0 and CONFIG.stat().st_mode & 0o777 == 0o644
    assert Path('/var/log/opsview').stat().st_mode & 0o777 == 0o700
    for log in Path('/var/log/opsview').glob('*.log'):
        assert log.stat().st_mode & 0o777 == 0o600
        assert password not in log.read_text() and username not in log.read_text()
    for path in PANEL.iterdir():
        if path.is_file():
            assert password.encode() not in path.read_bytes()
    assert password not in run(['journalctl','-u','opsview-panel','-u','opsview-executor','--no-pager'])
    assert run(['systemctl','show','-p','User','--value','opsview-panel']).strip() == 'opsview'
    assert Path('/run/opsview/executor.sock').is_socket()
    cert = digest(PANEL/'panel.pem')
    old = login(username, password)
    assert authenticated(old)
    assert 'ov <命令>' in run(['ov'])  # PIPE is noninteractive; never waits for a menu selection.
    run(['ov','status'])
    info = run(['ov','info'])
    assert username in info and password not in info and '密码不保存' in info
    run(['ov','info','--public-host','ops.example.com'])
    assert json.loads(CONFIG.read_text())['publicHost'] == 'ops.example.com'
    run(['ov','init'], ok=False)
    run(['ov','status'], user='nobody', ok=False)
    run(['bash', RELEASE/'deploy/install.sh'], ok=False)

    # Username/password operations invalidate sessions; shell syntax is rejected as data.
    run(['ov','username','invalid;touch /tmp/opsview-injected'], ok=False)
    assert not Path('/tmp/opsview-injected').exists()
    username = 'ci-opsview'
    run(['ov','username',username])
    assert not authenticated(old)
    old = login(username, password)
    password = secrets.token_urlsafe(24)
    run(['ov','password','--stdin'], data=password+'\n')
    assert not authenticated(old)
    old = login(username,password)
    output = run(['ov','password','--generate'])
    password = re.search(r'新密码：([A-Za-z0-9_-]{32})',output).group(1)
    del output
    assert not authenticated(old)
    old = login(username,password)
    run(['ov','reset-2fa'],ok=False)  # no confirmation in a PIPE
    run(['ov','reset-2fa','--yes'])
    assert not authenticated(old)

    # Hidden terminal input is exercised in a real pseudo-terminal (never emitted).
    import pty
    import select
    master, slave = pty.openpty()
    proc = subprocess.Popen(['ov','password'],stdin=slave,stdout=slave,stderr=slave)
    os.close(slave)
    password = secrets.token_urlsafe(24)
    terminal = b''
    for prompt in ['新密码（','再次输入新密码：']:
        deadline = time.monotonic()+15
        while prompt not in terminal.decode(errors='ignore'):
            assert time.monotonic()<deadline, 'password prompt timed out'
            ready,_,_ = select.select([master],[],[],1)
            if ready:
                terminal += os.read(master,4096)
        # ReadPassword turns echo off before blocking; let the ioctl complete before writing.
        time.sleep(0.1)
        os.write(master,(password+'\n').encode())
    assert proc.wait(timeout=15)==0
    while select.select([master],[],[],0.1)[0]:
        try: terminal += os.read(master,4096)
        except OSError: break
    os.close(master)
    assert password.encode() not in terminal
    del terminal
    login(username,password)
    print('PASS: random login, one-time credentials, root-only ov, hidden password, session invalidation')

    # Drop-ins survive successful updates, conflicting ExecStart overrides are refused.
    dropin = Path('/etc/systemd/system/opsview-panel.service.d')
    dropin.mkdir(exist_ok=True)
    (dropin/'limits.conf').write_text('[Service]\nLimitNOFILE=65536\n')
    run(['systemctl','daemon-reload'])
    output=run(['ov','port','18880','--yes'])
    PORT = 18880
    assert json.loads(CONFIG.read_text())['port'] == PORT
    run(['bash',RELEASE/'deploy/update.sh'],ok=False) # pending access transactions block update
    confirm_access(output)
    del output
    login(username,password)
    before = CONFIG.read_bytes()
    assert '已取消' in ssl_off_menu('n')
    assert CONFIG.read_bytes() == before
    output=ssl_off_menu('y')
    confirm_access(output,'http')
    assert json.loads(CONFIG.read_text())['protocol']=='http'
    assert digest(PANEL/'panel.pem') == cert
    assert authenticated(login(username,password,'http'),'http')
    print('PASS: menu 15 SSL off, cancellation, HTTP reconnect and authenticated login, certificate preserved')
    output=run(['ov','ssl','on','--yes'])
    confirm_access(output)
    del output
    login(username,password)
    (dropin/'bad.conf').write_text('[Service]\nExecStart=\nExecStart=/bin/false\n')
    run(['systemctl','daemon-reload'])
    run(['ov','port','18881','--yes'],ok=False)
    run(['bash',RELEASE/'deploy/update.sh'],ok=False)
    (dropin/'bad.conf').unlink()
    run(['systemctl','daemon-reload'])
    occupied = socket.socket();occupied.bind(('0.0.0.0',18881));occupied.listen()
    run(['ov','port','18881','--yes'],ok=False)
    occupied.close()
    assert json.loads(CONFIG.read_text())['port'] == PORT

    # Root CLI firewall behavior is additionally tested with deterministic command fixtures.
    # Real runner UFW stays disabled unless it was already active: no SSH/firewall assumptions.
    run(['ov','backup'])
    run(['ov','diagnose'])
    run(['ov','stop'])
    run(['systemctl','is-active','--quiet','opsview-executor.service'])
    run(['ov','start'])
    time.sleep(2)
    run(['bash',RELEASE/'deploy/update.sh'])
    assert (dropin/'limits.conf').exists()
    assert json.loads(CONFIG.read_text())['port'] == PORT
    login(username,password)
    config_before = CONFIG.read_bytes()
    print('PASS: configurable port, conflict rejection, preserved drop-ins and update on changed port')

    # A valid panel with a broken executor reaches service health checks, then rolls back.
    broken = stage/'broken'
    shutil.copytree(RELEASE,broken)
    shutil.copyfile('/bin/false',broken/'bin/opsview-executor')
    run(['bash',broken/'deploy/update.sh'],ok=False)
    assert CONFIG.read_bytes() == config_before
    assert digest('/opt/opsview/bin/opsview-executor') == digest(RELEASE/'bin/opsview-executor')
    assert digest('/opt/opsview/bin/opsview') == digest(RELEASE/'bin/opsview')
    assert digest(PANEL/'panel.pem') == cert
    assert Path('/usr/local/bin/ov').resolve() == Path('/opt/opsview/bin/opsview')
    assert digest('/opt/opsview/bin/opsview-install.sh') == digest(RELEASE/'bin/opsview-install.sh')
    assert (dropin/'limits.conf').exists()
    deadline=time.monotonic()+35
    while True:
        try: login(username,password);break
        except (OSError,urllib.error.URLError):
            assert time.monotonic()<deadline,'rollback health timed out'
            time.sleep(1)
    run(['ov','logs'])
    run(['ov','restart'])
    print('PASS: failed update restored services, credentials, certificate, port, ov and controlled updater')

    # Upgrade the publicly released v0.1.0 package, both initialized and uninitialized.
    import tarfile
    base = 'https://github.com/gzdzh-cn/dzh-OpsView/releases/download/v0.1.0/'
    asset = 'opsview-linux-amd64.tar.gz'
    with urllib.request.urlopen(base+'SHA256SUMS',timeout=60) as response:
        sums=response.read().decode()
    expected=[line.split()[0] for line in sums.splitlines() if line.split()[-1] == asset]
    assert len(expected)==1
    package=stage/asset
    with urllib.request.urlopen(base+asset,timeout=120) as response, package.open('wb') as file:
        shutil.copyfileobj(response,file)
    assert digest(package)==expected[0]
    legacy=stage/'legacy';legacy.mkdir()
    with tarfile.open(package) as tar:
        tar.extractall(legacy,filter='data')
    legacy=legacy/'opsview-linux-amd64'
    for initialized in (True,False):
        run(['systemctl','stop','opsview-panel.service','opsview-executor.service'])
        run(['systemctl','disable','opsview-panel.service','opsview-executor.service'])
        for unit in ('opsview-panel.service','opsview-executor.service'):
            Path('/etc/systemd/system',unit).unlink(missing_ok=True)
        shutil.rmtree('/etc/systemd/system/opsview-panel.service.d',ignore_errors=True)
        for path in ('/opt/opsview','/var/lib/opsview','/etc/opsview'):
            shutil.rmtree(path)
        for name in ('ov','opsview'):
            Path('/usr/local/bin',name).unlink(missing_ok=True)
        run(['systemctl','daemon-reload'])
        run(['systemctl','reset-failed'])
        run(['bash',legacy/'deploy/install.sh'])
        PORT=18790
        if initialized:
            username='legacy-admin'
            password=secrets.token_urlsafe(24)
            token=(PANEL/'setup.token').read_text().strip()
            assert api('auth/setup',dict(token=token,username=username,password=password))['ok']
            del token
        run(['bash',RELEASE/'deploy/update.sh','--public-host','migrated.example.com'])
        assert json.loads(CONFIG.read_text())['port']==18790
        assert json.loads(CONFIG.read_text())['publicHost']=='migrated.example.com'
        if initialized:
            login(username,password)
            run(['ov','init'],ok=False)
        else:
            assert api('auth')['setupRequired'] is True
            output=run(['ov','init'])
            username=re.search(r'用户名：(ov_[a-f0-9]{12})',output).group(1)
            password=re.search(r'密码：([A-Za-z0-9_-]{32})',output).group(1)
            del output
            login(username,password)
            assert not (PANEL/'setup.token').exists()
    print('PASS: actual v0.1.0 migration preserved administrator and uninitialized setup with explicit ov init')
