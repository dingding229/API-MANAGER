const state = { token: sessionStorage.getItem('api_manager_token') || '', user: null, permissions: [], page: 'overview', cache: {} };
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const esc = (value) => String(value ?? '').replace(/[&<>'"]/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const can = (permission) => state.permissions.includes('*') || state.permissions.includes(permission);

// 展示名称不参与鉴权或提交；未知的自定义权限保留原始代码。
const permissionLabels = {
  '*': '全部权限',
  'api.read': '查看接口',
  'api.write': '创建与修改接口',
  'api.publish': '发布、下线与回滚接口',
  'api.delete': '删除接口',
  'credential.read': '查看调用凭证列表',
  'credential.reveal': '查看完整调用密钥',
  'credential.write': '创建与吊销调用凭证',
  'plugin.read': '查看插件',
  'plugin.manage': '管理插件配置',
  'user.read': '查看用户、角色与权限',
  'user.manage': '创建用户与分配角色',
  'audit.read': '查看审计日志',
};
const roleLabels = {
  super_admin: '超级管理员',
  tenant_admin: '平台管理员（单租户）',
  operator: '运维人员',
  api_developer: '接口开发者',
  viewer: '只读用户',
};
const roleDescriptions = {
  super_admin: '完整管理权限',
  tenant_admin: '平台管理权限（当前仅支持单租户）',
  operator: '接口运维权限',
  api_developer: '接口开发权限',
  viewer: '只读权限',
};
const permissionLabel = (code) => permissionLabels[code] || code;
const roleLabel = (name) => roleLabels[name] || name;
const roleDescription = (role) => roleDescriptions[role.name] || role.description || '';
function permissionChecklist(permissions, selected = []) {
  const selectedCodes = new Set(selected);
  const categories = {api: '接口管理', credential: '调用凭证', plugin: '插件管理', user: '用户管理', audit: '审计日志'};
  const groups = new Map();
  for (const permission of permissions) {
    const category = permission.code.split('.')[0];
    const name = categories[category] || '其他权限';
    if (!groups.has(name)) groups.set(name, []);
    groups.get(name).push(permission);
  }
  return `<div class="permission-groups" role="group" aria-label="选择权限">${[...groups].map(([name, entries]) => `<fieldset class="permission-group"><legend>${esc(name)}</legend><div class="permission-options">${entries.map(p => `<label class="permission-option" title="${esc(p.code)}"><input type="checkbox" name="permission" value="${esc(p.code)}" ${selectedCodes.has(p.code) ? 'checked' : ''}><span>${esc(permissionLabel(p.code))}</span></label>`).join('')}</div></fieldset>`).join('')}</div>`;
}

function rolePermissionsSummary(codes = []) {
  if (!codes.length) return '<span class="role-permission-count">未分配权限</span>';
  if (codes.includes('*')) return '<span class="badge">全部权限</span>';
  return `<details class="role-permission-details"><summary>${codes.length} 项权限 <span>查看详情</span></summary><div class="role-permission-list">${codes.map(code => `<span class="badge" title="${esc(code)}">${esc(permissionLabel(code))}</span>`).join('')}</div></details>`;
}

async function api(path, options = {}) {
  const headers = {'Content-Type': 'application/json', ...(options.headers || {})};
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const response = await fetch(path, {...options, headers});
  const body = await response.json().catch(() => ({}));
  if (response.status === 401 && state.token) { logout(); throw new Error('登录已过期'); }
  if (!response.ok) throw new Error(body.error || `请求失败：${response.status}`);
  return body;
}

function notice(message, ok = false, target = '') {
  const loginVisible = !$('#login-view')?.classList.contains('hidden');
  const node = target === 'auth' || (!target && loginVisible) ? $('#auth-message') : $('#message');
  if (!node) return;
  node.textContent = message || '';
  node.className = `message${ok ? ' ok' : ''}`;
  if (message) setTimeout(() => { if (node.textContent === message) node.textContent = ''; }, 5000);
}

async function withSubmitting(form, pendingText, action) {
  const button = form.querySelector('button[type="submit"]');
  const originalText = button?.textContent || '';
  if (button) { button.disabled = true; button.textContent = pendingText; }
  try { return await action(); }
  finally { if (button) { button.disabled = false; button.textContent = originalText; } }
}

function setBootstrapAvailable(available, clear = false) {
  const panel = $('#bootstrap-panel');
  if (!panel) return;
  panel.hidden = !available;
  if (!available) {
    panel.open = false;
    if (clear) $('#bootstrap-form')?.reset();
  }
}

async function refreshBootstrapAvailability() {
  setBootstrapAvailable(false);
  try {
    const response = await fetch('/auth/v1/bootstrap/status', {headers:{Accept:'application/json'}, cache:'no-store'});
    const body = await response.json().catch(() => ({}));
    const available = response.ok && body.available === true;
    setBootstrapAvailable(available, !available);
  } catch (_) {
    // Fail closed: a status failure must not expose a first-use administrator form.
    setBootstrapAvailable(false);
  }
}

function authErrorMessage(error) {
  const message = error?.message || '请求失败';
  if (message === 'invalid credentials') return '邮箱或密码错误';
  if (message === 'bootstrap is not available') return '初始化失败：管理员 Token 不正确，或系统已经完成初始化';
  if (message.includes('valid email and password')) return '请输入有效邮箱，密码至少 8 位';
  return message;
}

function showConsole() {
  setBootstrapAvailable(false, true);
  $('#login-view').classList.add('hidden');
  $('#console-view').classList.remove('hidden');
  $('#current-user').textContent = `${state.user?.email || ''} · ${state.user?.roles?.join(', ') || state.user?.role || ''}`;
  $$('#nav button[data-permission]').forEach((button) => button.classList.toggle('hidden', !can(button.dataset.permission)));
  renderPage();
}

function showLogin() {
  $('#login-view').classList.remove('hidden');
  $('#console-view').classList.add('hidden');
  void refreshBootstrapAvailability();
}

function logout() {
  state.token = ''; state.user = null; state.permissions = [];
  sessionStorage.removeItem('api_manager_token');
  showLogin();
}

async function login(email, password) {
  const result = await api('/auth/v1/login', {method:'POST', body: JSON.stringify({email, password})});
  state.token = result.token; state.user = result.user; state.permissions = result.permissions || [];
  sessionStorage.setItem('api_manager_token', state.token); showConsole();
}

async function bootstrap(email, password, token) {
  const result = await fetch('/auth/v1/bootstrap', {method:'POST', headers:{'Content-Type':'application/json','X-Admin-Token':token}, body:JSON.stringify({email,password})});
  const body = await result.json().catch(() => ({}));
  if (!result.ok) {
    if (result.status === 404) throw new Error('bootstrap is not available');
    throw new Error(body.error || '初始化失败');
  }
  return body;
}

async function hydrateSession() {
  if (!state.token) return showLogin();
  try { const result = await api('/auth/v1/me'); state.user = result.user; state.permissions = result.permissions || []; showConsole(); }
  catch (_) { showLogin(); }
}

function renderPage() {
  const titles = {overview:'总览', apis:'接口管理', credentials:'调用凭证', users:'用户管理', roles:'角色与权限', plugins:'插件', audit:'审计日志'};
  $('#page-title').textContent = titles[state.page] || '总览';
  $$('#nav button').forEach((button) => button.classList.toggle('active', button.dataset.page === state.page));
  const renderers = {overview: renderOverview, apis: renderAPIs, credentials: renderCredentials, users: renderUsers, roles: renderRoles, plugins: renderPlugins, audit: renderAuditLogs};
  return renderers[state.page]();
}

async function renderOverview() {
  const page = $('#page'); page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const [apis, users, credentials, plugins, ready] = await Promise.all([
      can('api.read') ? api('/admin/v1/apis') : Promise.resolve([]),
      can('user.read') ? api('/admin/v1/users') : Promise.resolve([]),
      can('credential.read') ? api('/admin/v1/credentials') : Promise.resolve([]),
      can('plugin.read') ? api('/admin/v1/plugins') : Promise.resolve({plugins:[]}),
      fetch('/health/ready').then((r) => r.json()).catch(() => ({status:'unknown'}))
    ]);
    page.innerHTML = `<div class="stats">
      <div class="stat"><span class="small">接口数量</span><div class="number">${apis.length}</div></div>
      <div class="stat"><span class="small">已发布接口</span><div class="number">${apis.filter(x=>x.enabled).length}</div></div>
      <div class="stat"><span class="small">用户数量</span><div class="number">${users.length}</div></div>
      <div class="stat"><span class="small">调用凭证</span><div class="number">${credentials.length}</div></div>
      <div class="stat"><span class="small">插件</span><div class="number">${(plugins.plugins || []).length}</div></div>
      <div class="stat"><span class="small">服务状态</span><div class="number">${esc(ready.status)}</div></div>
    </div>
    <div class="split spaced-split">
      <div class="card"><h2>快速开始</h2><p class="muted">先创建调用凭证，再创建接口并发布。所有管理操作都会经过后端 RBAC 权限校验。</p><div class="actions"><button data-go="apis">管理接口</button><button class="secondary" data-go="credentials">创建凭证</button></div></div>
      <div class="card"><h2>当前账号</h2><p>${esc(state.user?.email)}</p><p class="small">角色：${esc((state.user?.roles || [state.user?.role]).filter(Boolean).join(', '))}</p><p class="small">权限：${state.permissions.length} 项</p></div>
    </div>`;
    $$('[data-go]').forEach((button) => button.onclick = () => { state.page = button.dataset.go; renderPage(); });
  } catch (error) { page.innerHTML = `<div class="empty">${esc(error.message)}</div>`; }
}

async function renderAPIs() {
  const page = $('#page'); page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const apis = await api('/admin/v1/apis');
    page.innerHTML = `<div class="split"><div class="table-wrap"><div class="toolbar table-toolbar"><h2>已配置接口</h2><button class="secondary" id="openapi">导出 OpenAPI</button></div><table><thead><tr><th>名称</th><th>路由</th><th>鉴权</th><th>状态</th><th>操作</th></tr></thead><tbody>${apis.length ? apis.map(apiRow).join('') : '<tr><td colspan="5"><div class="empty">暂无接口</div></td></tr>'}</tbody></table></div><div class="card"><h2 id="api-form-title">创建接口</h2>${apiForm()}<hr class="section-line"><details class="import-openapi"><summary>导入 OpenAPI 3.x 文档</summary>${openAPIImportForm()}</details></div></div>`;
    $('#openapi').onclick = async () => { try { const document = await api('/admin/v1/openapi.json'); const blob = new Blob([JSON.stringify(document, null, 2)], {type:'application/json'}); const url = URL.createObjectURL(blob); window.open(url, '_blank', 'noopener,noreferrer'); setTimeout(() => URL.revokeObjectURL(url), 30000); } catch(error) { notice(error.message); } };
    $('#api-form').onsubmit = createAPI;
    $('#api-form').elements.path.oninput = (event) => event.target.setCustomValidity('');
    $('#openapi-import-form').onsubmit = importOpenAPI;
    $$('#page [data-action]').forEach((button) => button.onclick = () => apiAction(button.dataset.action, button.dataset.id));
    $$('#page [data-edit-api]').forEach((button) => button.onclick = () => editAPI(button.dataset.editApi, apis));
  } catch (error) { page.innerHTML = `<div class="empty">${esc(error.message)}</div>`; }
}

function apiRow(item) {
  const action = item.enabled ? `<button class="secondary" data-action="unpublish" data-id="${esc(item.id)}">下线</button>` : `<button data-action="publish" data-id="${esc(item.id)}">发布</button>`;
  const source = item.plugin ? `插件：${esc(item.plugin)}` : item.upstream_url ? `上游：${esc(item.upstream_url)}` : '静态响应';
  return `<tr><td><strong>${esc(item.name)}</strong><br><span class="small">${esc(source)}</span></td><td><code>${esc(item.method)} ${esc(item.path)}</code></td><td>${esc(item.auth_mode || 'none')}</td><td><span class="badge ${item.enabled?'':'off'}">${item.enabled?'已发布':'草稿'}</span></td><td><div class="actions">${can('api.write')?`<button class="secondary" data-edit-api="${esc(item.id)}">编辑</button>`:''}${can('api.publish')?action:''}${can('api.delete')?`<button class="danger" data-action="delete" data-id="${esc(item.id)}">删除</button>`:''}</div></td></tr>`;
}

function selected(value, expected) { return String(value ?? '') === String(expected) ? 'selected' : ''; }
function apiForm(item = {}) {
  const value = (key, fallback = '') => esc(item[key] ?? fallback);
  const jsonValue = (key) => item[key] ? esc(JSON.stringify(item[key], null, 2)) : '';
  const hasUpstream = item.upstream_url || item.upstream_path || item.upstream_auth_ref;
  const hasResilience = Number(item.upstream_timeout_ms || 0) || Number(item.upstream_retries || 0) || Number(item.circuit_breaker_threshold || 0) || (item.circuit_breaker_reset_seconds !== undefined && Number(item.circuit_breaker_reset_seconds) !== 30);
  const hasResponse = item.response_body || Number(item.response_status || 0) || item.request_schema || item.response_schema || item.parameters_schema;
  const hasQuota = Number(item.rate_limit_per_minute || 0) || Number(item.daily_quota || 0) || Number(item.monthly_quota || 0);
  return `<form id="api-form" class="form-stack api-form" data-api-id="${value('id')}">
    <section class="form-section form-section-primary" aria-labelledby="api-basics-title">
      <div class="form-section-heading"><div><h3 id="api-basics-title">基础信息</h3><p>先定义公开路由和访问方式。</p></div><span class="required-note">带 * 为必填</span></div>
      <label class="field"><span class="field-label">名称 <span aria-hidden="true">*</span></span><input name="name" required placeholder="订单查询" value="${value('name')}"></label>
      <div class="grid-2">
        <label class="field"><span class="field-label">方法 <span aria-hidden="true">*</span></span><select name="method"><option ${selected(item.method,'GET')}>GET</option><option ${selected(item.method,'POST')}>POST</option><option ${selected(item.method,'PUT')}>PUT</option><option ${selected(item.method,'DELETE')}>DELETE</option><option ${selected(item.method,'PATCH')}>PATCH</option><option ${selected(item.method,'HEAD')}>HEAD</option><option ${selected(item.method,'OPTIONS')}>OPTIONS</option>${item.method && !['GET','POST','PUT','DELETE','PATCH','HEAD','OPTIONS'].includes(item.method) ? `<option selected>${esc(item.method)}</option>` : ''}</select></label>
        <label class="field"><span class="field-label">鉴权</span><select name="auth_mode"><option value="none" ${selected(item.auth_mode,'none')}>无</option><option value="api_key" ${selected(item.auth_mode,'api_key')}>API Key</option><option value="jwt" ${selected(item.auth_mode,'jwt')}>JWT</option><option value="hmac" ${selected(item.auth_mode,'hmac')}>HMAC</option></select></label>
      </div>
      <label class="field"><span class="field-label">访问路径 <span aria-hidden="true">*</span></span><input name="path" required placeholder="/api/example/v1/status" value="${value('path')}" aria-describedby="path-hint"><span id="path-hint" class="field-hint">必须以 / 开头；只填路径，不要填插件名、完整网址或连续斜线。</span></label>
      <label class="field"><span class="field-label">说明</span><input name="description" value="${value('description')}" placeholder="简短描述这个接口的用途"></label>
      <label class="field"><span class="field-label">插件名称</span><input name="plugin" placeholder="your-plugin-name" value="${value('plugin')}" aria-describedby="plugin-hint"><span id="plugin-hint" class="field-hint">与访问路径分开填写；不使用插件时留空。</span></label>
    </section>
    <details class="form-section form-section-collapsible" ${hasUpstream ? 'open' : ''}>
      <summary><span><strong>上游转发</strong><small>连接外部服务或内部插件以外的上游接口</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body">
        <label class="field"><span class="field-label">上游 URL</span><input name="upstream_url" placeholder="https://example.com" value="${value('upstream_url')}"></label>
        <div class="grid-2"><label class="field"><span class="field-label">上游路径</span><input name="upstream_path" placeholder="/v1/offers/{external_id}" value="${value('upstream_path')}"></label><label class="field"><span class="field-label">上游凭证引用</span><input name="upstream_auth_ref" placeholder="仅填写引用名，不要填写 Key" value="${value('upstream_auth_ref')}"></label></div>
        <label class="checkbox-field"><input type="checkbox" name="strip_path" ${item.strip_path ? 'checked' : ''}><span>转发时剥离匹配路径</span></label>
      </div>
    </details>
    <details class="form-section form-section-collapsible" ${hasResilience ? 'open' : ''}>
      <summary><span><strong>可靠性策略</strong><small>超时、重试与熔断</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body"><div class="grid-2"><label class="field"><span class="field-label">上游超时（毫秒）</span><input type="number" min="0" max="120000" name="upstream_timeout_ms" value="${value('upstream_timeout_ms','0')}"></label><label class="field"><span class="field-label">失败重试次数</span><input type="number" min="0" max="5" name="upstream_retries" value="${value('upstream_retries','0')}"><span class="field-hint">仅建议对幂等、安全方法启用。</span></label><label class="field"><span class="field-label">熔断阈值</span><input type="number" min="0" max="100" name="circuit_breaker_threshold" value="${value('circuit_breaker_threshold','0')}"><span class="field-hint">填 0 表示关闭。</span></label><label class="field"><span class="field-label">熔断恢复秒数</span><input type="number" min="0" max="3600" name="circuit_breaker_reset_seconds" value="${value('circuit_breaker_reset_seconds','30')}"></label></div></div>
    </details>
    <details class="form-section form-section-collapsible" ${hasResponse ? 'open' : ''}>
      <summary><span><strong>响应与校验</strong><small>静态响应和 JSON Schema 均为可选</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body"><label class="field"><span class="field-label">鉴权配置 JSON</span><textarea name="auth_config" class="schema-input" placeholder="{}">${jsonValue('auth_config')}</textarea></label><label class="field"><span class="field-label">响应状态码</span><input type="number" min="0" max="599" name="response_status" value="${value('response_status',item.response_body ? '200' : '0')}"><span class="field-hint">填 0 使用默认行为。</span></label><label class="field"><span class="field-label">静态响应 JSON</span><textarea name="response_body" placeholder='{"message":"ok"}'>${value('response_body')}</textarea></label><div class="grid-2"><label class="field"><span class="field-label">请求 JSON Schema</span><textarea name="request_schema" class="schema-input" placeholder='{"type":"object"}'>${jsonValue('request_schema')}</textarea></label><label class="field"><span class="field-label">响应 JSON Schema</span><textarea name="response_schema" class="schema-input" placeholder='{"type":"object"}'>${jsonValue('response_schema')}</textarea></label></div><label class="field"><span class="field-label">参数 JSON Schema</span><textarea name="parameters_schema" class="schema-input" placeholder='{"type":"object","properties":{"query":{"type":"object"}}}'>${jsonValue('parameters_schema')}</textarea><span class="field-hint">根对象可包含 query、path、header。</span></label></div>
    </details>
    <details class="form-section form-section-collapsible" ${hasQuota ? 'open' : ''}>
      <summary><span><strong>限流与配额</strong><small>按接口控制调用频率和周期配额</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body"><div class="grid-2"><label class="field"><span class="field-label">每分钟限制</span><input type="number" min="0" name="rate_limit_per_minute" value="${value('rate_limit_per_minute','0')}"></label><label class="field"><span class="field-label">每日配额</span><input type="number" min="0" name="daily_quota" value="${value('daily_quota','0')}"></label></div><label class="field"><span class="field-label">每月配额</span><input type="number" min="0" name="monthly_quota" value="${value('monthly_quota','0')}"></label></div>
    </details>
    <div class="form-submit"><span class="small">保存后仍需单独发布接口，草稿不会立即对外生效。</span><div class="actions"><button type="submit">${item.id ? '保存接口修改' : '创建并保存草稿'}</button>${item.id ? '<button type="button" class="secondary" id="cancel-api-edit">取消编辑</button>' : ''}</div></div>
  </form>`;
}
function apiFormData(form) {
  const data = Object.fromEntries(new FormData(form).entries());
  for (const key of ['request_schema', 'response_schema', 'parameters_schema', 'auth_config']) { if (data[key]?.trim()) data[key] = JSON.parse(data[key]); else delete data[key]; }
  for (const key of ['rate_limit_per_minute', 'daily_quota', 'monthly_quota', 'response_status', 'upstream_timeout_ms', 'upstream_retries', 'circuit_breaker_threshold', 'circuit_breaker_reset_seconds']) data[key] = Number(data[key] || 0);
  data.strip_path = form.elements.strip_path.checked;
  return data;
}

function routePathError(path) {
  if (!path.startsWith('/')) return '访问路径必须以 / 开头，例如 /api/example/v1/status；插件名称请填在“插件名称”字段。';
  if (path.includes('//')) return '访问路径不能包含连续的 //；请只填写路径，不要填写 http:// 或 https:// 开头的完整网址。';
  return '';
}

async function createAPI(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const pathField = form.elements.path;
  const path = pathField.value.trim();
  const pathError = routePathError(path);
  if (pathError) {
    pathField.setCustomValidity(pathError);
    pathField.reportValidity();
    pathField.focus();
    return;
  }
  pathField.setCustomValidity('');
  pathField.value = path;
  try {
    const data = apiFormData(form);
    const id = form.dataset.apiId;
    await api(id ? `/admin/v1/apis/${encodeURIComponent(id)}` : '/admin/v1/apis', {method:id ? 'PUT' : 'POST', body:JSON.stringify(data)});
    notice(id ? '接口修改已保存' : '接口创建成功', true); renderAPIs();
  } catch (error) { notice(error instanceof SyntaxError ? 'JSON 配置或 Schema 必须是合法 JSON' : error.message); }
}

async function editAPI(id, apis) {
  try {
    const item = apis.find(candidate => candidate.id === id) || await api(`/admin/v1/apis/${encodeURIComponent(id)}`);
    $('#api-form-title').textContent = `编辑接口：${item.name}`;
    const card = $('#api-form-title').closest('.card');
    card.querySelector('#api-form').outerHTML = apiForm(item);
    card.querySelector('#api-form').onsubmit = createAPI;
    card.querySelector('#api-form').elements.path.oninput = (event) => event.target.setCustomValidity('');
    $('#cancel-api-edit').onclick = () => renderAPIs();
    card.scrollIntoView({behavior:'smooth', block:'start'});
  } catch (error) { notice(error.message); }
}


function openAPIImportForm() {
  return `<form id="openapi-import-form" class="form-stack"><p class="small">导入会根据每个 path + operation 创建草稿，并从文档的 JSON 请求/响应 Schema 生成校验规则。默认使用文档首个 servers URL，也可在下方覆盖。</p><label>上游 URL（可选覆盖）<input name="upstream_url" placeholder="https://api.example.com"></label><label>路径前缀（可选）<input name="path_prefix" placeholder="/api"></label><label>OpenAPI 3.x 文档（JSON 或 YAML）<textarea name="document" required class="openapi-input" placeholder="openapi: 3.0.3&#10;servers:&#10;  - url: https://api.example.com&#10;paths: {}"></textarea></label><button type="submit" class="secondary">导入为草稿</button></form>`;
}

async function importOpenAPI(event) {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const text = String(form.get('document') || '').trim();
  const data = {upstream_url: form.get('upstream_url'), path_prefix: form.get('path_prefix')};
  try {
    try { data.document = JSON.parse(text); }
    catch (_) { data.document_yaml = text; }
    const result = await api('/admin/v1/openapi/import', {method:'POST', body:JSON.stringify(data)});
    const errors = result.errors || [];
    notice(`已导入 ${result.created_count || 0} 个接口${errors.length ? `，${errors.length} 个操作未导入` : ''}`, errors.length === 0);
    renderAPIs();
  } catch (error) { notice(error.message); }
}

async function apiAction(action, id) {
  if (!['publish', 'unpublish', 'delete'].includes(action)) return;
  if (action === 'delete' && !confirm('确定删除该接口？')) return;
  const path = `/admin/v1/apis/${encodeURIComponent(id)}`;
  try {
    await api(action === 'delete' ? path : `${path}/${action}`, {method:action === 'delete' ? 'DELETE' : 'POST'});
    notice(action === 'delete' ? '接口已删除' : '状态已更新', true);
    renderAPIs();
  } catch (error) { notice(error.message); }
}

async function renderCredentials() {
  const page = $('#page'); page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const items = await api('/admin/v1/credentials');
    page.innerHTML = `<div class="split"><div class="table-wrap"><table><thead><tr><th>名称</th><th>前缀</th><th>状态</th><th>创建时间</th><th>操作</th></tr></thead><tbody>${items.length?items.map(c=>`<tr><td>${esc(c.name)}</td><td><code>${esc(c.prefix)}…</code></td><td><span class="badge ${c.revoked?'off':''}">${c.revoked?'已撤销':'有效'}</span></td><td>${esc(new Date(c.created_at).toLocaleString())}</td><td><div class="actions">${!c.revoked&&can('credential.reveal')?`<button class="secondary" data-view-key="${esc(c.id)}" ${c.api_key_available?'':'disabled title="历史凭证无法恢复，请先轮换"'}>${c.api_key_available?'查看并复制 Key':'需轮换后可查看'}</button>`:''}${!c.revoked&&can('credential.write')?`<button class="secondary" data-rotate="${esc(c.id)}">重新生成并查看 Key</button><button class="danger" data-revoke="${esc(c.id)}">撤销</button>`:''}</div></td></tr>`).join(''):'<tr><td colspan="5"><div class="empty">暂无凭证</div></td></tr>'}</tbody></table></div><div class="card"><h2>创建调用凭证</h2><form id="credential-form" class="form-stack"><label>名称<input name="name" required placeholder="production-client"></label><button>创建</button></form><p class="small">新建或轮换后的 Key 会加密保存。拥有查看权限的管理员可按需查看并复制；历史凭证若未加密保存，需要先轮换。</p></div></div>`;
    $('#credential-form').onsubmit = async (e) => { e.preventDefault(); try { const result = await api('/admin/v1/credentials',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(e.target).entries()))}); await renderCredentials(); showCredentialKey(result.api_key, '新建凭证'); } catch(error){notice(error.message)} };
    $$('#page [data-view-key]').forEach(button => button.onclick = async () => {
      if (button.disabled) return;
      try { const result = await api(`/admin/v1/credentials/${encodeURIComponent(button.dataset.viewKey)}/key`); showCredentialKey(result.api_key, '查看 API Key'); } catch(error) { notice(error.message); }
    });
    $$('#page [data-rotate]').forEach(button => button.onclick = async () => {
      if (!confirm('重新生成后，旧 API Key 会立即失效。确定继续吗？')) return;
      try { const result = await api(`/admin/v1/credentials/${encodeURIComponent(button.dataset.rotate)}/rotate`, {method:'POST'}); await renderCredentials(); showCredentialKey(result.api_key, '重新生成成功，旧 Key 已失效'); } catch(error) { notice(error.message); }
    });
    $$('#page [data-revoke]').forEach(button => button.onclick = async () => { if(confirm('确定撤销此凭证？')){try{await api(`/admin/v1/credentials/${encodeURIComponent(button.dataset.revoke)}/revoke`,{method:'POST'});renderCredentials()}catch(error){notice(error.message)}} });
  } catch (error) { page.innerHTML = `<div class="empty">${esc(error.message)}</div>`; }
}

function showCredentialKey(key, title) {
  const card = $('#credential-form')?.closest('.card');
  if (!card) return;
  const previous = $('#credential-key-result');
  if (previous) previous.remove();
  const panel = document.createElement('div');
  panel.id = 'credential-key-result';
  panel.className = 'credential-key-result';
  panel.innerHTML = `<strong>${esc(title)}</strong><p>仅向当前有权限的管理员显示。请复制并妥善保管，不要在聊天、日志或截图中传播。</p><div class="actions"><input aria-label="新的 API Key" type="text" readonly autocomplete="off" spellcheck="false"><button type="button" id="copy-credential-key">复制 Key</button><button type="button" class="secondary" id="hide-credential-key">关闭</button></div>`;
  card.append(panel);
  const input = panel.querySelector('input');
  input.value = key;
  $('#copy-credential-key').onclick = async () => {
    try { await navigator.clipboard.writeText(input.value); notice('Key 已复制，请妥善保管', true); }
    catch (error) { input.select(); notice('无法自动复制，请手动复制选中的 Key'); }
  };
  $('#hide-credential-key').onclick = () => { input.value = ''; panel.remove(); };
}

async function renderUsers() {
  const page = $('#page'); page.innerHTML = '<div class="empty">加载中…</div>';
  try { const [users, roles] = await Promise.all([api('/admin/v1/users'),api('/admin/v1/roles')]); page.innerHTML = `<div class="split"><div class="table-wrap"><table><thead><tr><th>邮箱</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>${users.length?users.map(u=>`<tr><td>${esc(u.email)}</td><td>${(u.roles||[u.role]).map(r=>`<span class="badge">${esc(r)}</span>`).join(' ')}</td><td><span class="badge ${u.status==='active'?'':'off'}">${esc(u.status)}</span></td><td><div class="actions">${can('user.manage')?`<button data-user-status="${esc(u.id)}" data-status="${u.status==='active'?'disabled':'active'}">${u.status==='active'?'禁用':'启用'}</button><button class="secondary" data-user-roles="${esc(u.id)}">编辑角色</button>`:''}</div></td></tr>`).join(''):'<tr><td colspan="4"><div class="empty">暂无用户</div></td></tr>'}</tbody></table></div><div class="card"><h2>创建用户</h2><form id="user-form" class="form-stack"><label>邮箱<input name="email" type="email" required></label><label>密码<input name="password" type="password" minlength="8" required></label><label>角色<select name="role">${roles.filter(r=>can('*') || (r.name !== 'super_admin' && r.name !== 'tenant_admin' && !(r.permissions || []).some(p=>p==='*'||p==='user.manage'))).map(r=>`<option value="${esc(r.name)}">${esc(r.name)}</option>`).join('')}</select></label><button>创建用户</button></form></div></div>`; $('#user-form').onsubmit=async(e)=>{e.preventDefault();try{await api('/admin/v1/users',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(e.target).entries()))});notice('用户创建成功',true);renderUsers()}catch(err){notice(err.message)}}; $$('#page [data-user-status]').forEach(b=>b.onclick=async()=>{try{await api(`/admin/v1/users/${b.dataset.userStatus}/status`,{method:'PUT',body:JSON.stringify({status:b.dataset.status})});renderUsers()}catch(e){notice(e.message)}}); $$('#page [data-user-roles]').forEach(b=>b.onclick=async()=>{const value=prompt('输入角色，多个角色用英文逗号分隔');if(value===null)return;try{await api(`/admin/v1/users/${b.dataset.userRoles}/roles`,{method:'PUT',body:JSON.stringify({roles:value.split(',').map(x=>x.trim()).filter(Boolean)})});renderUsers()}catch(e){notice(e.message)}}); } catch(error){page.innerHTML=`<div class="empty">${esc(error.message)}</div>`}
}

async function renderRoles() {
  const page = $('#page');
  page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const [roles, permissions] = await Promise.all([api('/admin/v1/roles'), api('/admin/v1/permissions')]);
    page.innerHTML = `<div class="roles-page">
      <div class="table-wrap roles-table-wrap"><table class="roles-table"><thead><tr><th scope="col">角色</th><th scope="col">说明</th><th scope="col">权限</th><th scope="col">操作</th></tr></thead><tbody>
      ${roles.map(r => `<tr><td><strong title="${esc(r.name)}">${esc(roleLabel(r.name))}</strong>${roleLabels[r.name] ? `<br><span class="small">${esc(r.name)}</span>` : ''}</td><td>${esc(roleDescription(r))}</td><td>${rolePermissionsSummary(r.permissions || [])}</td><td>${can('*') && r.name !== 'super_admin' ? `<button class="secondary" data-role="${esc(r.name)}">编辑权限</button>` : ''}</td></tr>`).join('')}
      </tbody></table></div>
      ${can('*') ? `<section class="card role-editor" id="role-editor"><h2>创建角色</h2><p class="small">设置角色信息，并选择该角色可以执行的操作。</p><form id="role-form" class="form-stack"><div class="role-fields"><label>名称<input name="name" required placeholder="例如 support"></label><label>说明<input name="description" placeholder="简述角色用途"></label></div>${permissionChecklist(permissions)}<div class="role-form-actions"><button type="submit">创建角色</button></div></form></section>` : ''}
    </div>`;
    const createForm = $('#role-form');
    if (createForm) createForm.onsubmit = async (event) => {
      event.preventDefault();
      const form = new FormData(event.currentTarget);
      try {
        await api('/admin/v1/roles', {method:'POST', body:JSON.stringify({name:form.get('name'), description:form.get('description'), permissions:form.getAll('permission')})});
        notice('角色创建成功', true); renderRoles();
      } catch (error) { notice(error.message); }
    };
    $$('#page [data-role]').forEach(button => button.onclick = () => {
      const role = roles.find(item => item.name === button.dataset.role);
      if (!role) return;
      const editor = $('#role-editor');
      editor.innerHTML = `<h2>编辑权限：${esc(roleLabel(role.name))}</h2><p class="small">${esc(role.name)} · 勾选要授予的权限，保存后立即生效。</p><form id="role-permissions-form" class="form-stack">${permissionChecklist(permissions, role.permissions || [])}<div class="role-form-actions"><button type="button" class="secondary" id="cancel-role-edit">取消</button><button type="submit">保存权限</button></div></form>`;
      $('#cancel-role-edit').onclick = () => renderRoles();
      $('#role-permissions-form').onsubmit = async (event) => {
        event.preventDefault();
        const codes = new FormData(event.currentTarget).getAll('permission');
        try {
          await api(`/admin/v1/roles/${encodeURIComponent(role.name)}/permissions`, {method:'PUT', body:JSON.stringify({permissions:codes})});
          notice('权限保存成功', true); renderRoles();
        } catch (error) { notice(error.message); }
      };
    });
  } catch (error) { page.innerHTML = `<div class="empty">${esc(error.message)}</div>`; }
}

async function renderAuditLogs(pageNumber = 1) {
  const page = $('#page');
  const filters = state.cache.auditFilters || {};
  page.innerHTML = `<div class="card audit-filters"><h2>筛选条件</h2><form id="audit-filter-form" class="filter-grid">
    <label>操作<input name="action" value="${esc(filters.action || '')}" placeholder="例如 api.publish"></label>
    <label>资源类型<input name="resource_type" value="${esc(filters.resource_type || '')}" placeholder="例如 api、user"></label>
    <label>操作者 ID<input name="actor_id" value="${esc(filters.actor_id || '')}" placeholder="用户 UUID"></label>
    <label>请求 ID<input name="request_id" value="${esc(filters.request_id || '')}" placeholder="X-Request-ID"></label>
    <label>开始时间<input name="from" type="datetime-local" value="${esc(filters.from || '')}"></label>
    <label>结束时间<input name="to" type="datetime-local" value="${esc(filters.to || '')}"></label>
    <div class="actions filter-actions"><button>查询</button><button type="button" id="audit-reset" class="secondary">重置</button></div>
  </form></div><div id="audit-results" class="empty" class="spaced-split">加载中…</div>`;
  $('#audit-filter-form').onsubmit = (event) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    state.cache.auditFilters = Object.fromEntries([...form.entries()].filter(([, value]) => value !== ''));
    renderAuditLogs(1);
  };
  $('#audit-reset').onclick = () => { state.cache.auditFilters = {}; renderAuditLogs(1); };
  try {
    const params = new URLSearchParams({...state.cache.auditFilters, page: String(pageNumber), page_size: '20'});
    ['from', 'to'].forEach((key) => {
      if (params.has(key)) {
        const local = params.get(key);
        const date = new Date(local);
        if (!Number.isNaN(date.valueOf())) params.set(key, date.toISOString());
      }
    });
    const result = await api(`/admin/v1/audit-logs?${params.toString()}`);
    const items = result.items || [];
    const totalPages = Math.max(1, Math.ceil((result.total || 0) / (result.page_size || 20)));
    $('#audit-results').className = 'table-wrap';
    $('#audit-results').innerHTML = `<div class="toolbar table-toolbar"><div><h2>审计记录</h2><p class="small">共 ${result.total || 0} 条；所有敏感字段均已脱敏。</p></div><span class="small">第 ${result.page || 1} / ${totalPages} 页</span></div>
      <table><thead><tr><th>时间</th><th>操作</th><th>操作者</th><th>资源</th><th>请求</th><th>详情</th></tr></thead><tbody>${items.length ? items.map(auditRow).join('') : '<tr><td colspan="6"><div class="empty">没有匹配的审计记录</div></td></tr>'}</tbody></table>
      <div class="pager"><button id="audit-prev" class="secondary" ${result.page <= 1 ? 'disabled' : ''}>上一页</button><button id="audit-next" class="secondary" ${result.page >= totalPages ? 'disabled' : ''}>下一页</button></div>`;
    $('#audit-prev').onclick = () => { if (result.page > 1) renderAuditLogs(result.page - 1); };
    $('#audit-next').onclick = () => { if (result.page < totalPages) renderAuditLogs(result.page + 1); };
  } catch (error) { $('#audit-results').className = 'empty'; $('#audit-results').textContent = error.message; }
}

function auditRow(item) {
  const timestamp = item.created_at ? new Date(item.created_at).toLocaleString('zh-CN', {hour12:false}) : '-';
  const actor = item.actor_email || item.actor_id || item.actor_type || '-';
  const details = Object.keys(item.details || {}).length ? `<details><summary>查看</summary><pre>${esc(JSON.stringify(item.details, null, 2))}</pre></details>` : '-';
  return `<tr><td><time datetime="${esc(item.created_at || '')}">${esc(timestamp)}</time></td><td><span class="badge">${esc(item.action)}</span><br><span class="small">${esc(item.method || '')} ${esc(item.status_code || '')}</span></td><td>${esc(actor)}<br><span class="small">${esc(item.actor_type || '')}</span></td><td><strong>${esc(item.resource_type)}</strong><br><code>${esc(item.resource_id || '-')}</code></td><td><code>${esc(item.request_id || '-')}</code><br><span class="small">${esc(item.remote_addr || '')}</span></td><td class="audit-details">${details}</td></tr>`;
}

async function renderPlugins() {
  const page = $('#page'); page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const [result, apis, library] = await Promise.all([
      api('/admin/v1/plugins'),
      can('api.read') ? api('/admin/v1/apis') : Promise.resolve([]),
      can('plugin.read') ? api('/admin/v1/plugin-library') : Promise.resolve([]),
    ]);
    const managed = result.managed || [];
    // Versions share a plugin name and the same API routes; show usage only once per name.
    const unique = [...new Map(managed.map(item => [item.name, managed.find(version => version.name === item.name && version.enabled) || item])).values()];
    const usage = unique.map(item => pluginUsage(item, apis)).join('') || '<div class="plugin-empty">还没有托管插件。上传并启用 WASM 插件后，可以在这里配置调用路由。</div>';
    const runtime = (result.plugins || []).map(name => `<code>${esc(name)}</code>`).join(' ') || '<span>暂无已加载插件</span>';
    page.innerHTML = `
      <div class="plugin-page">
        <div class="plugin-top">
          <section class="plugin-panel plugin-versions" aria-labelledby="plugin-versions-title">
            <div class="plugin-panel-head"><div><h2 id="plugin-versions-title">已安装插件</h2><p>管理版本与运行状态</p></div><span class="plugin-count">${managed.length} 个版本</span></div>
            <div class="plugin-version-list">${managed.length ? managed.map(item => pluginRow(item, library || [])).join('') : '<div class="plugin-empty">暂无托管插件</div>'}</div>
            <div class="plugin-runtime"><span>运行时已加载</span><div>${runtime}</div></div>
          </section>
          <section class="plugin-panel plugin-upload" aria-labelledby="plugin-upload-title">
            <div class="plugin-panel-head"><div><h2 id="plugin-upload-title">上传 WASM</h2><p>添加一个新插件版本</p></div></div>
            <p class="plugin-upload-hint">上传 <code>manifest.yaml</code> 和对应的 <code>.wasm</code> 模块。上传后默认为禁用，启用后仍需配置并发布接口路由。</p>
            ${can('plugin.manage') ? `<form id="plugin-upload-form" class="form-stack" enctype="multipart/form-data"><label class="field"><span class="field-label">清单文件</span><input type="file" name="manifest" accept=".yaml,.yml" required><span class="field-hint">描述插件名称、版本和路由。</span></label><label class="field"><span class="field-label">WASM 模块</span><input type="file" name="wasm" accept=".wasm,application/wasm" required><span class="field-hint">上传与清单入口文件一致的 WASM 文件。</span></label><button type="submit">上传插件</button></form>` : '<p class="small">当前账号没有插件管理权限。</p>'}
          </section>
        </div>
        <section class="plugin-panel plugin-usage" aria-labelledby="plugin-usage-title">
          <div class="plugin-panel-head"><div><h2 id="plugin-usage-title">接口与调用</h2><p>查看路由状态，复制请求示例</p></div></div>
          <p class="plugin-usage-help">插件需要绑定并发布接口才可调用。接口建议来自插件 manifest 的 routes 声明；未声明的插件会生成通用状态路由建议。API Key 可在“调用凭证”页创建；示例中的凭证需替换成你自己的。</p>
          ${usage}
        </section>
        <section class="plugin-panel plugin-library" aria-labelledby="plugin-library-title">
          <div class="plugin-panel-head"><div><h2 id="plugin-library-title">插件库</h2><p>服务器本地维护的插件包</p></div></div>
          <div class="plugin-library-list">${renderPluginLibrary(library || [])}</div>
        </section>
      </div>`;
    if ($('#plugin-upload-form')) $('#plugin-upload-form').onsubmit = uploadPlugin;
    $$('#page [data-plugin-status]').forEach(button => button.onclick = () => setPluginStatus(button.dataset.pluginStatus, button.dataset.enabled === 'true'));
    $$('#page [data-plugin-uninstall]').forEach(button => button.onclick = () => uninstallPlugin(button.dataset.pluginUninstall, button.dataset.pluginName, button.dataset.pluginVersion, button.dataset.enabled === 'true'));
    $$('#page [data-copy-call]').forEach(button => button.onclick = async () => { try { await navigator.clipboard.writeText(button.dataset.copyCall); notice('调用命令已复制', true); } catch (_) { notice('复制失败，请手动复制命令'); } });
    $$('#page [data-plugin-route]').forEach(button => button.onclick = () => openPluginRouteDraft(button.dataset.pluginName, button.dataset.pluginRoute, button.dataset.pluginMethod, button.dataset.pluginAuth, button.dataset.pluginTitle));
    $$('#page [data-plugin-library-install]').forEach(button => button.onclick = () => installPluginLibrary(button.dataset.pluginLibraryName, button.dataset.pluginLibraryVersion));
    $$('#page [data-plugin-library-publish]').forEach(button => button.onclick = () => publishPluginLibrary(button.dataset.pluginLibraryPublish));
  } catch (error) { page.innerHTML = `<div class="empty">${esc(error.message)}</div>`; }
}

async function openPluginRouteDraft(pluginName, path, method = 'GET', authMode = 'api_key', title = '') {
  if (!can('api.write')) return;
  state.page = 'apis';
  await renderPage();
  const form = $('#api-form');
  if (!form) return;
  form.elements.name.value = title || `${pluginName} · ${path.split('/').pop()}`;
  form.elements.method.value = method;
  form.elements.auth_mode.value = authMode;
  form.elements.path.value = path;
  form.elements.plugin.value = pluginName;
  form.scrollIntoView({behavior:'smooth', block:'start'});
  notice('已预填路径和插件名称；确认后保存草稿，再点击“发布”。', true);
}

function shellQuote(value) { return "'" + String(value).replaceAll("'", "'\"'\"'") + "'"; }

function pluginCallCommand(item, apiItem) {
  const examples = apiItem.example_path_params || {};
  const path = apiItem.path.replace(/\{([^}]+)\}/g, (_, key) => encodeURIComponent(examples[key] || `REPLACE_WITH_${key}`));
  const query = Object.entries(apiItem.example_query || {}).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join('&');
  const method = shellQuote(apiItem.method || 'GET');
  let auth = '';
  if (apiItem.auth_mode === 'api_key') auth = ' -H "X-API-Key: 你的 API Key"';
  if (apiItem.auth_mode === 'jwt') auth = ' -H "Authorization: Bearer 你的 JWT"';
  return `curl -X ${method}${auth} ${shellQuote(location.origin + path + (query ? '?' + query : ''))}`;
}

function pluginUsage(item, apis) {
  const routes = (apis || []).filter(apiItem => apiItem.plugin === item.name);
  let manifest = item.manifest || {};
  if (typeof manifest === 'string') { try { manifest = JSON.parse(manifest); } catch (_) { manifest = {}; } }
  const suggested = Array.isArray(manifest.routes) ? manifest.routes : [];
  // Declared endpoints stay visible after only some have been published; actual route settings take precedence.
  const listed = suggested.map(spec => {
    const bound = routes.find(route => route.method === spec.method && route.path === spec.path);
    const conflict = !bound && (apis || []).some(route => route.method === spec.method && route.path === spec.path);
    return {...spec, ...(bound || {unconfigured:true}), name:bound?.name || spec.name || spec.path.split('/').pop(),
      auth_mode:bound?.auth_mode || spec.auth_mode, conflict};
  });
  listed.push(...routes.filter(route => !suggested.some(spec => route.method === spec.method && route.path === spec.path)));
  const count = routes.filter(route => route.enabled && route.published_at).length;
  const heading = `<div class="plugin-usage-heading"><div><h3>${esc(item.name)}</h3><span>${count} 条已发布接口${!item.enabled ? ' · 插件未启用' : ''}</span></div><span class="plugin-state ${item.enabled ? 'is-on' : ''}">${item.enabled ? '运行中' : '已停用'}</span></div>`;
  const rows = listed.length ? listed.map(route => pluginRouteRow(item, route)).join('') : '<div class="plugin-empty">此插件未声明接口，也尚未绑定 API。请先参考插件文档，确认实际支持的路径，然后在“接口管理”手动创建并发布；控制台不会猜测接口。</div>';
  const note = suggested.length ? '接口建议来自当前插件 manifest；插件需自行实现声明的路径。未配置或未发布的接口不能调用。' : '此插件未声明接口；下方仅展示已实际绑定的 API，不提供未经验证的建议路由。';
  return `<section class="plugin-usage-item">${heading}<div class="plugin-route-list">${rows}</div><p class="plugin-footnote">${note}</p></section>`;
}

function pluginRouteRow(item, route) {
  const unconfigured = Boolean(route.unconfigured);
  const available = item.enabled && route.enabled && route.published_at && !unconfigured;
  const label = unconfigured ? (route.conflict ? '路径已占用' : '未配置') : route.enabled && route.published_at ? '已发布' : '草稿';
  const statusClass = available ? 'is-live' : unconfigured ? 'is-missing' : 'is-draft';
  const command = pluginCallCommand(item, route);
  const action = unconfigured && route.conflict
    ? ''
    : unconfigured && can('api.write')
      ? `<button type="button" class="secondary" data-plugin-route="${esc(route.path)}" data-plugin-name="${esc(item.name)}" data-plugin-method="${esc(route.method)}" data-plugin-auth="${esc(route.auth_mode)}" data-plugin-title="${esc(route.name)}">创建接口</button>`
      : `<button type="button" class="secondary" data-copy-call="${esc(command)}">复制命令</button>`;
  return `<article class="plugin-route ${statusClass}">
    <div class="plugin-route-main"><div class="plugin-route-identity"><span class="plugin-method">${esc(route.method || 'GET')}</span><code>${esc(route.path)}</code></div><div class="plugin-route-meta"><span class="plugin-route-status">${label}</span><span>${esc(authLabel(route.auth_mode))}</span>${!available && !unconfigured && !item.enabled ? '<span>插件未启用</span>' : ''}</div></div>
    <div class="plugin-route-call"><pre class="plugin-call-example">${esc(command)}</pre>${action}</div>
    ${!available ? `<p class="plugin-route-note">${unconfigured ? route.conflict ? '已有其他 API 占用同一方法和路径，请先处理冲突。' : '先创建并发布此接口，命令才可调用。' : !item.enabled ? '先启用插件，命令才可调用。' : '先发布接口，命令才可调用。'}</p>` : ''}
    ${route.auth_mode === 'hmac' ? '<p class="plugin-route-note">HMAC 鉴权需按接口配置计算并添加签名请求头。</p>' : ''}
  </article>`;
}

function authLabel(mode) {
  return ({api_key:'API Key', jwt:'JWT', hmac:'HMAC', none:'免鉴权'})[mode || 'none'] || mode;
}

function renderPluginLibrary(entries) {
  if (!entries.length) return '<div class="plugin-empty">插件库为空。请将插件包放入服务器配置的插件库目录。</div>';
  return entries.map(entry => `<article class="plugin-library-entry"><div><strong>${esc(entry.name)}</strong><span class="small">v${esc(entry.version)} · ${esc(entry.runtime)} · SHA-256 ${esc((entry.checksum || '').slice(0, 16))}…</span></div><div>${entry.installed ? '<span class="plugin-state is-on">已安装</span>' : can('plugin.manage') ? `<button class="secondary" data-plugin-library-install data-plugin-library-name="${esc(entry.name)}" data-plugin-library-version="${esc(entry.version)}">安装</button>` : '<span class="small">无法安装</span>'}</div></article>`).join('');
}
async function publishPluginLibrary(id) {
  if (!confirm('确定将此插件版本加入本地插件库？')) return;
  try { await api('/admin/v1/plugin-library', {method:'POST',body:JSON.stringify({plugin_id:id})}); notice('已加入插件库', true); renderPlugins(); } catch (error) { notice(error.message); }
}

async function installPluginLibrary(name, version) {
  if (!confirm(`确定安装插件 ${name} v${version}？安装后仍需手动启用。`)) return;
  try { await api(`/admin/v1/plugin-library/install?name=${encodeURIComponent(name)}&version=${encodeURIComponent(version)}`, {method:'POST'}); notice('插件库安装成功，请启用插件', true); renderPlugins(); } catch (error) { notice(error.message); }
}

function pluginRow(item, library = []) {
  const action = item.enabled ? `<button class="secondary" data-plugin-status="${esc(item.id)}" data-enabled="false">禁用</button>` : `<button data-plugin-status="${esc(item.id)}" data-enabled="true">启用</button>`;
  const inLibrary = library.some(entry => entry.name === item.name && entry.version === item.version);
  const publish = can('plugin.manage') && !inLibrary ? `<button class="secondary" data-plugin-library-publish="${esc(item.id)}">加入插件库</button>` : (inLibrary ? '<span class="small">已入库</span>' : '');
  const uninstall = can('plugin.manage') ? `<button class="danger" data-plugin-uninstall="${esc(item.id)}" data-plugin-name="${esc(item.name)}" data-plugin-version="${esc(item.version)}" data-enabled="${item.enabled ? 'true' : 'false'}">卸载</button>` : '';
  return `<div class="plugin-version"><div class="plugin-version-info"><div class="plugin-version-title"><strong>${esc(item.name)}</strong><span class="plugin-state ${item.enabled ? 'is-on' : ''}">${item.enabled ? '运行中' : '未启用'}</span></div><div class="plugin-version-meta"><span>v${esc(item.version)}</span><span>${esc(item.runtime)}</span><span>WASM 插件</span><code title="SHA-256: ${esc(item.checksum || '')}">${esc((item.checksum || '').slice(0, 12))}…</code></div></div><div class="plugin-version-actions">${can('plugin.manage') ? action + publish + uninstall : ''}</div></div>`;
}

async function uploadPlugin(event) {
  event.preventDefault();
  const form = event.currentTarget;
  const headers = {}; if (state.token) headers.Authorization = `Bearer ${state.token}`;
  try {
    const response = await fetch('/admin/v1/plugins', {method:'POST', headers, body:new FormData(form)});
    const body = await response.json().catch(() => ({}));
    if (response.status === 401) { logout(); throw new Error('登录已过期'); }
    if (!response.ok) throw new Error(body.error || `请求失败：${response.status}`);
    notice(`插件 ${body.name || body.id || ''} 上传成功，请启用后使用`, true); renderPlugins();
  } catch (error) { notice(error.message); }
}

async function setPluginStatus(id, enabled) {
  try { await api(`/admin/v1/plugins/${encodeURIComponent(id)}/status`, {method:'PUT', body:JSON.stringify({enabled})}); notice(enabled ? '插件已启用' : '插件已禁用', true); renderPlugins(); } catch (error) { notice(error.message); }
}

async function uninstallPlugin(id, name, version, enabled) {
  const routeWarning = '关联的 API 路由不会自动删除；卸载后请下线或修改这些路由。';
  const message = enabled
    ? `确定卸载插件 ${name} v${version}？\n\n系统会先停用插件，再删除插件文件和管理记录。\n${routeWarning}\n\n此操作不可恢复。`
    : `确定卸载插件 ${name} v${version}？\n\n插件文件和管理记录将被删除。\n${routeWarning}\n\n此操作不可恢复。`;
  if (!confirm(message)) return;
  try {
    if (enabled) {
      await api(`/admin/v1/plugins/${encodeURIComponent(id)}/status`, {method:'PUT', body:JSON.stringify({enabled:false})});
    }
    await api(`/admin/v1/plugins/${encodeURIComponent(id)}`, {method:'DELETE'});
    notice(`插件 ${name} v${version} 已卸载`, true);
    renderPlugins();
  } catch (error) {
    notice(enabled ? `卸载失败：${error.message}；插件可能已停用，请检查状态后重试` : `卸载失败：${error.message}`);
    renderPlugins();
  }
}

$('#login-form').onsubmit = async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  notice('', false, 'auth');
  try {
    await withSubmitting(form, '登录中…', () => login($('#email').value, $('#password').value));
  } catch (error) { notice(authErrorMessage(error), false, 'auth'); }
};
$('#bootstrap-form').onsubmit = async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  notice('', false, 'auth');
  try {
    await withSubmitting(form, '初始化中…', () => bootstrap($('#bootstrap-email').value, $('#bootstrap-password').value, $('#bootstrap-token').value));
    setBootstrapAvailable(false, true);
    notice('初始化成功，请使用管理员账号登录', true, 'auth');
  } catch (error) {
    await refreshBootstrapAvailability();
    notice(authErrorMessage(error), false, 'auth');
  }
};
$('#logout').onclick = logout;
$('#refresh').onclick = renderPage;
$$('#nav button').forEach((button) => button.onclick = () => { if (button.classList.contains('hidden')) return; state.page = button.dataset.page; renderPage(); });
hydrateSession();
