// External Codex boundary substitute: actual HTTP/WebSocket transport, no writer logic.
import http from 'node:http';
import fs from 'node:fs';
import { createHash } from 'node:crypto';
import readline from 'node:readline';
const args = process.argv.slice(2);
if (process.env.ECHO_SPAWN_LOG) fs.appendFileSync(process.env.ECHO_SPAWN_LOG, JSON.stringify({ pid: process.pid, args }) + '\n');
const listen = args.indexOf('--listen');
if (listen < 0) {
  if (!args.includes('app-server')) { console.log(JSON.stringify({ args })); process.exit(0); }
  readline.createInterface({ input: process.stdin }).on('line', line => process.stdout.write(line + '\n'));
} else {
  const names = new Map();
  const server = http.createServer((req, res) => { res.writeHead(req.url === '/readyz' ? 200 : 404); res.end('ok'); });
  server.on('upgrade', (req, socket) => {
    const accept = createHash('sha1').update(req.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
    socket.write('HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ' + accept + '\r\n\r\n');
    const send = value => {
      const data = Buffer.from(JSON.stringify(value));
      const header = Buffer.alloc(data.length < 126 ? 2 : data.length <= 65535 ? 4 : 10); header[0] = 0x81;
      if (header.length === 2) header[1] = data.length;
      else if (header.length === 4) { header[1] = 126; header.writeUInt16BE(data.length, 2); }
      else { header[1] = 127; header.writeBigUInt64BE(BigInt(data.length), 2); }
      socket.write(Buffer.concat([header, data]));
    };
    let pending = Buffer.alloc(0);
    socket.on('error', () => {});
    socket.on('data', chunk => {
      pending = Buffer.concat([pending, chunk]);
      while (pending.length >= 2) {
        const opcode = pending[0] & 15, masked = !!(pending[1] & 128); let length = pending[1] & 127, offset = 2;
        if (length === 126) { if (pending.length < 4) return; length = pending.readUInt16BE(2); offset = 4; }
        else if (length === 127) { if (pending.length < 10) return; length = Number(pending.readBigUInt64BE(2)); offset = 10; }
        const maskOffset = offset; if (masked) offset += 4;
        if (pending.length < offset + length) return;
        const data = Buffer.from(pending.subarray(offset, offset + length));
        if (masked) for (let i = 0; i < data.length; i++) data[i] ^= pending[maskOffset + i % 4];
        pending = pending.subarray(offset + length);
        if (opcode === 8) { socket.end(Buffer.from([0x88, 0])); continue; }
        if (opcode !== 1) continue;
        const msg = JSON.parse(data);
        if (msg.method === 'test/setName') names.set(msg.params.id, msg.params.name);
        if (msg.method === 'test/requestApproval') send({ id: 'approval-9', method: 'item/commandExecution/requestApproval', params: { command: ['pwd'] } });
        if (msg.id != null) send({ id: msg.id, result: { pid: process.pid, received: msg, name: names.get(msg.params?.id) } });
      }
    });
  });
  const target = new URL(args[listen + 1]);
  server.listen(Number(target.port), target.hostname);
  server.on('error', error => { console.error(error.message); process.exit(1); });
}
