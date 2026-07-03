#!/usr/bin/env python3
import json, socket, select, sys, time

uds = sys.argv[1]
method = sys.argv[2]
params = sys.argv[3] if len(sys.argv) > 3 else 'null'

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(5)
s.connect(uds)
req = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': json.loads(params)}) + '\n'
s.sendall(req.encode())

buf = b''
deadline = time.time() + 5
while time.time() < deadline:
    r, _, _ = select.select([s], [], [], 0.5)
    if r:
        chunk = s.recv(65536)
        if not chunk:
            break
        buf += chunk
    lines = buf.split(b'\n')
    for line in lines[:-1]:
        if not line.strip():
            continue
        try:
            obj = json.loads(line)
            if obj.get('id') == 1:
                print(json.dumps(obj))
                s.close()
                sys.exit(0)
        except json.JSONDecodeError:
            pass
    buf = lines[-1]
s.close()
print(json.dumps({'error': {'message': 'timeout'}}))
sys.exit(1)
