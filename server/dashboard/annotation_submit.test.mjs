import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
const app = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const model = await readFile(new URL('./annotation_model.js', import.meta.url), 'utf8');
const chatModel = await readFile(new URL('./chat_model.js', import.meta.url), 'utf8');
function setup() {
  const input = { value: '', style: {} };
  const requests = [];
  const context = {
    TextEncoder,
    console,
    Map,
    Set,
    Date,
    Math,
    JSON,
    state: { chat: null },
    $: (selector) => (selector === '#chat-input' ? input : null),
    sessionDeviceUnavailable: () => false,
    chatMutationsBlocked: () => false,
    chatSkillTokenNames: () => [],
    normalizeChatDraft: (value) => String(value || ''),
    renderChatAttachments() {},
    resizeChatInput() {},
    renderChat() {},
    updateChatComposerState() {},
    renderChatFollowups() {},
    closeChatSkillMenu() {},
    syncSessionRuntimeIndicators() {},
    persistAnnotationDraft() {},
    toast() {},
    chatTurnOptions: () => ({ approvalMode: 'on-request' }),
    loadServerChatQueue: async () => {},
    api: async (_mac, path, options) => {
      requests.push(JSON.parse(options.body));
      throw new Error('response lost');
    },
  };
  vm.createContext(context);
  vm.runInContext(model, context);
  vm.runInContext(chatModel, context);
  for (const name of [
    'submitChatInput',
    'enqueueServerChatMessage',
    'saveServerChatQueueItem',
    'restoreChatComposerItem',
    'mergeChatComposerText',
    'mergeChatAttachments',
  ]) {
    const source = app.match(new RegExp(`(?:async )?function ${name}\\([^]*?\\n}`))?.[0];
    assert.ok(source, name);
    vm.runInContext(source, context);
  }
  const chat = {
    macId: 'm1',
    assistant: 'codex',
    sessionId: 'session-a',
    controlReady: true,
    accessMode: 'read_write',
    annotationVersion: 'codex-response-annotations-v1',
    annotations: [{ id: 'one', text: '中文😀', annotation: '简洁一些' }],
    attachments: [],
    model: context.FleetChatModel.createChatState(),
  };
  context.state.chat = chat;
  return { context, chat, input, requests };
}
test('A04/A06 annotation-only send restores complete draft after a lost acknowledgment and reuses client identity', async () => {
  const { context, chat, requests } = setup();
  await context.submitChatInput();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].text, '');
  assert.equal(requests[0].annotations.items[0].annotation, '简洁一些');
  assert.equal(chat.annotations[0].text, '中文😀');
  assert.equal(chat.annotations[0].annotation, '简洁一些');
  await context.submitChatInput();
  assert.equal(requests.length, 2);
  assert.equal(requests[1].clientMessageId, requests[0].clientMessageId);
  assert.deepEqual(requests[1].annotations, requests[0].annotations);
});
test('A06 editing a restored request creates a new explicit input and preserves other attachments', async () => {
  const { context, chat, input, requests } = setup();
  await context.submitChatInput();
  input.value = '新的要求';
  await context.submitChatInput();
  assert.notEqual(requests[1].clientMessageId, requests[0].clientMessageId);
  assert.equal(input.value, '新的要求');
  assert.equal(chat.annotations.length, 1);
});
