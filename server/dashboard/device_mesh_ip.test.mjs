import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const app = await readFile(new URL('./app.js', import.meta.url), 'utf8');

function extractFunction(name) {
  const functionStart = app.indexOf(`function ${name}(`);
  assert.ok(functionStart >= 0, `missing ${name}`);
  const start = app.slice(Math.max(0, functionStart - 6), functionStart) === 'async '
    ? functionStart - 6
    : functionStart;
  let depth = 0;
  let started = false;
  for (let index = start; index < app.length; index++) {
    if (app[index] === '{') {
      depth++;
      started = true;
    } else if (app[index] === '}') {
      depth--;
      if (started && depth === 0) return app.slice(start, index + 1);
    }
  }
  assert.fail(`unterminated ${name}`);
}

function deviceHarness() {
  const elements = new Map(['hm-title', 'hm-name', 'hm-state', 'hm-ip', 'hm-http', 'hm-https', 'hm-proxy-on']
    .map((id) => [id, { id, textContent: '', value: '', checked: false, className: '' }]));
  const state = {
    deviceStatuses: {}, deviceIPs: {}, nodes: {}, sessionMacId: 'all', fileMacId: null, macId: null,
    mode: 'sessions', sessionResults: [], fileEntries: [], chatCache: new Map(), hostModalMac: null,
  };
  const calls = { ping: [] };
  const source = `${extractFunction('refreshNodes')}\n${extractFunction('openHostModal')}`;
  const create = new Function('state', 'elements', 'calls', `
    const BASE = '';
    let MACS = [];
    let macNames = {};
    const $ = (selector) => elements.get(selector.slice(1));
    const fetch = async () => ({ ok: true, json: async () => ({ devices: [
      { id: 'm2', name: 'Macbook Air', ip: '100.96.0.3', status: 'active', online: true },
    ] }) });
    const markGatewayUnreachable = () => {};
    const markGatewayReachable = () => {};
    const deviceConnectionState = (device) => ({ online: device.online === true });
    const renderHosts = () => {};
    const updateDeviceScopeUI = () => {};
    const loadSessions = () => {};
    const loadFiles = () => {};
    const refreshHostCounts = () => {};
    const refreshAssistantCapabilities = () => {};
    const updateChatComposerState = () => {};
    const sessionDeviceUnavailable = () => false;
    const submitChatInput = () => {};
    const macName = (id) => macNames[id] || id;
    const FleetDeviceAppearance = { get: () => ({ icon: 'notebook', color: 'rose' }) };
    const renderHostAppearanceChoices = () => {};
    const setPingChip = () => {};
    const hostProxyForm = () => ({});
    const closeMenus = () => {};
    const openOverlay = () => {};
    const api = async () => ({ meshIP: '127.0.0.1', proxy: { enabled: false } });
    const pingHost = (id) => calls.ping.push(id);
    const DEFAULT_PROXY = 'http://127.0.0.1:7897';
    ${source}
    return { refreshNodes, openHostModal };
  `);
  return { ...create(state, elements, calls), state, elements, calls };
}

test('device details use the Headscale registry address instead of agent loopback metadata', async () => {
  const current = deviceHarness();
  await current.refreshNodes();
  await current.openHostModal('m2');
  assert.equal(current.state.deviceIPs.m2, '100.96.0.3');
  assert.equal(current.elements.get('hm-ip').textContent, '100.96.0.3');
  assert.deepEqual(current.calls.ping, ['m2']);
});
