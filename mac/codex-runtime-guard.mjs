// lockf owns this process. Pipe EOF (including owner SIGKILL) reaps its Codex
// before releasing the kernel lock. This helper is outside the tool ancestry.
import fs from 'node:fs';
import readline from 'node:readline';
import { processIdentity, sameProcess, readPrivateJSON } from './codex-desktop-bridge.mjs';
let registration;
const input = readline.createInterface({ input: process.stdin });
input.on('line', line => {
  if (registration) return;
  const data = JSON.parse(line), child = processIdentity(data.codex?.pid);
  if (!sameProcess(data.codex, child) || child.ppid !== data.owner?.pid || data.owner.uid !== process.getuid()) process.exit(1);
  registration = data;
});
input.on('close', async () => {
  const data = registration;
  if (data) {
    if (sameProcess(data.codex)) {
      try { process.kill(data.codex.pid, 'SIGTERM'); } catch (error) { if (error.code !== 'ESRCH') throw error; }
      const deadline = Date.now() + 5000;
      while (sameProcess(data.codex) && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 50));
      if (sameProcess(data.codex)) {
        process.kill(data.codex.pid, 'SIGKILL');
        while (sameProcess(data.codex)) await new Promise(resolve => setTimeout(resolve, 50));
      }
    }
    try {
      const current = readPrivateJSON(data.stateFile);
      if (current.generation === data.generation) {
        fs.unlinkSync(data.stateFile);
        const socket = fs.lstatSync(data.proxySocket);
        if (socket.isSocket() && socket.uid === process.getuid()) fs.unlinkSync(data.proxySocket);
      }
    } catch (error) { if (error.code !== 'ENOENT') process.stderr.write(`runtime cleanup: ${error.message}\n`); }
  }
  process.exit(0);
});
process.stdout.write('locked\n');
