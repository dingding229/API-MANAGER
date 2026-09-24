const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.js'), 'utf8');
const css = fs.readFileSync(path.join(__dirname, '../../internal/web/assets/app.css'), 'utf8');
const helpersStart = script.indexOf('const permissionLabels =');
const helpersEnd = script.indexOf('async function api(', helpersStart);
const rolesStart = script.indexOf('async function renderRoles()');
const rolesEnd = script.indexOf('async function renderAuditLogs(', rolesStart);
assert.ok(helpersStart >= 0 && helpersEnd > helpersStart && rolesStart >= 0 && rolesEnd > rolesStart);
const page = {innerHTML: ''};
const permissions = [
  {code:'api.read'}, {code:'api.write'}, {code:'credential.read'}, {code:'plugin.read'}, {code:'user.manage'}, {code:'audit.read'},
];
const roles = [
  {name:'super_admin',permissions:['*']},
  {name:'operator',permissions:['api.read','api.write','credential.read']},
];
const context = {
  esc(value) {return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));},
  can() {return true;},
  $(selector) {return selector === '#page' ? page : null;},
  $$(selector) {return [];},
  async api(url) {return url.endsWith('permissions') ? permissions : roles;},
};
vm.createContext(context);
vm.runInContext(script.slice(helpersStart, helpersEnd) + script.slice(rolesStart, rolesEnd), context);

test('permission choices remain compact, grouped, named, and preselected', () => {
  const html = context.permissionChecklist(permissions, ['api.write']);
  assert.match(html, /<fieldset class="permission-group"><legend>接口管理<\/legend>/);
  assert.match(html, /<legend>调用凭证<\/legend>/);
  assert.match(html, /<label class="permission-option" title="api.write"><input type="checkbox" name="permission" value="api.write" checked>/);
  assert.equal((html.match(/name="permission"/g) || []).length, permissions.length);
  assert.match(css, /\.form-stack \.permission-option input\[type="checkbox"\]\{[^}]*height:18px;min-height:18px/);
});

test('roles list uses a full-width page and collapsible permission details', async () => {
  await context.renderRoles();
  assert.match(page.innerHTML, /class="roles-page"/);
  assert.match(page.innerHTML, /class="roles-table"/);
  assert.match(page.innerHTML, /<details class="role-permission-details"><summary>3 项权限/);
  assert.match(page.innerHTML, /<span class="badge">全部权限<\/span>/);
  assert.match(page.innerHTML, /<section class="card role-editor"/);
  assert.doesNotMatch(page.innerHTML, /<div class="split">/);
});

test('permission descriptions escape untrusted codes in summary and checklist', () => {
  const evil = '<script>alert(1)</script>';
  assert.doesNotMatch(context.permissionChecklist([{code:evil}]), /<script>/);
  assert.doesNotMatch(context.rolePermissionsSummary([evil]), /<script>/);
  assert.match(context.rolePermissionsSummary([evil]), /&lt;script&gt;/);
});
