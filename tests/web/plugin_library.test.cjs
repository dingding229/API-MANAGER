const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const start = source.indexOf('function renderPluginLibrary(');
const end = source.indexOf('async function uploadPlugin(', start);
assert.ok(start >= 0 && end > start);
const context = {
  can: () => true,
  esc: value => String(value).replace(/[&<>"']/g, ch => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch])),
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);

test('library escapes package metadata and keeps install action for a local package', () => {
  const html = context.renderPluginLibrary([{name:'<img src=x>', version:'1.0.0', runtime:'wasm', checksum:'1234567890abcdef'}]);
  assert.doesNotMatch(html, /<img src=x>/);
  assert.match(html, /&lt;img src=x&gt;/);
  assert.match(html, /data-plugin-library-install/);
  assert.match(html, /SHA-256 1234567890abcdef/);
  assert.doesNotMatch(html, /签名|signer/i);
});

test('installed version has a publish action without signer metadata', () => {
  const html = context.pluginRow({id:'plugin_1', name:'sample', version:'1.0.0', checksum:'1234567890123456'});
  assert.match(html, /data-plugin-library-publish="plugin_1"/);
  assert.doesNotMatch(html, /签名|signer|未签名/i);
  const published = context.pluginRow({id:'plugin_1', name:'sample', version:'1.0.0', checksum:'1234567890123456'}, [{name:'sample', version:'1.0.0'}]);
  assert.doesNotMatch(published, /data-plugin-library-publish/);
  assert.match(published, /已入库/);
});
