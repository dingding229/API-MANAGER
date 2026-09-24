const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const start = source.indexOf('function selected(');
const end = source.indexOf('function apiFormData(', start);
assert.ok(start >= 0 && end > start);
const context = {
  esc(value) { return String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char])); },
};
vm.createContext(context);
vm.runInContext(source.slice(start, end), context);

test('API form separates primary fields from collapsible advanced settings', () => {
  const html = context.apiForm();
  assert.match(html, /class="form-stack api-form"/);
  assert.match(html, /class="form-section form-section-primary"/);
  assert.equal((html.match(/class="form-section form-section-collapsible"/g) || []).length, 4);
  assert.match(html, /上游转发/);
  assert.match(html, /可靠性策略/);
  assert.match(html, /响应与校验/);
  assert.match(html, /限流与配额/);
  assert.match(html, /aria-describedby="path-hint"/);
  assert.match(html, /保存后仍需单独发布接口/);
});

test('API form opens relevant advanced sections while editing configured values', () => {
  const html = context.apiForm({id:'api_1', name:'demo', method:'GET', path:'/api/demo', upstream_url:'https://example.com', rate_limit_per_minute:60});
  assert.match(html, /class="form-section form-section-collapsible" open/);
  assert.match(html, /name="rate_limit_per_minute" value="60"/);
  assert.match(html, /name="upstream_url"/);
});
