import { runDesktopBridge, processIdentity } from '../../mac/codex-desktop-bridge.mjs';
const input = JSON.parse(process.argv[2]);
await runDesktopBridge({ ...input, desktop: processIdentity(process.ppid) });
