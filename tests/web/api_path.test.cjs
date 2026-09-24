const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

const script = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const from = script.indexOf('function routePathError(');
const to = script.indexOf('async function createAPI(', from);
assert.ok(from >= 0 && to > from);
const context = {};
vm.createContext(context);
vm.runInContext(script.slice(from, to), context);

test('bare plugin name is not a route path and gets actionable Chinese feedback', () => {
  assert.match(context.routePathError('game-discount'), /以 \/ 开头/);
  assert.match(context.routePathError('game-discount'), /插件名称/);
  assert.doesNotMatch(context.routePathError('game-discount'), /game-discount-wasm/);
});

test('URLs and double slashes are rejected but valid plugin route is accepted', () => {
  assert.match(context.routePathError('https://example.com/api/status'), /以 \/ 开头/);
  assert.match(context.routePathError('/api//status'), /连续的 \/\//);
  assert.equal(context.routePathError('/api/game-discount-wasm/v1/status'), '');
});
