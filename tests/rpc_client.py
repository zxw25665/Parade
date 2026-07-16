#!/usr/bin/env python3
"""
DEPRECATED: This client previously connected via Unix Domain Socket.
After the stdio refactor, the daemon uses stdin/stdout for IPC.
Use a pipe-based approach instead.

Example:
  echo '{"jsonrpc":"2.0","id":1,"method":"GetPubKey","params":null}' | parade daemon
"""

import json, subprocess, sys

method = sys.argv[1]
params = sys.argv[2] if len(sys.argv) > 2 else 'null'

req = json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': method, 'params': json.loads(params)})

proc = subprocess.Popen(
    ['parade', 'daemon'],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
)

stdout, stderr = proc.communicate(input=req + '\n', timeout=10)

for line in stdout.split('\n'):
    line = line.strip()
    if not line:
        continue
    try:
        obj = json.loads(line)
        if obj.get('id') == 1:
            print(json.dumps(obj))
            sys.exit(0)
    except json.JSONDecodeError:
        pass

print(json.dumps({'error': {'message': 'no response'}}))
sys.exit(1)
