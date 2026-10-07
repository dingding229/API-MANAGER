let pendingSetupKey = '';
const setupFragment = location.hash.match(/^#setup=([0-9a-f]{64})$/);
if (setupFragment) { pendingSetupKey = setupFragment[1]; history.replaceState(null, '', location.pathname + location.search); }
sessionStorage.removeItem('api_manager_key');
let legacySession = sessionStorage.getItem('api_manager_session') || '';
sessionStorage.removeItem('api_manager_session');
let authEpoch=0;
const state = { user: null, permissions: [], callOrigin: '', page: 'overview', cache: {}, recoveryEnabled: false };
const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const esc = (value) => String(value ?? '').replace(/[&<>'"]/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const can = (permission) => state.permissions.includes('*') || state.permissions.includes(permission);

// 展示名称不参与鉴权或提交；未知的自定义权限保留原始代码。
const permissionLabels = {
  '*': '全部权限',
  'api.read': '查看接口',
  'api.test': '测试已展示接口',
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
  'observability.read': '查看运行指标、日志、链路与告警',
  'observability.manage': '确认和管理运行告警',
  'observability.logs.clear': '清理全部应用日志',
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
const permissionLabel = (code) => state.cache.permissionNames?.[code] || permissionLabels[code] || code;
const roleLabel = (name) => state.cache.roleNames?.[name] || roleLabels[name] || name;
const roleDescription = (role) => role.description || roleDescriptions[role.name] || '';
function permissionChecklist(permissions, selected = []) {
  const selectedCodes = new Set(selected);
  const categories = {api: '接口管理', credential: '调用凭证', plugin: '插件管理', user: '用户管理', audit: '审计日志', observability: '运行观测'};
  const groups = new Map();
  for (const permission of permissions) {
    const category = permission.code.split('.')[0]; const name = categories[category] || '其他权限';
    if (!groups.has(name)) groups.set(name, []); groups.get(name).push(permission);
  }
  const total = permissions.length; const checked = permissions.filter(p => selectedCodes.has(p.code)).length;
  return `<div class="permission-picker" data-permission-picker><div class="permission-picker-toolbar"><strong>可授予权限</strong><span class="small" data-permission-count role="status" aria-live="polite" aria-atomic="true">${checked}/${total} 已选择</span><span class="permission-picker-actions"><button type="button" class="link" data-permission-all>全选</button><button type="button" class="link" data-permission-none>清空</button></span></div><div class="permission-groups" role="group" aria-label="选择权限">${[...groups].map(([name, entries]) => `<fieldset class="permission-group"><legend>${esc(name)}</legend><div class="permission-options">${entries.map(p => `<label class="permission-option" title="${esc(p.code)}"><input type="checkbox" name="permission" value="${esc(p.code)}" ${selectedCodes.has(p.code) ? 'checked' : ''}><span>${esc(permissionLabel(p.code))}</span></label>`).join('')}</div></fieldset>`).join('')}</div></div>`;
}
function bindPermissionPicker(root) {
  const picker = root?.querySelector('[data-permission-picker]'); if (!picker) return;
  const boxes = () => [...picker.querySelectorAll('input[name="permission"]')];
  const update = () => { const all=boxes(); const count=all.filter(input=>input.checked).length; const node=picker.querySelector('[data-permission-count]'); if(node) node.textContent=`${count}/${all.length} 已选择`; };
  picker.querySelector('[data-permission-all]')?.addEventListener('click',()=>{boxes().forEach(input=>{input.checked=true});update()});
  picker.querySelector('[data-permission-none]')?.addEventListener('click',()=>{boxes().forEach(input=>{input.checked=false});update()});
  boxes().forEach(input=>input.addEventListener('change',update)); update();
}

function rolePermissionsSummary(codes = []) {
  if (!codes.length) return '<span class="role-permission-count">未分配权限</span>';
  if (codes.includes('*')) return '<span class="badge">全部权限</span>';
  return `<details class="role-permission-details"><summary>${codes.length} 项权限 <span>查看详情</span></summary><div class="role-permission-list">${codes.map(code => `<span class="badge" title="${esc(code)}">${esc(permissionLabel(code))}</span>`).join('')}</div></details>`;
}

async function api(path, options = {}) {
  const requestEpoch=authEpoch;
  const headers = {'Content-Type': 'application/json', ...(options.headers || {})};
  headers['X-API-Request'] = '1';
  const response = await fetch(path, {...options, headers, credentials:'same-origin', cache:'no-store',signal:options.signal||((path.startsWith('/auth/')||path.startsWith('/test/'))?AbortSignal.timeout(10000):undefined)});
  const body = await response.json().catch(() => ({}));
  if (response.status === 401 && state.user && requestEpoch===authEpoch) { clearSession(); const expired=new Error('登录已过期，请重新登录');expired.status=401;throw expired; }
  if (!response.ok) {
    const error = new Error(body.error || `请求失败：${response.status}`);
    error.code = body.code || ''; error.status = response.status;
    throw error;
  }
  return body;
}

function notice(message, ok = false, target = '') {
  const loginVisible = !$('#login-view')?.classList.contains('hidden');
  const node = target === 'auth' || (!target && loginVisible) ? $('#auth-message') : $('#message');
  if (!node) return;
  node.textContent = message || '';
  node.className = `message${ok ? ' ok' : ''}`;
  node.setAttribute('role', ok ? 'status' : 'alert');
  if (message && ok) setTimeout(() => { if (node.textContent === message) node.textContent = ''; }, 6000);
}

async function withSubmitting(form, pendingText, action) {
  if (form._submitting) return;
  const button = form.querySelector('button[type="submit"], button:not([type])');
  form._submitting = true; form.setAttribute('aria-busy', 'true');
  try { return await withAction(button, pendingText, action); }
  finally { form._submitting = false; form.removeAttribute('aria-busy'); }
}

async function withAction(button, pendingText, action) {
  if (button?.disabled) return;
  const originalText = button?.textContent || '';
  if (button) { button.disabled = true; button.textContent = pendingText; button.setAttribute('aria-busy', 'true'); }
  try { return await action(); }
  finally { if (button) { button.disabled = false; button.textContent = originalText; button.removeAttribute('aria-busy'); } }
}

function restrictForm(form, permission) {
  if (!form || can(permission)) return;
  form.querySelectorAll('input, select, textarea, button').forEach(control => { control.disabled = true; });
  const hint = document.createElement('p'); hint.className = 'read-only-note';
  hint.textContent = '当前账号仅可查看，请联系管理员获取操作权限。';
  form.before(hint);
}

function bindModalKeyboard(modal, close) {
  modal.onkeydown = event => {
    if (event.key === 'Escape') { event.preventDefault(); close(); }
    if (event.key !== 'Tab') return;
    const controls = [...modal.querySelectorAll('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href]')];
    const first = controls[0], last = controls.at(-1);
    if (!first) { event.preventDefault(); return; }
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
}

function authErrorMessage(error) {
  return error?.message === 'invalid credentials' ? '用户名或密码不正确' : (error?.message || '请求失败');
}

function showConsole() {
  if(!can('*')&&!can('api.read')&&!can('user.read')){location.replace('/account');return;}
  $('#auth-loading')?.classList.add('hidden');
  void loadVersion(true);
  $('#login-view').classList.add('hidden');
  $('#console-view').classList.remove('hidden');
  $('#current-user').textContent = `${state.user?.username || ''} · ${(state.user?.roles || [state.user?.role]).filter(Boolean).map(roleLabel).join('、')}`;
  $$('#nav button[data-permission]').forEach((button) => button.classList.toggle('hidden', !can(button.dataset.permission)));
  const superAdmin=(state.user?.roles||[state.user?.role]).includes('super_admin');
  $$('#nav [data-super-admin]').forEach(button=>button.classList.toggle('hidden',!superAdmin));
  renderPage();
}

function showLogin() {
  $('#auth-loading')?.classList.add('hidden');
  $('#login-view').classList.remove('hidden');
  $('#console-view').classList.add('hidden');
}

function clearSession() {
  authEpoch++;legacySession='';
  closeProfileModal(true); closeRoleModal(true); closeLogCleanupModal(true);
  $$('.cache-dialog').forEach(dialog=>{dialog.close();dialog.remove()});
  state.user = null; state.permissions = [];
  sessionStorage.removeItem('api_manager_session');
  showLogin();
}
const authEvents = typeof BroadcastChannel === 'function' ? new BroadcastChannel('api-manager-auth') : null;
function broadcastAuth() { authEvents?.postMessage('changed'); }
async function logout() {
  try { await api('/auth/v1/logout',{method:'POST'}); clearSession(); broadcastAuth(); }
  catch(error) { if(error.status===401) { clearSession(); broadcastAuth(); } else notice('退出未完成，请稍后重试'); }
}
async function login(username,password) {
  authEpoch++;
  const options=await api('/account/v1/options').catch(error=>{if(error.status===503)return {};throw error;});
  if(options.turnstile){location.assign('/account?return=admin');return;}
  const result=await api('/auth/v1/login',{method:'POST',body:JSON.stringify({username:username.trim(),password})});
  if(result.mfa_required){$('#password').value='';location.assign('/account?mfa=1&return=admin');return;}
  const profile=await api('/auth/v1/me').catch(()=>{throw new Error('浏览器未能保存安全登录状态，请使用 HTTPS 网站地址。')});
  state.user=profile.user;state.permissions=profile.permissions||[];state.cache.roleNames=profile.level_names||{};state.cache.permissionNames=profile.permission_names||{};state.page='overview';legacySession='';
  $('#password').value='';broadcastAuth();showConsole();
}
let hydratingSession = false;
let pendingSessionHydration=false;
let initialSessionHydration = true;
const initialLoginControls=[...document.querySelectorAll('#login-form input,#login-form button')];
initialLoginControls.forEach(control=>{control.disabled=true});
async function hydrateSession() {
  if (hydratingSession) { pendingSessionHydration=true;return; } const epoch=authEpoch,first=initialSessionHydration;hydratingSession=true;
  try {
    let result;
    if(initialSessionHydration){initialSessionHydration=false;try{await api('/test/v1/session')}catch(error){if(error.status!==401)throw error}}
    try { result=await api('/auth/v1/me'); }
    catch(error) {
      if(error.status!==401 || !legacySession) throw error;
      await api('/auth/v1/session',{method:'POST',headers:{Authorization:`Bearer ${legacySession}`}});legacySession='';
      result=await api('/auth/v1/me');broadcastAuth();
    }
    if(epoch!==authEpoch) return;
    legacySession='';
    const changed=JSON.stringify([state.user,state.permissions])!==JSON.stringify([result.user,result.permissions||[]]);
    if(state.user?.id && state.user.id!==result.user.id) { closeProfileModal(true);closeRoleModal(true);closeLogCleanupModal(true);$$('.cache-dialog').forEach(dialog=>{dialog.close();dialog.remove()});state.cache={}; }
    state.user=result.user;state.permissions=result.permissions||[];state.cache.roleNames=result.level_names||{};state.cache.permissionNames=result.permission_names||{};
    if(changed || $('#console-view').classList.contains('hidden')) showConsole();
  } catch(error) { if(epoch!==authEpoch)return;legacySession='';if(error.status===401) clearSession();else if(!state.user){$('#auth-loading p').textContent='暂时无法连接，请重试。';$('#retry-auth').classList.remove('hidden')}else notice('登录状态暂时无法确认，请稍后刷新'); }
  finally { hydratingSession=false;if(first)initialLoginControls.forEach(control=>{control.disabled=false});if(pendingSessionHydration){pendingSessionHydration=false;void hydrateSession()} }
}
authEvents?.addEventListener('message',()=>{authEpoch++;void hydrateSession()});
window.addEventListener('focus',()=>void hydrateSession());
document.addEventListener('visibilitychange',()=>{if(!document.hidden)void hydrateSession()});
setInterval(()=>{if(!document.hidden)void hydrateSession()},60000);

function renderPage() {
  // Each view owns its node. Late responses from the previous view can only
  // update a detached node, never overwrite the active view (including iframes).
  const oldPage = $('#page');
  const newPage = document.createElement('div'); newPage.id = 'page';
  oldPage.replaceWith(newPage);
  notice('');
  const titles = {overview:'总览', apis:'接口管理', credentials:'调用凭证', users:'用户管理', roles:'角色与权限', plugins:'插件', observability:'运行观测', audit:'审计日志',settings:'网站设置',accounts:'用户余额',plans:'套餐管理',calllogs:'调用日志',authentication:'注册与登录'};
  $('#page-title').textContent = titles[state.page] || '总览';
  $$('#nav button').forEach((button) => {
    const active = button.dataset.page === state.page; button.classList.toggle('active', active);
    if (active) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current');
  });
  const renderers = {overview: renderOverview, apis: renderAPIs, credentials: renderCredentials, users: renderUsers, roles: renderRoles, plugins: renderPlugins, observability: renderObservability, audit: renderAuditLogs, settings: renderSiteSettings,accounts:renderBalanceAdmin,plans:renderPlanAdmin,calllogs:renderCallLogsAdmin,authentication:renderAuthentication};
  return renderers[state.page]();
}

function overviewStat(label,value,hint='') {
  return `<div class="stat"><span class="small">${esc(label)}</span><div class="number">${value === undefined ? '—' : esc(value)}</div>${hint ? `<span class="metric-caption">${esc(hint)}</span>` : ''}</div>`;
}
function overviewSeries(points, timestamp) {
  const end = Math.floor(new Date(timestamp).getTime() / 60000) * 60000;
  const minutes = new Map(points.map(point => [Math.floor(new Date(point.timestamp).getTime()/60000)*60000, point]));
  return Array.from({length:60}, (_,index) => {
    const time=end-(59-index)*60000;
    return minutes.get(time) || {timestamp:new Date(time).toISOString(),requests:0};
  });
}
let versionRequest = 0;
async function loadVersion(check = false) {
  const actor = state.user?.id, run = ++versionRequest;
  const current = $('#current-version'), status = $('#update-status');
  if (!current || !actor) return;
  if (check && status) status.textContent = '正在检查…';
  try {
    const data = await api(check ? '/admin/v1/version/check' : '/admin/v1/version', check ? {method:'POST'} : {});
    if (run !== versionRequest || actor !== state.user?.id) return;
    current.textContent = `版本 ${data.current || '未知'}`;
    current.title = data.revision ? `构建 ${data.revision}` : '';
    if (status) {
      status.textContent = data.latest ? `${data.message} 最新：${data.latest}` : data.message || '尚未检查更新。';
      status.title = data.checked_at ? `检查时间：${new Date(data.checked_at).toLocaleString()}；检查结果会缓存，稍后可再检查。` : '';
    }
  } catch (error) { if (run === versionRequest && actor === state.user?.id && status) status.textContent = '版本信息暂时不可用。'; }
}
$('#check-update')?.addEventListener('click', event => void withAction(event.currentTarget,'检查中…',()=>loadVersion(true)));

async function renderOverview() {
  if (state.page !== 'overview') return;
  const page=$('#page'); page.innerHTML='<div class="empty">加载总览…</div>';
  try {
    const [data,ready]=await Promise.all([api('/admin/v1/overview'),api('/health/ready')]);
    if (!page.isConnected) return;
    const resources=data.resources||{}, metrics=data.metrics;
    const resourceCards=[overviewStat('接口数量',resources.apis),overviewStat('已发布接口',resources.published),overviewStat('有效调用凭证',resources.active_credentials),overviewStat('已启用插件',resources.enabled_plugins),overviewStat('服务状态',ready.status==='ready'?'运行正常':'待检查')];
    if (resources.users!==undefined) resourceCards.push(overviewStat('用户数量',resources.users));
    const successRate=metrics&&metrics.gateway_requests_total ? ((1-metrics.gateway_error_rate)*100).toFixed(1)+'%' : '暂无调用';
    const requestCards=metrics ? [overviewStat('业务请求总数',formatMetric(metrics.gateway_requests_total),'本次运行累计'),overviewStat('最近 5 分钟请求',formatMetric(data.recent_requests),'仅统计接口调用'),overviewStat('业务成功率',successRate,'成功完成的调用'),overviewStat('平均响应时间',formatMetric(metrics.average_latency_ms,1)+' ms'),overviewStat('P95 响应时间',formatMetric(metrics.p95_latency_ms,1)+' ms'),overviewStat('业务失败请求',formatMetric(metrics.gateway_errors_total),'含 4xx 与 5xx')] : [];
    const roles=(state.user?.roles||[state.user?.role]).filter(Boolean).map(roleLabel).join('、');
    page.innerHTML=`<div class="overview-page"><section class="overview-stats" aria-label="资源与服务">${resourceCards.join('')}</section>${metrics ? `<section class="overview-stats" aria-label="接口请求统计">${requestCards.join('')}</section><section class="card overview-traffic"><div class="overview-section-head"><div><h2>请求趋势</h2><p class="small">最近一小时业务 API 调用；重新启动后会重新统计。</p></div><span class="small">已运行 ${esc(formatUptime(metrics.uptime_seconds))}</span></div>${observationChart(overviewSeries(metrics.series||[],data.timestamp),'requests','每分钟请求数','#365cff')}<div class="overview-runtime"><span>HTTP 请求总数 <strong>${esc(formatMetric(metrics.http_requests_total))}</strong></span><span>鉴权失败 <strong>${esc(formatMetric(metrics.auth_failures))}</strong></span><span>限流次数 <strong>${esc(formatMetric(metrics.rate_limit_hits))}</strong></span><span>当前 HTTP 并发 <strong>${esc(formatMetric(metrics.inflight))}</strong></span></div></section>` : '<p class="small">当前账号没有运行统计查看权限。</p>'}<section class="overview-panels"><div class="card overview-panel"><h2>快速操作</h2><p class="small">配置接口、创建调用凭证，再按需发布。管理功能由当前账号的角色权限控制。</p><div class="actions">${can('api.read')?'<button data-go="apis">管理接口</button>':''}${can('credential.read')?'<button class="secondary" data-go="credentials">调用凭证</button>':''}${can('observability.read')?'<button class="secondary" data-go="observability">运行观测</button>':''}</div></div><div class="card overview-panel"><h2>当前账号</h2><dl class="overview-account"><div><dt>用户名</dt><dd>${esc(state.user?.username)}</dd></div><div><dt>邮箱</dt><dd>${esc(state.user?.email||'未绑定')}</dd></div><div><dt>角色</dt><dd>${esc(roles)}</dd></div><div><dt>权限</dt><dd>${can('*')?'全部权限':state.permissions.length+' 项'}</dd></div></dl><button type="button" class="secondary" data-overview-account>账号设置</button></div></section></div>`;
    page.querySelectorAll('[data-go]').forEach(button=>button.onclick=()=>{state.page=button.dataset.go;renderPage()});
    page.querySelector('[data-overview-account]').onclick=()=>openProfileModal(state.user);
  } catch(error){if(page.isConnected)page.innerHTML=`<div class="empty" role="alert">${esc(error.message)}</div>`}
}

async function renderAPIs() {
  if (state.page !== 'apis') return;
  const page = $('#page'); if (!page.isConnected) return; page.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const apis = await api('/admin/v1/apis');
    if (!page.isConnected) return; page.innerHTML = `<div class="split api-management-grid management-grid"><div class="table-wrap api-table-wrap" role="region" aria-label="已配置接口" tabindex="0"><div class="toolbar table-toolbar"><h2>已配置接口</h2><button class="secondary" id="openapi">导出 OpenAPI</button></div><table class="api-table"><colgroup><col class="api-name-column"><col class="api-route-column"><col class="api-auth-column"><col class="api-status-column"><col class="api-actions-column"></colgroup><thead><tr><th scope="col">名称</th><th scope="col">路由</th><th scope="col" class="api-control-heading">鉴权</th><th scope="col" class="api-control-heading">状态</th><th scope="col" class="api-actions-heading">操作</th></tr></thead><tbody>${apis.length ? apis.map(apiRow).join('') : '<tr><td colspan="5"><div class="empty">暂无接口</div></td></tr>'}</tbody></table></div><div class="card"><h2 id="api-form-title">创建接口</h2>${apiForm()}<hr class="section-line"><details class="import-openapi"><summary>导入 OpenAPI 3.x 文档</summary>${openAPIImportForm()}</details></div></div>`;
    $('#openapi').onclick = async () => { try { const document = await api('/admin/v1/openapi.json'); const blob = new Blob([JSON.stringify(document, null, 2)], {type:'application/json'}); const url = URL.createObjectURL(blob); window.open(url, '_blank', 'noopener,noreferrer'); setTimeout(() => URL.revokeObjectURL(url), 30000); } catch(error) { notice(error.message); } };
    $('#api-form').onsubmit = createAPI; bindCacheFields($('#api-form'));
    restrictForm($('#api-form'), 'api.write'); restrictForm($('#openapi-import-form'), 'api.write');
    $('#api-form').elements.path.oninput = (event) => event.target.setCustomValidity('');
    $('#openapi-import-form').onsubmit = importOpenAPI;
    $$('#page [data-action]').forEach((button) => button.onclick = () => apiAction(button.dataset.action, button.dataset.id));
    $$('#page [data-edit-api]').forEach((button) => button.onclick = () => editAPI(button.dataset.editApi, apis));
  } catch (error) { if (!page.isConnected) return; page.innerHTML = `<div class="empty" role="alert">${esc(error.message)}</div>`; }
}

function apiRow(item) {
  const label = (action) => esc(`${action}接口 ${item.name}`);
  const publication = item.enabled
    ? `<button type="button" class="secondary api-row-button" data-action="unpublish" data-id="${esc(item.id)}" aria-label="${label('下线')}">下线</button>`
    : `<button type="button" class="api-row-button api-row-publish" data-action="publish" data-id="${esc(item.id)}" aria-label="${label('发布')}">发布</button>`;
  const source = item.plugin ? `插件：${item.plugin}` : item.upstream_url ? `上游：${item.upstream_url}` : '静态响应';
  const buttons = [
    can('api.write') ? `<button type="button" class="secondary api-row-button" data-edit-api="${esc(item.id)}" aria-label="${label('编辑')}">编辑</button>` : '',
    can('api.publish') ? publication : '',
    can('api.delete') ? `<button type="button" class="danger api-row-button" data-action="delete" data-id="${esc(item.id)}" aria-label="${label('删除')}">删除</button>` : '',
  ].filter(Boolean).join('');
  return `<tr>
    <td><div class="api-row-identity"><strong>${esc(item.name)}</strong><span class="small">${esc(source)}</span></div></td>
    <td class="api-row-route"><code>${esc((item.methods?.length ? item.methods : [item.method]).join(' / '))} ${esc(item.path)}</code></td>
    <td class="api-control-cell"><span class="api-auth-tag ${item.auth_mode === 'none' ? 'is-public' : ''}">${esc(authLabel(item.auth_mode))}</span></td>
    <td class="api-control-cell"><span class="api-status-tag ${item.enabled ? 'is-live' : 'is-draft'}"><span class="api-status-dot" aria-hidden="true"></span>${item.enabled ? '已发布' : '草稿'}</span></td>
    <td class="api-actions-cell"><div class="api-row-actions" role="group" aria-label="${esc(`${item.name}的操作`)}">${buttons || '<span class="api-no-actions">—</span>'}</div></td>
  </tr>`;
}

function selected(value, expected) { return String(value ?? '') === String(expected) ? 'selected' : ''; }
function apiForm(item = {}) {
  const value = (key, fallback = '') => esc(item[key] ?? fallback);
  const jsonValue = (key) => item[key] ? esc(JSON.stringify(item[key], null, 2)) : '';
  const hasUpstream = item.upstream_url || item.upstream_path || item.upstream_auth_ref;
  const hasResilience = Number(item.upstream_timeout_ms || 0) || Number(item.upstream_retries || 0) || Number(item.circuit_breaker_threshold || 0) || (item.circuit_breaker_reset_seconds !== undefined && Number(item.circuit_breaker_reset_seconds) !== 30);
  const hasResponse = item.response_body || Number(item.response_status || 0) || item.request_schema || item.response_schema || item.parameters_schema;
  return `<form id="api-form" class="form-stack api-form" data-api-id="${value('id')}">
    <section class="form-section form-section-primary" aria-labelledby="api-basics-title">
      <div class="form-section-heading"><div><h3 id="api-basics-title">基础信息</h3><p>填写接口名称、地址与调用方式。</p></div><span class="required-note">带 * 为必填</span></div>
      <label class="field"><span class="field-label">名称 <span aria-hidden="true">*</span></span><input name="name" required placeholder="订单查询" value="${value('name')}"></label>
      <div class="method-auth-fields">
        <fieldset class="field method-field" aria-describedby="method-hint"><legend class="field-label">请求方法 <span aria-hidden="true">*</span></legend><div class="method-options">${['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS'].map(method=>`<label class="method-option"><input type="checkbox" name="methods" value="${method}" ${(item.methods?.length?item.methods:[item.method||'GET']).includes(method)?'checked':''}><span>${method}</span></label>`).join('')}</div><span id="method-hint" class="field-hint">可以选择多种调用方式。</span><p class="method-error hidden" role="alert" data-method-error></p></fieldset>
        <label class="field"><span class="field-label">鉴权方式</span><select name="auth_mode" aria-describedby="auth-hint"><option value="api_key" ${item.auth_mode==='none'?'':'selected'}>KEY · 需要调用凭证</option><option value="none" ${item.auth_mode==='none'?'selected':''}>无需验证 · 公开访问</option></select><span id="auth-hint" class="field-hint">选择无需验证时，任何人都可以调用此接口。</span></label>
      </div>
      <label class="field"><span class="field-label">每次成功调用价格（元）</span><input name="call_price" inputmode="decimal" value="${Number(item.price_micros||0)/1000000}" pattern="[0-9]+(\.[0-9]{1,6})?" required><span class="field-hint">填 0 为免费。付费接口须选择 KEY 认证，且密钥须绑定用户。</span></label>
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
    <details class="form-section form-section-collapsible" ${item.plugin_cache?.enabled ? 'open' : ''}>
      <summary><span><strong>插件数据缓存</strong><small>将查询结果保存在数据库，减少重复计算</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body">
        <label class="checkbox-field"><input type="checkbox" name="cache_enabled" ${item.plugin_cache?.enabled?'checked':''}><span>启用此接口的插件结果缓存</span></label>
        <div class="grid-2"><label class="field"><span class="field-label">缓存有效期（秒）</span><input type="number" name="cache_ttl" min="1" max="604800" value="${Number(item.plugin_cache?.ttl_seconds)||300}" required><span class="field-hint">过期后重新执行插件，最长 7 天。</span></label><label class="field"><span class="field-label">最多缓存条目</span><input type="number" name="cache_limit" min="1" max="10000" value="${Number(item.plugin_cache?.max_entries)||1000}" required><span class="field-hint">达到上限时淘汰较早写入的结果。</span></label></div>
        <label class="checkbox-field"><input type="checkbox" name="cache_post" ${item.plugin_cache?.cache_post?'checked':''}><span>同时缓存 POST 查询结果（仅适合只读查询）</span></label>
        <p class="small">仅适用于插件接口。默认缓存 GET/HEAD 的成功结果，鉴权和配额仍按每次请求执行；不同调用密钥、请求参数不会混用缓存。浏览器和 CDN 仍不缓存业务响应。</p>
        ${item.id && item.plugin ? '<button type="button" class="secondary" data-cache-manage>查看与清理缓存</button>':''}
      </div>
    </details>
    <details class="form-section form-section-collapsible" ${hasResponse ? 'open' : ''}>
      <summary><span><strong>响应与校验</strong><small>静态响应和 JSON Schema 均为可选</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body"><label class="field"><span class="field-label">响应状态码</span><input type="number" min="0" max="599" name="response_status" value="${value('response_status',item.response_body ? '200' : '0')}"><span class="field-hint">填 0 使用默认行为。</span></label><label class="field"><span class="field-label">静态响应 JSON</span><textarea name="response_body" placeholder='{"message":"ok"}'>${value('response_body')}</textarea></label><div class="grid-2"><label class="field"><span class="field-label">请求 JSON Schema</span><textarea name="request_schema" class="schema-input" placeholder='{"type":"object"}'>${jsonValue('request_schema')}</textarea></label><label class="field"><span class="field-label">响应 JSON Schema</span><textarea name="response_schema" class="schema-input" placeholder='{"type":"object"}'>${jsonValue('response_schema')}</textarea></label></div><label class="field"><span class="field-label">参数 JSON Schema</span><textarea name="parameters_schema" class="schema-input" placeholder='{"type":"object","properties":{"query":{"type":"object"}}}'>${jsonValue('parameters_schema')}</textarea><span class="field-hint">根对象可包含 query、path、header。</span></label></div>
    </details>
    <details class="form-section form-section-collapsible" ${item.public_visible ? 'open' : ''}>
      <summary><span><strong>公开目录</strong><small>选择是否展示在网站首页</small></span><span class="summary-chevron" aria-hidden="true">⌄</span></summary>
      <div class="form-section-body">
        <label class="checkbox-field"><input type="checkbox" name="public_visible" ${item.public_visible ? 'checked' : ''}><span>公开展示并允许有测试权限的登录用户在线测试</span></label>
        <p class="small">仅公开、启用且已发布的接口可测试。测试会真实调用，可能消耗额度或修改数据。</p>
        <label class="field"><span class="field-label">公开标题</span><input name="public_title" maxlength="120" value="${value('public_title')}" placeholder="面向调用方的接口名称"></label>
        <label class="field"><span class="field-label">公开分类</span><input name="public_category" maxlength="48" value="${value('public_category')}" placeholder="例如：数据查询"></label>
        <label class="field"><span class="field-label">公开说明</span><textarea name="public_summary" maxlength="600" placeholder="仅填写可公开的用途说明，不要写内部地址、密码或 KEY">${value('public_summary')}</textarea></label>
        <p class="small">只有已发布、已启用且打开此开关的接口才会显示并允许在线测试。测试可能消耗余额、套餐次数或修改业务数据。私有说明、上游配置、真实响应及凭据不会输出。隐藏目录不等于关闭接口。</p>
      </div>
    </details>

    <div class="form-submit"><span class="small">保存后仍需单独发布接口，草稿不会立即对外生效。</span><div class="actions"><button type="submit">${item.id ? '保存接口修改' : '创建并保存草稿'}</button>${item.id ? '<button type="button" class="secondary" id="cancel-api-edit">取消编辑</button>' : ''}</div></div>
  </form>`;
}
function apiFormData(form) {
  const data = Object.fromEntries(new FormData(form).entries());
  data.methods = new FormData(form).getAll('methods');
  const methodField=form.querySelector('.method-field'); const methodError=form.querySelector('[data-method-error]');
  if (!data.methods.length) {
    methodField.setAttribute('aria-invalid','true');methodError.textContent='请至少选择一种请求方法。';methodError.classList.remove('hidden');form.querySelector('[name="methods"]').focus();
    throw new Error('请至少选择一种请求方法');
  }
  methodField.removeAttribute('aria-invalid');methodError.textContent='';methodError.classList.add('hidden');
  data.method = data.methods[0];
  data.price_micros=moneyMicros(data.call_price);delete data.call_price;
  for (const key of ['request_schema', 'response_schema', 'parameters_schema']) { if (data[key]?.trim()) data[key] = JSON.parse(data[key]); else delete data[key]; }
  for (const key of ['response_status', 'upstream_timeout_ms', 'upstream_retries', 'circuit_breaker_threshold', 'circuit_breaker_reset_seconds']) data[key] = Number(data[key] || 0);
  data.strip_path = form.elements.strip_path.checked;
  data.public_visible = form.elements.public_visible.checked;
  data.public_test_enabled=data.public_visible;
  data.plugin_cache = {enabled:form.elements.cache_enabled.checked,ttl_seconds:Number(data.cache_ttl||form.elements.cache_ttl.value),max_entries:Number(data.cache_limit||form.elements.cache_limit.value),cache_post:form.elements.cache_enabled.checked && form.elements.cache_post.checked};
  for(const key of ['cache_enabled','cache_ttl','cache_limit','cache_post'])delete data[key];
  return data;
}

function routePathError(path) {
  if (!path.startsWith('/')) return '访问路径必须以 / 开头，例如 /api/example/v1/status；插件名称请填在“插件名称”字段。';
  if (path.includes('//')) return '访问路径不能包含连续的 //；请只填写路径，不要填写 http:// 或 https:// 开头的完整网址。';
  return '';
}

async function createAPI(event) {
  event.preventDefault();
  const form = event.currentTarget; if (form._submitting) return;
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
    await withSubmitting(form, id ? '保存中…' : '创建中…', () => api(id ? `/admin/v1/apis/${encodeURIComponent(id)}` : '/admin/v1/apis', {method:id ? 'PUT' : 'POST', body:JSON.stringify(data)}));
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
    bindCacheFields(card.querySelector('#api-form'), id);
    $('#cancel-api-edit').onclick = () => renderAPIs();
    card.scrollIntoView({behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth', block:'start'});
  } catch (error) { notice(error.message); }
}


function openAPIImportForm() {
  return `<form id="openapi-import-form" class="form-stack"><p class="small">导入会根据每个 path + operation 创建草稿，并从文档的 JSON 请求/响应 Schema 生成校验规则。默认使用文档首个 servers URL，也可在下方覆盖。</p><label>上游 URL（可选覆盖）<input name="upstream_url" placeholder="https://api.example.com"></label><label>路径前缀（可选）<input name="path_prefix" placeholder="/api"></label><label>OpenAPI 3.x 文档（JSON 或 YAML）<textarea name="document" required class="openapi-input" placeholder="openapi: 3.0.3&#10;servers:&#10;  - url: https://api.example.com&#10;paths: {}"></textarea></label><button type="submit" class="secondary">导入为草稿</button></form>`;
}

async function importOpenAPI(event) {
  event.preventDefault();
  if (event.currentTarget._submitting) return;
  const form = new FormData(event.currentTarget);
  const text = String(form.get('document') || '').trim();
  const data = {upstream_url: form.get('upstream_url'), path_prefix: form.get('path_prefix')};
  try {
    try { data.document = JSON.parse(text); }
    catch (_) { data.document_yaml = text; }
    const result = await withSubmitting(event.currentTarget, '导入中…', () => api('/admin/v1/openapi/import', {method:'POST', body:JSON.stringify(data)}));
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


function roleCanBeGranted(role) {
  return can('*') || (role.name !== 'super_admin' && role.name !== 'tenant_admin' && !(role.permissions || []).some(permission => permission === '*' || permission === 'user.manage' || permission === 'observability.logs.clear'));
}

function closeRoleModal(force = false) {
  const modal = $('#role-edit-modal');
  if (!modal || (modal._saving && !force)) return;
  const previous = modal._previousFocus;
  const app = $('#app'); if (app) app.inert = modal._previousInert;
  document.body.style.overflow = modal._previousOverflow;
  modal.remove();
  if (previous?.isConnected) previous.focus();
}

function openRoleModal(user, roles) {
  if ($('#role-edit-modal')?._saving || $('#user-profile-modal')?._saving) return;
  closeRoleModal(); closeProfileModal();
  const currentRoles = new Set(user.roles || [user.role].filter(Boolean));
  const options = roles.filter(role => roleCanBeGranted(role) || currentRoles.has(role.name));
  const modal = document.createElement('div');
  modal.id = 'role-edit-modal'; modal.className = 'modal-backdrop'; modal.setAttribute('role', 'presentation');
  modal._previousFocus = document.activeElement;
  modal._previousInert = $('#app')?.inert || false; modal._previousOverflow = document.body.style.overflow;
  modal.innerHTML = `<section class="modal-card role-modal" role="dialog" aria-modal="true" aria-labelledby="role-modal-title" tabindex="-1"><div class="modal-heading"><div><h2 id="role-modal-title">编辑角色</h2><p class="small">${esc(user.username)} · 选择该用户要拥有的角色。</p></div><button type="button" class="modal-close" aria-label="关闭角色编辑">×</button></div><form id="role-edit-form" class="form-stack"><fieldset class="role-select-fieldset"><legend>用户角色</legend><div class="role-select-list">${options.map(role => `<label class="role-select-option"><input type="checkbox" name="role" value="${esc(role.name)}" ${currentRoles.has(role.name) ? 'checked' : ''}><span><strong>${esc(roleLabel(role.name))}</strong><small>${esc(roleDescription(role))}</small></span></label>`).join('')}</div></fieldset><p id="role-edit-error" class="message" role="alert" aria-live="polite"></p><div class="modal-actions"><button type="button" class="secondary" data-role-modal-cancel>取消</button><button type="submit">保存角色</button></div></form></section>`;
  document.body.appendChild(modal);
  $('#app').inert = true; document.body.style.overflow = 'hidden';
  const close = () => closeRoleModal();
  modal.querySelector('.modal-close').onclick = close;
  modal.querySelector('[data-role-modal-cancel]').onclick = close;
  modal.addEventListener('click', event => { if (event.target === modal) close(); });
  modal.querySelector('#role-edit-form').onsubmit = async event => {
    event.preventDefault(); if (modal._saving) return;
    const form = new FormData(event.currentTarget);
    const selected = form.getAll('role'); const error = modal.querySelector('#role-edit-error');
    if (!selected.length) { error.textContent = '至少保留一个角色，否则用户将无法访问管理功能。'; return; }
    error.textContent = ''; modal._saving = true;
    const fields = [...modal.querySelectorAll('input, button[type="button"]')]; fields.forEach(field => { field.disabled = true; });
    try { await withSubmitting(event.currentTarget, '保存中…', async () => {
      try { await api(`/admin/v1/users/${encodeURIComponent(user.id)}/roles`, {method:'PUT', body:JSON.stringify({roles:selected})}); closeRoleModal(true); notice('角色已更新', true); renderUsers(); }
      catch (caught) { error.textContent = caught.message; }
    }); } finally { modal._saving = false; fields.forEach(field => { field.disabled = false; }); }
  };
  const first = modal.querySelector('input[name="role"]') || modal.querySelector('.modal-close'); first.focus();
  bindModalKeyboard(modal, close);
}

function closeProfileModal(force = false) {
  const modal = $('#user-profile-modal');
  if (!modal || (modal._saving && !force)) return;
  const previous = modal._previousFocus;
  modal.querySelectorAll('input[type="password"], input[data-password-field]').forEach(input => { input.value = ''; });
  const app = $('#app'); if (app) app.inert = modal._previousInert;
  document.body.style.overflow = modal._previousOverflow;
  modal.remove();
  if (previous?.isConnected) previous.focus();
}

function profileErrorMessage(error) {
  return ({current_password_invalid: '当前密码不正确，请重新输入。', profile_forbidden: '没有权限修改此账号。', invalid_profile: '请检查用户名格式与密码长度后重试。', profile_conflict: '用户名已被使用，或账号信息已被修改。请刷新后重试。', user_not_found: '该用户已不存在，请刷新用户列表。', profile_unavailable: '暂时无法保存，请稍后重试。'})[error.code] || error.message;
}

function openProfileModal(user) {
  if (!user || !state.user || $('#user-profile-modal')?._saving) return;
  closeRoleModal(); closeProfileModal();
  const isSelf = user.id === state.user?.id;
 if(isSelf){location.assign("/account");return;}
  const modal = document.createElement('div');
  modal.id = 'user-profile-modal'; modal.className = 'modal-backdrop'; modal.setAttribute('role', 'presentation');
  modal._previousFocus = document.activeElement; modal._previousInert = $('#app')?.inert || false;
  modal._previousOverflow = document.body.style.overflow;
  modal.innerHTML = `<section class="modal-card profile-modal" role="dialog" aria-modal="true" aria-labelledby="profile-modal-title" aria-describedby="profile-impact" tabindex="-1"><div class="modal-heading"><div><h2 id="profile-modal-title">${isSelf ? '账号设置' : '编辑基本信息'}</h2><p class="small">${esc(user.username)} · ${isSelf ? '修改自己的登录信息' : '修改该用户的登录信息'}</p></div><button type="button" class="modal-close" aria-label="关闭基本信息编辑">×</button></div><form id="profile-edit-form" class="form-stack"><label class="field" for="profile-username"><span class="field-label">用户名 <span aria-hidden="true">*</span></span><input id="profile-username" name="username" type="text" required minlength="3" maxlength="254" value="${esc(user.username)}" autocomplete="username" spellcheck="false" aria-describedby="profile-username-hint"><span id="profile-username-hint" class="field-hint">原有用户名可保留；新用户名使用 3–64 位字母、数字、点、下划线或短横线。</span></label><label class="field" for="profile-email"><span class="field-label">邮箱（可选）</span><input id="profile-email" name="email" type="email" maxlength="254" value="${esc(user.email||'')}" autocomplete="email"><span class="field-hint">用于找回密码。未绑定邮箱时，请联系管理员。</span></label>${true ? '<label class="field" for="profile-current-password"><span class="field-label">当前管理员密码 <span aria-hidden="true">*</span></span><input id="profile-current-password" name="current_password" type="password" required maxlength="72" autocomplete="current-password"><span class="field-hint">修改账号信息需确认当前密码。</span></label>' : ''}<div class="field"><label class="field-label" for="profile-new-password">新密码（可选）</label><div class="password-input-row"><input id="profile-new-password" name="password" data-password-field type="password" maxlength="72" autocomplete="new-password" aria-describedby="profile-password-hint"><button type="button" class="secondary" data-toggle-password aria-label="显示新密码和确认密码" aria-pressed="false">显示</button></div><span id="profile-password-hint" class="field-hint">留空保留原密码。新密码为 8–72 个 UTF-8 字节。</span></div><label class="field" for="profile-password-confirm"><span class="field-label">确认新密码</span><input id="profile-password-confirm" name="password_confirm" data-password-field type="password" maxlength="72" autocomplete="new-password"></label><p id="profile-impact" class="profile-impact">${isSelf ? '用户名、邮箱或密码变更成功后，当前账号的全部会话将失效，请使用新信息重新登录。' : '用户名、邮箱或密码变更成功后，该用户的全部已有会话将失效。角色和账号状态保持不变。'}</p><p id="profile-edit-error" class="message" role="alert" aria-live="polite" tabindex="-1"></p><div class="modal-actions"><button type="button" class="secondary" data-profile-modal-cancel>取消</button><button type="submit">保存基本信息</button></div></form></section>`;
  document.body.appendChild(modal);
  $('#app').inert = true; document.body.style.overflow = 'hidden';
  const close = () => closeProfileModal();
  modal.querySelector('.modal-close').onclick = close;
  modal.querySelector('[data-profile-modal-cancel]').onclick = close;
  modal.addEventListener('click', event => { if (event.target === modal) close(); });
  const form = modal.querySelector('#profile-edit-form');
  const extra=document.createElement('section');extra.className='form-stack';extra.innerHTML='<label class="field"><span class="field-label">管理员动态码或恢复码</span><input name="totp_code" autocomplete="one-time-code" maxlength="64"><span class="field-hint">启用双重验证后必填。</span></label><div data-captcha></div><label class="field"><span class="field-label">新邮箱验证码</span><input name="verification_code" maxlength="6"></label><button type="button" class="secondary" data-email-code>发送新邮箱验证码</button>';form.querySelector('.modal-actions').before(extra);
  let profileCaptcha;void mountAccountCaptcha(extra).then(c=>profileCaptcha=c).catch(e=>notice(e.message));
  extra.querySelector('[data-email-code]').onclick=async()=>{try{const cfg=await api('/account/v1/options');if(cfg.turnstile)throw new Error('启用人机验证时，请由用户在用户中心自行修改邮箱。');const v=await api('/account/v1/send-code',{method:'POST',body:JSON.stringify({email:form.elements.email.value,purpose:'change-email'})});form._verification=v.verification_id;notice('验证码已发送至新邮箱',true)}catch(e){notice(e.message)}};
  [...form.elements].filter(element => element.tagName === 'INPUT').forEach(input => input.addEventListener('input', () => {
    input.setCustomValidity('');
    form.elements.password_confirm.setCustomValidity('');
    modal.querySelector('#profile-edit-error').textContent = '';
  }));
  modal.querySelector('[data-toggle-password]').onclick = event => {
    const show = form.elements.password.type === 'password';
    [form.elements.password, form.elements.password_confirm].forEach(input => { input.type = show ? 'text' : 'password'; });
    const button = event.currentTarget; button.textContent = show ? '隐藏' : '显示';
    button.setAttribute('aria-pressed', String(show)); button.setAttribute('aria-label', show ? '隐藏新密码和确认密码' : '显示新密码和确认密码');
  };
  form.onsubmit = async event => {
    event.preventDefault(); if (modal._saving) return;
    const error = modal.querySelector('#profile-edit-error'); error.textContent = '';
    const password = form.elements.password.value;
    const confirmation = form.elements.password_confirm.value;
    const byteLength = new TextEncoder().encode(password).length;
    if (password && (byteLength < 8 || byteLength > 72)) {
      form.elements.password.setCustomValidity('新密码必须为 8–72 个 UTF-8 字节。');
    }
    if (password !== confirmation) form.elements.password_confirm.setCustomValidity('两次输入的新密码不一致。');
    if (!form.reportValidity()) return;
    const data = {username: form.elements.username.value.trim(), email: form.elements.email.value.trim()};
    data.current_password = form.elements.current_password.value;
    data.totp_code=form.elements.totp_code.value;data.turnstile_token=profileCaptcha?.value()||'';data.verification_id=form._verification||'';data.verification_code=form.elements.verification_code.value;
    if (password) data.password = password;
    modal._saving = true;
    const submit = form.querySelector('[type="submit"]'); submit.textContent = '保存中…';
    const controls = [...modal.querySelectorAll('input, button')]; controls.forEach(control => { control.disabled = true; });
    try {
      const result = await api(isSelf ? '/auth/v1/me' : `/admin/v1/users/${encodeURIComponent(user.id)}/profile`, {method:'PUT', body:JSON.stringify(data)});
      profileCaptcha?.reset();closeProfileModal(true);
      if (result.reauthentication_required) {
        clearSession(); broadcastAuth(); $('#username').value = result.user.username; $('#password').value = '';
        notice('信息已更新，请使用新的登录信息重新登录。', true, 'auth'); $('#password').focus();
        return;
      }
      if (isSelf) state.user = result.user;
      notice(result.sessions_revoked ? '信息已更新，该用户的旧会话已失效。' : '信息未发生变化。', true);
      if (state.page === 'users') renderUsers();
    } catch (caught) {
      if (modal.isConnected) { profileCaptcha?.reset();error.textContent = profileErrorMessage(caught); error.focus(); }
    } finally {
      modal._saving = false;
      if (modal.isConnected) { controls.forEach(control => { control.disabled = false; }); submit.textContent = '保存基本信息'; }
    }
  };
  form.elements.username.focus(); form.elements.username.select();
  bindModalKeyboard(modal, close);
}

function userActions(user, roles) {
  const isSelf = user.id === state.user?.id;
 if(isSelf){location.assign("/account");return;}
  const targetRoles = user.roles?.length ? user.roles : [user.role].filter(Boolean);
  const canManageTarget = can('user.manage') && (can('*') || (!isSelf && targetRoles.every(name => {
    const role = roles.find(candidate => candidate.name === name);
    return role && roleCanBeGranted(role);
  })));
  const edit = isSelf || canManageTarget ? `<button type="button" class="secondary" data-user-profile="${esc(user.id)}">编辑信息</button>` : '';
  const role = canManageTarget ? `<button type="button" class="secondary" data-user-roles="${esc(user.id)}">编辑角色</button>` : '';
  const status = canManageTarget && !isSelf ? `<button type="button" class="${user.status === 'active' ? 'danger' : 'secondary'}" data-user-status="${esc(user.id)}" data-status="${user.status === 'active' ? 'disabled' : 'active'}">${user.status === 'active' ? '禁用' : '启用'}</button>` : '';
  return edit + role + status || '<span class="small">无可用操作</span>';
}

async function renderUsers(){
 const page=$('#page');page.innerHTML='<div class="empty">读取全站用户…</div>';
 try{const [users,roles]=await Promise.all([api('/admin/v1/users'),api('/admin/v1/roles')]);if(!page.isConnected)return;state.cache.roleNames=Object.fromEntries(roles.map(r=>[r.name,r.display_name||r.name]));
 page.innerHTML=`<div class="management-grid"><section class="card"><div class="toolbar"><h2>所有用户</h2><span class="badge">${users.length} 位用户</span></div><p class="small">包含管理员创建、邮箱注册与授权注册的所有用户，不局限于后台人员。</p><form class="directory-filter" data-user-filter><label class="field"><span class="field-label">搜索昵称、用户名、邮箱或 UID</span><input name="search" maxlength="200"></label><label class="field"><span class="field-label">角色</span><select name="role"><option value="">全部角色</option>${roles.map(r=>`<option value="${esc(r.name)}">${esc(r.display_name)}</option>`).join('')}</select></label></form><div class="table-wrap user-table-wrap"><table class="user-table"><thead><tr><th>昵称 / 用户名</th><th>邮箱 / UID</th><th>角色</th><th>状态 / 注册时间</th><th>操作</th></tr></thead><tbody data-user-rows></tbody></table></div></section><section class="card spaced-split"><h2>创建用户</h2><form id="user-form" class="form-stack"><div class="grid-2"><label>用户名<input name="username" minlength="3" maxlength="64" required autocomplete="username"></label><label>邮箱（可选）<input name="email" type="email" maxlength="254"></label><label>密码<input name="password" type="password" maxlength="72" required autocomplete="new-password"></label><label>角色<select name="role">${roles.map(r=>`<option value="${esc(r.name)}" ${r.name==='member'?'selected':''}>${esc(r.display_name)}</option>`).join('')}</select></label></div><button type="submit">创建用户</button></form><p class="small">默认创建普通用户。用户在用户中心管理个人资料、凭据和会话。</p></section></div>`;
 const filter=page.querySelector('[data-user-filter]'),rows=page.querySelector('[data-user-rows]');const draw=()=>{const search=filter.elements.search.value.trim().toLowerCase(),role=filter.elements.role.value;const selected=users.filter(u=>(!role||(u.roles||[u.role]).includes(role))&&(!search||[u.nickname,u.username,u.email,u.uid||u.id].join(' ').toLowerCase().includes(search)));rows.innerHTML=selected.map(u=>`<tr><td><strong>${esc(u.nickname||u.username)}</strong><br><span class="small">${esc(u.username)}</span></td><td>${esc(u.email||'未绑定邮箱')}<br><code>${esc(u.uid||u.id)}</code></td><td>${(u.roles||[u.role]).map(r=>`<span class="badge">${esc(roleLabel(r))}</span>`).join(' ')}</td><td><span class="badge status-badge ${u.status==='active'?'is-success':'off'}">${u.status==='active'?'启用':'停用'}</span><br><span class="small">${esc(formatDate(u.created_at))}</span></td><td><div class="actions">${userActions(u,roles)}</div></td></tr>`).join('')||'<tr><td colspan="5">没有匹配用户。</td></tr>';rows.querySelectorAll('[data-user-profile]').forEach(button=>button.onclick=()=>openProfileModal(users.find(u=>u.id===button.dataset.userProfile)));rows.querySelectorAll('[data-user-roles]').forEach(button=>button.onclick=()=>openRoleModal(users.find(u=>u.id===button.dataset.userRoles),roles));rows.querySelectorAll('[data-user-status]').forEach(button=>button.onclick=async()=>{try{await withAction(button,'更新中…',()=>api('/admin/v1/users/'+encodeURIComponent(button.dataset.userStatus)+'/status',{method:'PUT',body:JSON.stringify({status:button.dataset.status})}));notice('用户状态已更新',true);renderUsers()}catch(error){notice(error.message)}})};filter.onsubmit=e=>e.preventDefault();filter.elements.search.oninput=draw;filter.elements.role.onchange=draw;draw();
 const form=$('#user-form');restrictForm($('#user-form'), 'user.manage');form.onsubmit=async e=>{e.preventDefault();const bytes=new TextEncoder().encode(form.elements.password.value).length;form.elements.password.setCustomValidity(bytes<8||bytes>72?'密码须为 8–72 个 UTF-8 字节。':'');if(!form.reportValidity())return;try{await withSubmitting(form,'创建中…',()=>api('/admin/v1/users',{method:'POST',body:JSON.stringify(Object.fromEntries(new FormData(form)))}));notice('用户已创建',true);renderUsers()}catch(error){notice(error.message)}};form.elements.password.oninput=()=>form.elements.password.setCustomValidity('');
 }catch(e){if(page.isConnected)page.innerHTML=`<div class="empty" role="alert">${esc(e.message)}</div>`}
}

function formatDate(value) {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? '—' : date.toLocaleString('zh-CN', {hour12:false});
}

function formatMetric(value, digits = 0) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? number.toLocaleString('zh-CN', {maximumFractionDigits: digits, minimumFractionDigits: digits}) : '0';
}

function formatUptime(seconds) {
  seconds = Math.max(0, Number(seconds || 0));
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return days ? `${days} 天 ${hours} 小时` : hours ? `${hours} 小时 ${minutes} 分` : `${minutes} 分钟`;
}

function observationChart(points, key, label, color = '#365cff', formatter = (value) => formatMetric(value)) {
  const values = points.map((point) => Number(point[key] || 0));
  if (!values.length) return '<div class="chart-empty">暂无时间序列数据</div>';
  const width = 720, height = 190, padX = 22, padY = 20;
  const maxValue = Math.max(...values, 1);
  const coordinates = values.map((value, index) => {
    const x = values.length === 1 ? width / 2 : padX + index * (width - padX * 2) / (values.length - 1);
    const y = height - padY - value / maxValue * (height - padY * 2);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(' ');
  const last = values[values.length - 1];
  const peak = Math.max(...values);
  return `<div class="metric-chart" role="img" aria-label="${esc(label)}，当前 ${esc(formatter(last))}，峰值 ${esc(formatter(peak))}">
    <div class="metric-chart-head"><span>${esc(label)}</span><strong>${esc(formatter(last))}</strong></div>
    <svg viewBox="0 0 ${width} ${height}" preserveAspectRatio="none" aria-hidden="true">
      <line x1="${padX}" y1="${height-padY}" x2="${width-padX}" y2="${height-padY}" class="chart-axis"></line>
      <polyline points="${coordinates}" fill="none" stroke="${color}" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"></polyline>
    </svg>
    <div class="metric-chart-foot"><span>最近 60 分钟</span><span>峰值 ${esc(formatter(peak))}</span></div>
  </div>`;
}

function alertCard(alert) {
  const firing = alert.status === 'firing';
  const severity = alert.severity === 'critical' ? '严重' : '警告';
  return `<article class="alert-card ${esc(alert.severity)} ${firing ? 'firing' : 'resolved'}">
    <div class="alert-indicator" aria-hidden="true"></div>
    <div class="alert-content"><div class="alert-title-row"><strong>${esc(alert.title)}</strong><span class="badge">${firing ? severity : '已恢复'}</span></div>
      <p>${esc(alert.message)}</p><div class="alert-meta"><span>${esc(alert.source)}</span><span>${esc(formatDate(alert.last_seen))}</span>${alert.acknowledged ? `<span>已由 ${esc(alert.acknowledged_by || '管理员')} 确认</span>` : ''}</div></div>
    ${firing && !alert.acknowledged && can('observability.manage') ? `<button class="secondary alert-ack" data-alert-ack="${esc(alert.id)}">确认</button>` : ''}
  </article>`;
}

function logRow(item) {
  const fields = Object.entries(item.fields || {}).filter(([key]) => !['request_id','trace_id'].includes(key)).slice(0, 5).map(([key,value]) => `${key}=${typeof value === 'object' ? JSON.stringify(value) : value}`).join(' · ');
  return `<tr><td>${esc(formatDate(item.timestamp))}</td><td><span class="log-level level-${esc(String(item.level || '').toLowerCase())}">${esc(item.level || 'INFO')}</span></td><td><strong>${esc(item.message)}</strong>${fields ? `<div class="small log-fields">${esc(fields)}</div>` : ''}</td><td><code>${esc(item.request_id || '—')}</code></td><td><code>${esc(item.trace_id || '—')}</code></td></tr>`;
}

function traceRow(item) {
  const path = item.attributes?.['url.path'] || item.attributes?.['http.route'] || '—';
  const statusCode = item.attributes?.['http.response.status_code'] || '';
  const status = item.status === 'error' ? '错误' : '正常';
  return `<tr><td>${esc(formatDate(item.started_at))}</td><td><strong>${esc(item.name)}</strong><div class="small">${esc(path)}</div></td><td>${formatMetric(item.duration_ms, 1)} ms</td><td><span class="badge trace-${esc(item.status)}">${status}${statusCode ? ` · HTTP ${esc(statusCode)}` : ''}</span></td><td><code>${esc(item.trace_id)}</code></td></tr>`;
}

async function renderObservability() {
  if (state.page !== 'observability') return;
  const page = $('#page'); if (!page.isConnected) return; page.innerHTML = '<div class="empty">加载内置观测数据…</div>';
  const filters = state.cache.observabilityFilters || {level:'', search:'', traceStatus:''};
  try {
    const logParams = new URLSearchParams({limit:'100'});
    if (filters.level) logParams.set('level', filters.level);
    if (filters.search) logParams.set('search', filters.search);
    const traceParams = new URLSearchParams({limit:'100'});
    if (filters.search) traceParams.set('search', filters.search);
    if (filters.traceStatus) traceParams.set('status', filters.traceStatus);
    const [dashboard, logs, traces] = await Promise.all([
      api('/admin/v1/observability/summary'),
      api(`/admin/v1/observability/logs?${logParams}`),
      api(`/admin/v1/observability/traces?${traceParams}`),
    ]);
    const metrics = dashboard.metrics || {};
    const storage = dashboard.storage || {};
    const firing = (dashboard.alerts || []).filter((alert) => alert.status === 'firing');
    const storageError = String(storage.last_write_error || '');
    const storageLabel = storageError ? '记录保存异常' : (storage.persistent ? '记录保存正常' : '记录暂未保存');
    if (!page.isConnected) return; page.innerHTML = `<div class="observability-page">
      <section class="observability-intro"><div><h2>运行情况</h2><p>查看请求、响应与服务状态。</p></div><div class="storage-state ${storage.persistent && !storageError ? 'ok' : 'warning'}"><strong>${storageLabel}</strong><span>日志 ${formatMetric(storage.logs)} / ${formatMetric(storage.max_logs)} · 链路 ${formatMetric(storage.traces)} / ${formatMetric(storage.max_traces)}</span>${storageError ? `<span class="storage-error" title="${esc(storageError)}">${esc(storageError)}</span>` : ''}</div></section>
      <div class="stats observability-stats">
        <div class="stat"><span class="small">网关请求</span><div class="number">${formatMetric(metrics.gateway_requests_total)}</div><span class="metric-caption">累计调用</span></div>
        <div class="stat"><span class="small">错误率</span><div class="number ${Number(metrics.gateway_error_rate) >= .05 ? 'metric-danger' : ''}">${formatMetric(Number(metrics.gateway_error_rate || 0) * 100, 1)}%</div><span class="metric-caption">HTTP 4xx / 5xx</span></div>
        <div class="stat"><span class="small">平均延迟</span><div class="number">${formatMetric(metrics.average_latency_ms, 1)}<small> ms</small></div><span class="metric-caption">网关请求</span></div>
        <div class="stat"><span class="small">P95 延迟</span><div class="number">${formatMetric(metrics.p95_latency_ms)}<small> ms</small></div><span class="metric-caption">95% 请求以内</span></div>
        <div class="stat"><span class="small">活动告警</span><div class="number ${firing.length ? 'metric-danger' : ''}">${firing.length}</div><span class="metric-caption">${firing.filter(x=>x.severity==='critical').length} 个严重</span></div>
        <div class="stat"><span class="small">运行时间</span><div class="number uptime">${esc(formatUptime(metrics.uptime_seconds))}</div><span class="metric-caption">本次运行</span></div>
      </div>
      <div class="observability-charts">
        <div class="card">${observationChart(metrics.series || [], 'requests', '每分钟网关请求', '#365cff')}</div>
        <div class="card">${observationChart(metrics.series || [], 'average_latency_ms', '每分钟平均延迟', '#d97706', value => `${formatMetric(value,1)} ms`)}</div>
      </div>
      <section class="card observation-section"><div class="section-heading"><div><h2>运行告警</h2><p>根据最近 5 分钟的错误率、延迟、限流、上游和插件失败自动计算。</p></div><span class="badge">${firing.length} 个活动告警</span></div><div class="alert-list">${(dashboard.alerts || []).length ? dashboard.alerts.map(alertCard).join('') : '<div class="empty">当前没有告警</div>'}</div></section>
      <section class="card observation-section"><div class="section-heading"><div><h2>应用日志</h2><p>查看服务运行日志，可按级别或关键词筛选。敏感信息会自动隐藏。</p></div><div class="section-heading-actions"><span class="badge">匹配 ${formatMetric(logs.total)} 条</span>${can('observability.logs.clear') ? `<button type="button" class="danger" data-clear-application-logs ${Number(storage.logs || 0) === 0 ? 'disabled' : ''}>清理日志</button>` : ''}</div></div>
        <form id="observation-filter" class="observation-filter"><label><span>级别</span><select name="level"><option value="">全部</option><option value="ERROR" ${filters.level==='ERROR'?'selected':''}>错误</option><option value="WARN" ${filters.level==='WARN'?'selected':''}>警告</option><option value="INFO" ${filters.level==='INFO'?'selected':''}>信息</option><option value="DEBUG" ${filters.level==='DEBUG'?'selected':''}>调试</option></select></label><label class="filter-search"><span>搜索日志和链路</span><input name="search" value="${esc(filters.search)}" placeholder="消息、路径、Request ID 或 Trace ID"></label><label><span>链路状态</span><select name="trace_status"><option value="">全部</option><option value="error" ${filters.traceStatus==='error'?'selected':''}>错误</option><option value="ok" ${filters.traceStatus==='ok'?'selected':''}>正常</option></select></label><button type="submit">筛选</button><button type="button" class="secondary" id="clear-observation-filter">重置筛选</button></form>
        <div class="table-wrap observation-table" role="region" aria-label="应用日志列表" tabindex="0"><table><thead><tr><th scope="col">时间</th><th scope="col">级别</th><th scope="col">消息</th><th scope="col">请求 ID</th><th scope="col">链路 ID</th></tr></thead><tbody>${logs.items?.length ? logs.items.map(logRow).join('') : '<tr><td colspan="5">暂无匹配日志</td></tr>'}</tbody></table></div></section>
      <section class="card observation-section"><div class="section-heading"><div><h2>请求链路</h2><p>查看请求的处理时间与结果，可按链路 ID 关联日志。</p></div><span class="badge">匹配 ${formatMetric(traces.total)} 条</span></div><div class="table-wrap observation-table" role="region" aria-label="请求链路列表" tabindex="0"><table><thead><tr><th scope="col">开始时间</th><th scope="col">操作</th><th scope="col">耗时</th><th scope="col">状态</th><th scope="col">链路 ID</th></tr></thead><tbody>${traces.items?.length ? traces.items.map(traceRow).join('') : '<tr><td colspan="5">暂无链路数据，请调用已发布接口后刷新</td></tr>'}</tbody></table></div></section>
    </div>`;
    $('#observation-filter').onsubmit = (event) => {
      event.preventDefault(); const form = new FormData(event.currentTarget);
      state.cache.observabilityFilters = {level:String(form.get('level')||''), search:String(form.get('search')||'').trim(), traceStatus:String(form.get('trace_status')||'')}; renderObservability();
    };
    page.querySelector('[data-clear-application-logs]')?.addEventListener('click', () => openLogCleanupModal(storage.logs || 0));
    $('#clear-observation-filter').onclick = () => { state.cache.observabilityFilters = {level:'',search:'',traceStatus:''}; renderObservability(); };
    $$('[data-alert-ack]').forEach((button) => button.onclick = async () => { try { await api(`/admin/v1/observability/alerts/${encodeURIComponent(button.dataset.alertAck)}/ack`, {method:'POST'}); notice('告警已确认', true); renderObservability(); } catch (error) { notice(error.message); } });
  } catch (error) { if (!page.isConnected) return; page.innerHTML = `<div class="empty" role="alert">${esc(error.message)}</div>`; }
}

function closeLogCleanupModal(force = false) {
  const modal = $('#log-cleanup-modal');
  if (!modal || (modal._saving && !force)) return;
  const previous = modal._previousFocus;
  const app = $('#app'); if (app) app.inert = modal._previousInert;
  document.body.style.overflow = modal._previousOverflow;
  modal.remove();
  if (previous?.isConnected) previous.focus();
}

function openLogCleanupModal(count) {
  if (!can('observability.logs.clear') || $('#log-cleanup-modal')?._saving || $('#user-profile-modal')?._saving || $('#role-edit-modal')?._saving) return;
  closeLogCleanupModal(); closeRoleModal(); closeProfileModal();
  const modal = document.createElement('div');
  modal.id = 'log-cleanup-modal'; modal.className = 'modal-backdrop';
  modal._previousFocus = document.activeElement; modal._previousInert = $('#app')?.inert || false;
  modal._previousOverflow = document.body.style.overflow;
  modal.innerHTML = `<section class="modal-card log-cleanup-modal" role="dialog" aria-modal="true" aria-labelledby="log-cleanup-title" aria-describedby="log-cleanup-description" tabindex="-1"><div class="modal-heading"><div><h2 id="log-cleanup-title">清理全部应用日志</h2><p class="small">当前可查询 ${esc(formatMetric(count))} 条日志，此操作无法撤销。</p></div><button type="button" class="modal-close" aria-label="关闭日志清理确认">×</button></div><form id="log-cleanup-form" class="form-stack"><p id="log-cleanup-description" class="profile-impact">将清理全部应用日志及已保存的历史记录，不仅是当前筛选结果。审计日志、请求链路与统计数据会保留；Docker 和外部日志系统的记录不受影响。后续产生的新日志仍会正常记录。</p><label class="checkbox-field"><input type="checkbox" name="confirm" required><span>我确认清理全部应用日志</span></label><p class="message" data-log-cleanup-error role="alert" aria-live="polite" tabindex="-1"></p><div class="modal-actions"><button type="button" class="secondary" data-log-cleanup-cancel>取消</button><button type="submit" class="danger" disabled>确认清理</button></div></form></section>`;
  document.body.appendChild(modal); $('#app').inert = true; document.body.style.overflow = 'hidden';
  const close = () => closeLogCleanupModal();
  modal.querySelector('.modal-close').onclick = close;
  modal.querySelector('[data-log-cleanup-cancel]').onclick = close;
  modal.addEventListener('click', event => { if (event.target === modal) close(); });
  const form = modal.querySelector('form'); const submit = form.querySelector('[type="submit"]');
  form.elements.confirm.onchange = () => { submit.disabled = !form.elements.confirm.checked; };
  form.onsubmit = async event => {
    event.preventDefault(); if (modal._saving || !form.reportValidity() || !form.elements.confirm.checked) return;
    modal._saving = true;
    const fields = [...modal.querySelectorAll('input, button[type="button"]')]; fields.forEach(field => { field.disabled = true; });
    const error = modal.querySelector('[data-log-cleanup-error]'); error.textContent = '';
    try {
      await withSubmitting(form, '清理中…', () => api('/admin/v1/observability/logs', {method:'DELETE', body:JSON.stringify({confirm:true})}));
      closeLogCleanupModal(true); notice('全部应用日志已清理，新日志仍会正常记录。', true);
      if (state.page === 'observability') await renderObservability();
    } catch (caught) {
      if (modal.isConnected) { error.textContent = caught.status === 403 ? '当前账号没有清理日志权限，请联系管理员。' : caught.message; error.focus(); }
    } finally {
      modal._saving = false; if (modal.isConnected) fields.forEach(field => { field.disabled = false; });
    }
  };
  bindModalKeyboard(modal, close); modal.querySelector('[data-log-cleanup-cancel]').focus();
}

async function renderAuditLogs(pageNumber = 1) {
  if (state.page !== 'audit') return;
  const page = $('#page');
  const filters = state.cache.auditFilters || {};
  const request = page._auditRequest = (page._auditRequest || 0) + 1;
  if (!page.isConnected) return; page.innerHTML = `<div class="card audit-filters"><h2>筛选条件</h2><form id="audit-filter-form" class="filter-grid">
    <label>操作<input name="action" value="${esc(filters.action || '')}" placeholder="例如 api.publish"></label>
    <label>资源类型<input name="resource_type" value="${esc(filters.resource_type || '')}" placeholder="例如 api、user"></label>
    <label>操作者 ID<input name="actor_id" value="${esc(filters.actor_id || '')}" placeholder="用户 UUID"></label>
    <label>请求 ID<input name="request_id" value="${esc(filters.request_id || '')}" placeholder="X-Request-ID"></label>
    <label>开始时间<input name="from" type="datetime-local" value="${esc(filters.from || '')}"></label>
    <label>结束时间<input name="to" type="datetime-local" value="${esc(filters.to || '')}"></label>
    <div class="actions filter-actions"><button>查询</button><button type="button" id="audit-reset" class="secondary">重置</button></div>
  </form></div><div id="audit-results" class="empty spaced-split">加载中…</div>`;
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
    if (!page.isConnected || page._auditRequest !== request) return;
    const items = result.items || [];
    const totalPages = Math.max(1, Math.ceil((result.total || 0) / (result.page_size || 20)));
    $('#audit-results').className = 'table-wrap spaced-split audit-table-wrap';
    $('#audit-results').setAttribute('role', 'region'); $('#audit-results').setAttribute('aria-label', '审计记录'); $('#audit-results').tabIndex = 0;
    $('#audit-results').innerHTML = `<div class="toolbar table-toolbar"><div><h2>审计记录</h2><p class="small">共 ${result.total || 0} 条；所有敏感字段均已脱敏。</p></div><span class="small">第 ${result.page || 1} / ${totalPages} 页</span></div>
      <table><thead><tr><th scope="col">时间</th><th scope="col">操作</th><th scope="col">操作者</th><th scope="col">资源</th><th scope="col">请求</th><th scope="col">详情</th></tr></thead><tbody>${items.length ? items.map(auditRow).join('') : '<tr><td colspan="6"><div class="empty">没有匹配的审计记录</div></td></tr>'}</tbody></table>
      <div class="pager"><button id="audit-prev" class="secondary" ${result.page <= 1 ? 'disabled' : ''}>上一页</button><button id="audit-next" class="secondary" ${result.page >= totalPages ? 'disabled' : ''}>下一页</button></div>`;
    $('#audit-prev').onclick = () => { if (result.page > 1) renderAuditLogs(result.page - 1); };
    $('#audit-next').onclick = () => { if (result.page < totalPages) renderAuditLogs(result.page + 1); };
  } catch (error) { if (!page.isConnected || page._auditRequest !== request) return; const results = page.querySelector('#audit-results'); results.className = 'empty spaced-split'; results.textContent = error.message; results.setAttribute('role', 'alert'); }
}

function auditRow(item) {
  const timestamp = formatDate(item.created_at);
  const actor = item.actor_email || item.actor_id || item.actor_type || '-';
  const details = Object.keys(item.details || {}).length ? `<details><summary>查看</summary><pre>${esc(JSON.stringify(item.details, null, 2))}</pre></details>` : '-';
  return `<tr><td><time datetime="${esc(item.created_at || '')}">${esc(timestamp)}</time></td><td><span class="badge">${esc(item.action)}</span><br><span class="small">${esc(item.method || '')} ${esc(item.status_code || '')}</span></td><td>${esc(actor)}<br><span class="small">${esc(item.actor_type || '')}</span></td><td><strong>${esc(item.resource_type)}</strong><br><code>${esc(item.resource_id || '-')}</code></td><td><code>${esc(item.request_id || '-')}</code><br><span class="small">${esc(item.remote_addr || '')}</span></td><td class="audit-details">${details}</td></tr>`;
}

async function renderPlugins() {
  if (state.page !== 'plugins') return;
  const page = $('#page'); if (!page.isConnected) return; page.innerHTML = '<div class="empty">加载中…</div>';
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
    if (!page.isConnected) return; page.innerHTML = `
      <div class="plugin-page">
        <div class="plugin-top">
          <section class="plugin-panel plugin-versions" aria-labelledby="plugin-versions-title">
            <div class="plugin-panel-head"><div><h2 id="plugin-versions-title">已安装插件</h2><p>管理版本与运行状态</p></div><span class="plugin-count">${managed.length} 个版本</span></div>
            <div class="plugin-version-list">${managed.length ? managed.map(item => pluginRow(item, library || [])).join('') : '<div class="plugin-empty">暂无托管插件</div>'}</div>
            <div class="plugin-runtime"><span>运行中</span><div>${runtime}</div></div>
          </section>
          <section class="plugin-panel plugin-upload" aria-labelledby="plugin-upload-title">
            <div class="plugin-panel-head"><div><h2 id="plugin-upload-title">上传插件</h2><p>添加一个新插件版本</p></div></div>
            <p class="plugin-upload-hint">上传 <code>manifest.yaml</code> 和对应的 <code>.wasm</code> 模块。上传后默认为禁用，启用后仍需配置并发布接口路由。</p>
            ${can('plugin.manage') ? `<form id="plugin-upload-form" class="form-stack" enctype="multipart/form-data"><label class="field"><span class="field-label">清单文件</span><input type="file" name="manifest" accept=".yaml,.yml" required><span class="field-hint">描述插件名称、版本和路由。</span></label><label class="field"><span class="field-label">WASM 模块</span><input type="file" name="wasm" accept=".wasm,application/wasm" required><span class="field-hint">上传与清单入口文件一致的 WASM 文件。</span></label><div class="form-footer"><button type="submit">上传插件</button></div></form>` : '<p class="small">当前账号没有插件管理权限。</p>'}
          </section>
        </div>
        <section class="plugin-panel plugin-usage" aria-labelledby="plugin-usage-title">
          <div class="plugin-panel-head"><div><h2 id="plugin-usage-title">接口与调用</h2><p>查看路由状态，复制请求示例</p></div></div>
          <p class="plugin-usage-help">插件需要绑定并发布接口才可调用。下方展示插件声明的接口和已绑定的路由；未声明的路径不会自动生成。调用密钥可在“调用凭证”页创建，使用示例前请替换为自己的密钥。</p>
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
  } catch (error) { if (!page.isConnected) return; page.innerHTML = `<div class="empty" role="alert">${esc(error.message)}</div>`; }
}

async function openPluginRouteDraft(pluginName, path, method = 'GET', authMode = 'api_key', title = '') {
  if (!can('api.write')) return;
  state.page = 'apis';
  await renderPage();
  const form = $('#api-form');
  if (!form) return;
  form.elements.name.value = title || `${pluginName} · ${path.split('/').pop()}`;
  form.querySelectorAll('input[name="methods"]').forEach(input => { input.checked = input.value === method; });
  form.elements.auth_mode.value = authMode === 'none' ? 'none' : 'api_key';
  form.elements.path.value = path;
  form.elements.plugin.value = pluginName;
  form.elements.plugin.dispatchEvent(new Event('input'));
  form.scrollIntoView({behavior:'smooth', block:'start'});
  notice('已预填路径和插件名称；确认后保存草稿，再点击“发布”。', true);
}

function shellQuote(value) { return "'" + String(value).replaceAll("'", "'\"'\"'") + "'"; }

function pluginCallCommand(item, apiItem) {
  const examples = apiItem.example_path_params || {};
  const path = apiItem.path.replace(/\{([^}]+)\}/g, (_, key) => encodeURIComponent(examples[key] || '1'));
  const query = Object.entries(apiItem.example_query || {}).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join('&');
  const method = shellQuote(apiItem.method || 'GET');
  let auth = '';
  if (apiItem.auth_mode === 'api_key') auth = ' -H "X-API-Key: 你的 API Key"';
  return `curl -X ${method}${auth} ${shellQuote((state.callOrigin||location.origin) + path + (query ? '?' + query : ''))}`;
}

function pluginUsage(item, apis) {
  const routes = (apis || []).filter(apiItem => apiItem.plugin === item.name);
  let manifest = item.manifest || {};
  if (typeof manifest === 'string') { try { manifest = JSON.parse(manifest); } catch (_) { manifest = {}; } }
  const suggested = Array.isArray(manifest.routes) ? manifest.routes : [];
  // Declared endpoints stay visible after only some have been published; actual route settings take precedence.
  const listed = suggested.map(spec => {
    const bound = routes.find(route => (route.methods?.length ? route.methods : [route.method]).includes(spec.method) && route.path === spec.path);
    const conflict = !bound && (apis || []).some(route => (route.methods?.length ? route.methods : [route.method]).includes(spec.method) && route.path === spec.path);
    return {...spec, ...(bound || {unconfigured:true}), name:bound?.name || spec.name || spec.path.split('/').pop(),
      auth_mode:bound?.auth_mode || spec.auth_mode, method:spec.method, conflict};
  });
  for (const route of routes) {
    for (const method of (route.methods?.length ? route.methods : [route.method])) {
      if (!suggested.some(spec => method===spec.method && route.path===spec.path)) listed.push({...route,method});
    }
  }
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
  </article>`;
}

function authLabel(mode) {
  return mode === 'none' ? '无需验证' : 'KEY';
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
  const headers = {'X-API-Request':'1'};
  try {
    const response = await fetch('/admin/v1/plugins', {method:'POST', headers, body:new FormData(form),credentials:'same-origin',cache:'no-store'});
    const body = await response.json().catch(() => ({}));
    if (response.status === 401) { clearSession(); throw new Error('登录已过期，请重新登录'); }
    if (!response.ok) {
    const error = new Error(body.error || `请求失败：${response.status}`);
    error.code = body.code || ''; error.status = response.status;
    throw error;
  }
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
    await withSubmitting(form, '登录中…', () => login($('#username').value,$('#password').value));
  } catch (error) { notice(authErrorMessage(error), false, 'auth'); }
};
$('#logout').onclick = logout;
$('#account-settings').onclick = () => location.assign('/account');
$('#retry-auth').onclick = () => {authEpoch++;void hydrateSession()};
$('#refresh').onclick = () => withAction($('#refresh'), '刷新中…', renderPage);
$$('#nav button').forEach((button) => button.onclick = () => { if (button.classList.contains('hidden')) return; state.page = button.dataset.page; renderPage(); });
if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',()=>void hydrateSession(),{once:true});else void hydrateSession();

let pendingResetToken = '';
function openRecoveryModal(resetToken = '') {
 if(resetToken){location.replace('/account#reset='+encodeURIComponent(resetToken));return;}
 location.assign('/account?recover=1&return=admin');return;

  const old = $('#recovery-modal'); if (old?._saving) return; old?.remove();
  const resetting = Boolean(resetToken);
  const modal=document.createElement('div'); modal.id='recovery-modal'; modal.className='modal-backdrop';
  const previous=document.activeElement; const app=$('#app'); const wasInert=app.inert; const overflow=document.body.style.overflow;
  modal.innerHTML=`<section class="modal-card" role="dialog" aria-modal="true" aria-labelledby="recovery-title" tabindex="-1"><div class="modal-heading"><div><h2 id="recovery-title">${resetting?'设置新密码':'找回密码'}</h2><p class="small">${resetting?'重置链接仅可使用一次，15 分钟内有效。':'向账号绑定的邮箱发送重置链接。'}</p></div><button class="modal-close" type="button" aria-label="关闭找回密码">×</button></div>${state.recoveryEnabled?`<form class="form-stack" id="recovery-form">${resetting?'<label>新密码<input name="password" type="password" required maxlength="72" autocomplete="new-password"></label><label>确认新密码<input name="confirm" type="password" required maxlength="72" autocomplete="new-password"></label><span class="field-hint">新密码为 8–72 个 UTF-8 字节。成功后所有旧会话失效。</span>':'<label>已绑定邮箱<input name="email" type="email" required maxlength="254" autocomplete="email"></label><span class="field-hint">无论邮箱是否存在，都会显示相同的提交结果。请检查收件箱和垃圾邮件。</span>'}<p class="message" id="recovery-message" role="status" aria-live="polite" tabindex="-1"></p><div class="modal-actions"><button type="button" class="secondary" data-recovery-close>取消</button><button type="submit">${resetting?'保存新密码':'发送重置链接'}</button></div></form>`:'<p class="profile-impact">管理员尚未配置邮件服务。请先联系管理员重置密码；配置 SMTP 后即可通过绑定邮箱自行找回。</p><div class="modal-actions"><button type="button" data-recovery-close>知道了</button></div>'}</section>`;
  document.body.append(modal); app.inert=true;document.body.style.overflow='hidden';
  const close=()=>{if(modal._saving)return;modal.querySelectorAll('input').forEach(input=>{input.value=''});app.inert=wasInert;document.body.style.overflow=overflow;modal.remove();pendingResetToken='';if(previous?.isConnected)previous.focus()};
  modal.querySelector('.modal-close').onclick=close;modal.querySelector('[data-recovery-close]').onclick=close;
  modal.addEventListener('click',e=>{if(e.target===modal)close()});
  bindModalKeyboard(modal, close);
  const form=modal.querySelector('form');
  if(form){form.querySelectorAll('input').forEach(input=>input.oninput=()=>{input.setCustomValidity('');if(form.elements.confirm)form.elements.confirm.setCustomValidity('')});form.onsubmit=async e=>{
    e.preventDefault();if(modal._saving)return;
    if(resetting){const bytes=new TextEncoder().encode(form.elements.password.value).length;form.elements.password.setCustomValidity(bytes<8||bytes>72?'密码必须为 8–72 个 UTF-8 字节。':'');form.elements.confirm.setCustomValidity(form.elements.password.value===form.elements.confirm.value?'':'两次输入的密码不一致。')}
    if(!form.reportValidity())return;
    const payload=resetting?{token:resetToken,password:form.elements.password.value}:{email:form.elements.email.value.trim()};
    const message=modal.querySelector('#recovery-message');modal._saving=true;const controls=[...modal.querySelectorAll('input,button')];controls.forEach(el=>el.disabled=true);
    try{await api(resetting?'/auth/v1/reset-password':'/auth/v1/forgot-password',{method:'POST',body:JSON.stringify(payload)});modal._saving=false;
      if(resetting){close();clearSession();broadcastAuth();notice('密码已重置，请使用新密码登录。',true,'auth');$('#password').focus()}else{message.textContent='如果该邮箱已绑定有效账号，重置链接将发送到该邮箱，15 分钟内有效。';message.focus();form.querySelector('[type="submit"]').textContent='已提交';controls.filter(el=>el.hasAttribute('data-recovery-close')||el.classList.contains('modal-close')).forEach(el=>el.disabled=false)}
    }catch(error){modal._saving=false;controls.forEach(el=>el.disabled=false);message.textContent=error.status===429?'请求太频繁，请一分钟后重试。':resetting?'链接无效、已过期或已使用，请重新找回密码。':'暂时无法发送，请稍后重试或联系管理员。';message.focus()}
  }}
  (modal.querySelector('input')||modal.querySelector('[data-recovery-close]')).focus();
}
async function initRecovery() {
  const match=location.hash.match(/^#reset=([0-9a-f]{64})$/);
  if(match){pendingResetToken=match[1];history.replaceState(null,'',location.pathname+location.search)}
  try{const result=await api('/auth/v1/recovery');state.recoveryEnabled=result.enabled===true}catch(_){state.recoveryEnabled=false}
  $('#forgot-password').onclick=()=>location.assign('/account?recover=1&return=admin');
  if(pendingResetToken)openRecoveryModal(pendingResetToken);
}

initRecovery();

async function initSetup() {
  try {
    const result = await api('/auth/v1/setup');
    if (!result.available) { pendingSetupKey = ''; return; }
    const card = $('#login-view .auth-card');
    const section = document.createElement('section');
    section.innerHTML = `<details class="setup-panel" ${pendingSetupKey?'open':''}><summary>首次部署 · 注册管理员</summary><p class="small">一次性密钥只用于创建首个管理员，注册成功后永久失效。用户名与邮箱独立设置。</p><form class="form-stack setup-form"><label class="field"><span class="field-label">一次性注册密钥</span><input name="key" type="password" required autocomplete="off" minlength="32" maxlength="128"></label><label class="field"><span class="field-label">用户名</span><input name="username" required autocomplete="username" pattern="[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}" maxlength="64"></label><label class="field"><span class="field-label">邮箱</span><input name="email" type="email" required autocomplete="email" maxlength="254"></label><label class="field"><span class="field-label">密码</span><input name="password" type="password" required autocomplete="new-password" maxlength="72"></label><label class="field"><span class="field-label">确认密码</span><input name="confirm" type="password" required autocomplete="new-password" maxlength="72"></label><span class="field-hint">密码为 8–72 个 UTF-8 字节。</span><p class="message" role="status" aria-live="polite"></p><button type="submit">注册管理员</button></form></details>`;
    card.append(section);
    const form = section.querySelector('form');
    form.elements.key.value = pendingSetupKey; pendingSetupKey = '';
    form.querySelectorAll('input').forEach(input => { input.oninput = () => { form.elements.password.setCustomValidity(''); form.elements.confirm.setCustomValidity(''); }; });
    form.onsubmit = async event => {
      event.preventDefault(); if (form._saving) return;
      const bytes = new TextEncoder().encode(form.elements.password.value).length;
      form.elements.password.setCustomValidity(bytes<8||bytes>72?'密码必须为 8–72 个 UTF-8 字节。':'');
      form.elements.confirm.setCustomValidity(form.elements.confirm.value===form.elements.password.value?'':'两次输入的密码不一致。');
      if (!form.reportValidity()) return;
      form._saving=true; const fields=[...form.querySelectorAll('input')]; fields.forEach(field=>field.disabled=true);
      try {
        await withSubmitting(form, '正在注册…', () => api('/auth/v1/setup', {method:'POST', body:JSON.stringify({key:form.elements.key.value,username:form.elements.username.value.trim(),email:form.elements.email.value.trim(),password:form.elements.password.value})}));
        $('#username').value = form.elements.username.value.trim();
        form.querySelectorAll('input').forEach(input=>{input.value='';}); section.remove();
        notice('管理员注册成功，请使用设置的用户名和密码登录。',true,'auth'); $('#password').focus();
      } catch(error) { section.querySelector('.message').textContent = error.status===429?'请求过于频繁，请稍后重试。':'注册失败：请检查密钥、账号格式，或确认注册链接是否已经使用。'; }
      finally {form._saving=false;fields.forEach(field=>field.disabled=false);}
    };
    if (setupFragment) form.elements.username.focus();
  } catch (_) { pendingSetupKey=''; }
}
initSetup();

async function refreshSiteIdentity() {
  try {
    const response=await fetch('/public/v1/site',{credentials:'omit',cache:'no-store'});if(!response.ok)return;
    const data=await response.json();
    const candidate=data.api_domain||data.website_url||location.origin;try{const parsed=new URL(candidate);if(['https:','http:'].includes(parsed.protocol)&&!parsed.username&&!parsed.password)state.callOrigin=parsed.origin}catch(_){state.callOrigin=location.origin}
    if(typeof data.admin_title==='string') {document.title=data.admin_title;}
    if(typeof data.name==='string') {$('#console-view .brand strong').textContent=data.name;$('#console-view .brand strong').title=data.name;$('#login-view .eyebrow').textContent=data.name;}
  }catch(_){/* Static defaults remain usable during a transient network failure. */}
}
function siteSettingInput(label,name,value,max=120,type='text',hint='') {
 return `<label class="field"><span class="field-label">${esc(label)}</span><input name="${esc(name)}" type="${type}" value="${esc(value)}" maxlength="${max}">${hint?`<span class="field-hint">${esc(hint)}</span>`:''}</label>`;
}
function siteSettingArea(label,name,value,max) {
 return `<label class="field"><span class="field-label">${esc(label)}</span><textarea name="${esc(name)}" maxlength="${max}" rows="3">${esc(value)}</textarea></label>`;
}
async function renderSiteSettings() {
  if(state.page!=='settings')return;
  if(!(state.user?.roles||[state.user?.role]).includes('super_admin')){$('#page').innerHTML='<div class="empty">仅超级管理员可配置网站。</div>';return;}
  const page=$('#page');page.innerHTML='<div class="empty">加载网站设置…</div>';
  try {
    const cfg=await api('/admin/v1/settings');if(!page.isConnected)return;
    const site=cfg.site,smtp=cfg.smtp;
    page.innerHTML=`<div class="settings-page"><p class="settings-security-note">网站设置仅由超级管理员管理，保存后立即生效。</p><form id="site-settings-form" class="form-stack"><section class="card settings-section"><div class="settings-heading"><div><h2>网站基本信息</h2><p class="small">设置网站名称与页面标题。</p></div><span class="badge">全站生效</span></div><div class="grid-2">${siteSettingInput('网站名称','name',site.name,80)}${siteSettingInput('目录副标题','subtitle',site.subtitle,80)}${siteSettingInput('网站页面标题','public_title',site.public_title)}${siteSettingInput('后台页面标题','admin_title',site.admin_title)}</div>${siteSettingArea('网站介绍','description',site.description,600)}${siteSettingInput('搜索关键词','keywords',site.keywords,300)}<div class="grid-2">${siteSettingInput('网站公开地址','website_url',site.website_url,512,'url','填写网站的实际访问地址。')}${siteSettingInput('接口专用域名（可选）','api_domain',site.api_domain||'',512,'text','留空使用网站地址；填写后，接口只能通过这个地址调用。')}</div></section><section class="card settings-section"><h2>公开目录内容</h2>${siteSettingArea('首页主标题','hero_title',site.hero_title,160)}${siteSettingArea('首页介绍','hero_description',site.hero_description,600)}${siteSettingArea('网站公告（可选）','announcement',site.announcement,600)}<div class="grid-2">${siteSettingInput('页脚说明','footer',site.footer,300)}${siteSettingInput('公开联系邮箱（可选）','contact_email',site.contact_email,254,'email')}</div><p class="field-hint">主标题与介绍可以换行。</p></section><section class="card settings-section"><div class="settings-heading"><div><h2>SMTP 邮件与找回密码</h2><p class="small">配置发件服务，用于找回密码。</p></div><span class="badge ${cfg.recovery_enabled?'':'off'}">${cfg.recovery_enabled?'邮件找回已启用':'邮件找回未启用'}</span></div><label class="checkbox-field"><input type="checkbox" name="smtp_enabled" ${smtp.enabled?'checked':''}><span>启用 SMTP 与邮件找回密码</span></label><div class="grid-2">${siteSettingInput('SMTP 服务器','smtp_host',smtp.host,253,'text','填写公网邮件服务器主机名，不包含协议、路径或账号。')}${siteSettingInput('SMTP 端口','smtp_port',smtp.port,5,'number')}<label class="field"><span class="field-label">加密方式</span><select name="smtp_mode"><option value="starttls" ${smtp.mode==='starttls'?'selected':''}>STARTTLS · 常用端口 587</option><option value="tls" ${smtp.mode==='tls'?'selected':''}>隐式 TLS · 常用端口 465</option></select></label>${siteSettingInput('SMTP 用户名','smtp_username',smtp.username,254)}</div><label class="field"><span class="field-label">SMTP 密码 / 授权码</span><div class="password-input-row"><input name="smtp_password" type="password" maxlength="1024" autocomplete="new-password" placeholder="${cfg.smtp_password_set?'已设置；留空保留，不回显原密码':'未设置'}"><button class="secondary" type="button" data-smtp-visibility aria-pressed="false">显示</button></div><span class="field-hint">留空保留原密码；需要删除时勾选下方选项。</span></label><label class="checkbox-field"><input type="checkbox" name="clear_smtp_password"><span>清除已保存的 SMTP 密码</span></label>${siteSettingInput('发件人邮箱','smtp_from',smtp.from,254,'email')}${siteSettingInput('密码重置页面地址','smtp_reset_url',smtp.reset_url,512,'url','填写后台的安全访问地址。')}<p class="field-hint">发件邮箱与授权码需要匹配，请使用邮箱服务商提供的配置。</p></section><div class="settings-save"><p role="status" aria-live="polite" tabindex="-1" data-settings-message></p><button type="submit">保存全部设置</button></div></form><section class="card settings-section"><h2>发送测试邮件</h2><p class="small">先保存邮件设置，再发送测试邮件。每分钟最多三次。</p><form id="smtp-test-form" class="form-stack"><div class="settings-test-row"><label class="field"><span class="field-label">测试收件邮箱</span><input name="recipient" type="email" required maxlength="254" autocomplete="email"></label><button type="submit" ${cfg.recovery_enabled?'':'disabled'}>发送测试邮件</button></div><p role="status" aria-live="polite" tabindex="-1" data-test-message></p></form></section></div>`;
    const form=page.querySelector('#site-settings-form');
    const updateRequirements=()=>{const enabled=form.elements.smtp_enabled.checked;['smtp_host','smtp_port','smtp_from','smtp_reset_url'].forEach(name=>{form.elements[name].required=enabled});form.elements.name.required=true;form.elements.public_title.required=true;form.elements.admin_title.required=true;form.elements.hero_title.required=true;form.elements.smtp_port.min='1';form.elements.smtp_port.max='65535';form.elements.smtp_password.disabled=form.elements.clear_smtp_password.checked};
    updateRequirements();form.elements.smtp_enabled.onchange=updateRequirements;form.elements.clear_smtp_password.onchange=()=>{if(form.elements.clear_smtp_password.checked)form.elements.smtp_password.value='';updateRequirements()};
    page.querySelector('[data-smtp-visibility]').onclick=event=>{const button=event.currentTarget;const visible=form.elements.smtp_password.type==='password';form.elements.smtp_password.type=visible?'text':'password';button.textContent=visible?'隐藏':'显示';button.setAttribute('aria-pressed',String(visible))};
    form.onsubmit=async event=>{
      event.preventDefault();if(form._saving||!form.reportValidity())return;
      const values=Object.fromEntries(new FormData(form).entries());const payload={version:cfg.version,site:{},smtp:{enabled:form.elements.smtp_enabled.checked,host:values.smtp_host.trim(),port:Number(values.smtp_port),mode:values.smtp_mode,username:values.smtp_username.trim(),from:values.smtp_from.trim(),reset_url:values.smtp_reset_url.trim()},clear_smtp_password:form.elements.clear_smtp_password.checked};
      for(const name of ['name','public_title','admin_title','description','keywords','website_url','api_domain','subtitle','hero_title','hero_description','announcement','footer','contact_email'])payload.site[name]=String(values[name]||'').trim();
      payload.site.api_base_url=cfg.site.api_base_url||'';
      if(!payload.clear_smtp_password&&form.elements.smtp_password.value)payload.smtp_password=form.elements.smtp_password.value;
      const message=page.querySelector('[data-settings-message]');form._saving=true;const fields=[...form.querySelectorAll('input,select,textarea,button[type=button]')];fields.forEach(field=>field.disabled=true);
      try {await withSubmitting(form,'保存中…',async()=>{await api('/admin/v1/settings',{method:'PUT',body:JSON.stringify(payload)});form.elements.smtp_password.value='';await refreshSiteIdentity();await initRecovery();notice('网站设置已保存并生效。',true);await renderSiteSettings()})}
      catch(error){message.textContent=error.status===409?'其他管理员已更新设置，请刷新后重新编辑。':error.message;message.className='message';message.focus?.()}
      finally {form._saving=false;if(form.isConnected){fields.forEach(field=>field.disabled=false);updateRequirements();}}
    };
    const test=page.querySelector('#smtp-test-form');test.onsubmit=async event=>{event.preventDefault();if(test._saving||!test.reportValidity())return;test._saving=true;const message=page.querySelector('[data-test-message]');try{await withSubmitting(test,'发送中…',async()=>{const result=await api('/admin/v1/settings/smtp/test',{method:'POST',body:JSON.stringify({recipient:test.elements.recipient.value.trim()})});message.textContent=result.message;message.className='message ok'})}catch(error){message.textContent=error.message;message.className='message'}finally{test._saving=false}};
  }catch(error){if(page.isConnected)page.innerHTML=`<div class="empty" role="alert">${esc(error.message)}</div>`}
}
refreshSiteIdentity();

function bindCacheFields(form, id) {
  if(!form) return;
  const plugin=form.elements.plugin, enabled=form.elements.cache_enabled;
  const update=()=>{const available=Boolean(plugin?.value.trim());enabled.disabled=!available;if(!available)enabled.checked=false;for(const name of ['cache_ttl','cache_limit','cache_post'])form.elements[name].disabled=!enabled.checked;};
  plugin?.addEventListener('input',update);enabled?.addEventListener('change',update);update();
  form.querySelector('[data-cache-manage]')?.addEventListener('click',()=>void showPluginCache(id));
}
async function showPluginCache(id) {
  if(!id)return;
  const dialog=document.createElement('dialog');dialog.className='modal-card cache-dialog';
  dialog.innerHTML='<div class="modal-heading"><h2>接口缓存</h2><button type="button" class="modal-close" aria-label="关闭缓存设置">×</button></div><p class="small" data-cache-info>正在读取…</p><form><label class="checkbox-field"><input type="checkbox" name="confirm"><span>我确认清理此接口的全部缓存结果</span></label><p class="message" role="alert"></p><div class="modal-actions"><button type="button" class="secondary" data-cache-close>关闭</button><button type="submit" class="danger" disabled>清理缓存</button></div></form>';
  dialog.setAttribute('aria-label','接口缓存');document.body.appendChild(dialog);dialog.showModal();
  let busy=false;const close=()=>{if(!busy){dialog.close();dialog.remove()}};
  dialog.querySelector('.modal-close').onclick=close;dialog.querySelector('[data-cache-close]').onclick=close;dialog.oncancel=event=>{event.preventDefault();close()};
  const form=dialog.querySelector('form'),confirm=form.elements.confirm,submit=form.querySelector('[type=submit]');
  confirm.disabled=!can('api.write');confirm.onchange=()=>{submit.disabled=!confirm.checked||busy};
  try {const data=await api(`/admin/v1/apis/${encodeURIComponent(id)}/cache`);if(dialog.isConnected)dialog.querySelector('[data-cache-info]').textContent=`有效缓存 ${data.stats.entries} 条，过期缓存 ${data.stats.expired} 条。有效期 ${data.config.ttl_seconds||0} 秒。`;}
  catch(error){if(dialog.isConnected){dialog.querySelector('.message').textContent=error.message;confirm.disabled=true;}}
  form.onsubmit=async event=>{event.preventDefault();if(busy||!confirm.checked)return;busy=true;confirm.disabled=true;
    try {await withSubmitting(form,'清理中…',()=>api(`/admin/v1/apis/${encodeURIComponent(id)}/cache`,{method:'DELETE',body:JSON.stringify({confirm:true})}));busy=false;close();notice('接口缓存已清理',true);}
    catch(error){dialog.querySelector('.message').textContent=error.message;busy=false;confirm.disabled=false;}
  };
}
