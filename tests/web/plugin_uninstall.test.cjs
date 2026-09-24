const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const start = source.indexOf('async function uninstallPlugin(');
const end = source.indexOf("$('#login-form')", start);
assert.ok(start >= 0 && end > start);

function setup(confirmed = true, failAt = '') {
  const calls = [], notices = [];
  let renders = 0;
  const context = {
    confirm: message => { calls.push(['confirm', message]); return confirmed; },
    api: async (url, options) => {
      calls.push([options.method, url, options.body || '']);
      if (options.method === failAt) throw new Error('request failed');
    },
    notice: (message, ok) => notices.push({message, ok}),
    renderPlugins: () => { renders++; },
    encodeURIComponent,
    JSON,
  };
  vm.createContext(context);
  vm.runInContext(source.slice(start, end), context);
  return {context, calls, notices, get renders() {return renders;}};
}

test('enabled version requires confirmation and is disabled before deletion', async () => {
  const app = setup();
  await app.context.uninstallPlugin('plugin_1', 'inventory-demo', '1.0.0', true);
  assert.deepEqual(app.calls.map(call => call[0]), ['confirm', 'PUT', 'DELETE']);
  assert.match(app.calls[0][1], /关联的 API 路由不会自动删除/);
  assert.match(app.calls[0][1], /先停用/);
  assert.equal(JSON.parse(app.calls[1][2]).enabled, false);
  assert.equal(app.calls[2][1], '/admin/v1/plugins/plugin_1');
  assert.equal(app.renders, 1);
  assert.match(app.notices[0].message, /已卸载/);
});

test('disabled version is deleted directly after confirmation', async () => {
  const app = setup();
  await app.context.uninstallPlugin('plugin_2', 'inventory-demo', '0.9.0', false);
  assert.deepEqual(app.calls.map(call => call[0]), ['confirm', 'DELETE']);
});

test('cancelling uninstall has no side effects', async () => {
  const app = setup(false);
  await app.context.uninstallPlugin('plugin_1', 'inventory-demo', '1.0.0', true);
  assert.deepEqual(app.calls.map(call => call[0]), ['confirm']);
  assert.equal(app.renders, 0);
});

test('failed deletion after disable reports partial outcome and refreshes state', async () => {
  const app = setup(true, 'DELETE');
  await app.context.uninstallPlugin('plugin_1', 'inventory-demo', '1.0.0', true);
  assert.deepEqual(app.calls.map(call => call[0]), ['confirm', 'PUT', 'DELETE']);
  assert.match(app.notices[0].message, /可能已停用/);
  assert.equal(app.renders, 1);
});
