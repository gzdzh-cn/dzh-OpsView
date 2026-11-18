#!/usr/bin/env python3
"""Private probe endpoint for disposable firewall acceptance networks only."""
import http.server
import ipaddress
import socket
import subprocess
import threading
import urllib.parse

class Probe(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
        ok = False
        try:
            address = str(ipaddress.ip_address(q['target'][0]))
            if self.path.startswith('/ping?'):
                ok = subprocess.run(['ping', '-n', '-c', '1', '-W', '1', address], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=3).returncode == 0
            else:
                port = int(q['port'][0])
                if not 1 <= port <= 65535:
                    raise ValueError('port')
                with socket.create_connection((address, port), timeout=1.5) as conn:
                    conn.sendall(b'GET / HTTP/1.0\r\nHost: test\r\n\r\n')
                    ok = b'200' in conn.recv(128)
        except (ValueError, KeyError, OSError, subprocess.TimeoutExpired):
            pass
        self.send_response(200 if ok else 503)
        self.end_headers()
        self.wfile.write(b'ok' if ok else b'blocked')
    def log_message(self, *_):
        pass

class Payload(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'forwarded')
    def log_message(self, *_):
        pass

threading.Thread(target=http.server.ThreadingHTTPServer(('0.0.0.0',18901),Payload).serve_forever,daemon=True).start()
http.server.ThreadingHTTPServer(('0.0.0.0',18900),Probe).serve_forever()
