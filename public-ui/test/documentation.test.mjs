import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { catalogOrigin, filterCatalog, projectCatalog, requestExample, snippet } from '../lib/catalog.ts';

const source = { id: 'docs-one', title: '库存查询', summary: '已公开的说明', category: '商品', method: 'POST', path: '/api/products/{id}', authentication: 'api_key', parameters: [{ name: 'id', type: 'string', location: 'path', required: true }, { name: 'page', type: 'integer', location: 'query', required: false }, { name: 'X-Request-ID', type: 'string', location: 'header', required: false }], body: [{ name: 'quantity', type: 'integer', location: 'body', required: true }, { name: 'enabled', type: 'boolean', location: 'body', required: false }, { name: 'metadata', type: 'object', location: 'body', required: false }] };
const origin = 'https://api.example.test';
const api = projectCatalog({ version: 1, base_url: origin, apis: [source] }).apis[0];
const filters = { query: '', method: 'all', authentication: 'all', category: 'all' };

test('search finds parameter names without duplicating endpoints', () => {
  assert.equal(filterCatalog([api], { ...filters, query: '  METADATA  ' }).length, 1);
  assert.equal(filterCatalog([api], { ...filters, query: 'not-found' }).length, 0);
  assert.equal(filterCatalog([api], { ...filters, method: 'GET' }).length, 0);
});

test('configured dedicated domain wins and fallback uses current website origin', () => {
  const catalog = projectCatalog({ version: 1, base_url: 'https://website.example.test', site: { api_domain: origin, website_url: 'https://website.example.test' }, apis: [source] });
  assert.equal(catalogOrigin(catalog, 'http://localhost:18085'), origin);
  assert.equal(catalogOrigin({ version: 1, base_url: '', apis: [] }, 'http://localhost:18085'), 'http://localhost:18085');
});

test('request preview and snippets share typed parameters, KEY headers and target origin', () => {
  const overrides = { 'path:id': '42', 'query:page': '3', 'body:quantity': '7', 'body:enabled': 'false', 'body:metadata': '{"tag":"公开示例"}' };
  const request = requestExample(api, origin, 'POST', overrides);
  assert.equal(request.url, origin + '/api/products/42?page=3');
  assert.deepEqual(JSON.parse(request.body), { quantity: 7, enabled: false, metadata: { tag: '公开示例' } });
  assert.ok(request.headers.some(([name]) => name === 'X-API-Key'));
  for (const language of ['curl', 'javascript', 'python', 'go']) {
    const code = snippet(api, origin, 'POST', language, overrides);
    assert.ok(code.includes(origin)); assert.ok(code.includes('/api/products/42?page=3'));
    assert.ok(code.includes('X-API-Key')); assert.doesNotMatch(code, /YOUR_API_DOMAIN|YOUR_VALUE|sk_test_|nexus/);
  }
});

test('bad types, control characters, dot path segments and unlisted methods cannot produce callable examples', () => {
  for (const [key, value] of [['body:quantity', ''], ['body:quantity', '1.2'], ['query:page', 'no-number'], ['body:enabled', 'yes'], ['body:metadata', '[]'], ['header:X-Request-ID', 'x\r\nHost:evil'], ['path:id', '..']]) {
    assert.throws(() => snippet(api, origin, 'POST', 'curl', { [key]: value }));
  }
  assert.throws(() => snippet(api, 'https://user:password@example.test', 'POST', 'go'));
  assert.throws(() => snippet(api, origin, 'DELETE', 'javascript'));
});

test('method-specific auth remains accurate across all languages', () => {
  const combined = projectCatalog({ version: 1, apis: [{ ...source, method: 'GET', authentication: 'none', body: [] }, source] }).apis[0];
  for (const language of ['curl', 'javascript', 'python', 'go']) {
    assert.doesNotMatch(snippet(combined, origin, 'GET', language), /X-API-Key/);
    assert.match(snippet(combined, origin, 'POST', language), /X-API-Key/);
  }
  assert.throws(() => snippet(api, origin, 'GET', 'javascript'));
});

test('generated Python and Go snippets parse with quotes, Unicode and composite body values', () => {
  const directory = mkdtempSync(join(tmpdir(), 'public-docs-snippets-'));
  try {
    const overrides = { 'path:id': "quote'\"测试", 'body:metadata': '{"__proto__":{"text":"引号\\\""}}' };
    const python = snippet(api, origin, 'POST', 'python', overrides);
    execFileSync('python3', ['-c', 'import ast,sys;ast.parse(sys.stdin.read())'], { input: python });
    const path = join(directory, 'main.go'); writeFileSync(path, snippet(api, origin, 'POST', 'go', overrides));
    execFileSync('gofmt', ['-w', path]);
    const js = snippet(api, origin, 'POST', 'javascript', overrides);
    assert.doesNotThrow(() => new Function('return async function(){' + js + '}'));
  } finally { rmSync(directory, { recursive: true, force: true }); }
});

test('frontend uses only public data and no template secrets, response mocks or external CDN assets', () => {
  const page = readFileSync(new URL('../app/page.tsx', import.meta.url), 'utf8');
  const css = readFileSync(new URL('../app/global.css', import.meta.url), 'utf8');
  assert.match(page, /fetch\('\/catalog\.json'/);
  assert.match(page, /credentials: 'omit'/);
  assert.doesNotMatch(page, /\/admin\/v1|\/auth\/v1|sessionStorage|localStorage|dangerouslySetInnerHTML|executeMockRequest|sk_test_/);
  assert.doesNotMatch(css, /@import|fonts\.google|tailwindcss\.com/);
  assert.match(page, /当前公开目录未提供响应示例/);
  assert.match(page, /不会发送实际请求/);
});

test('generated cURL preserves shell safety and typed JSON for hostile but valid values', () => {
  const hostile = "x'$(printf EXPLOITED)'`printf EXPLOITED`;echo EXPLOITED";
  const code = snippet(api, origin, 'POST', 'curl', { 'header:X-Request-ID': hostile, 'body:metadata': JSON.stringify({ value: hostile }) });
  const args = execFileSync('/bin/sh', ['-c', `curl(){ printf '%s\\0' "$@"; }; ${code}`], { encoding: 'utf8' }).split('\0').filter(Boolean);
  assert.ok(args.includes('X-Request-ID: ' + hostile));
  assert.equal(JSON.parse(args.at(-1)).metadata.value, hostile);
});
