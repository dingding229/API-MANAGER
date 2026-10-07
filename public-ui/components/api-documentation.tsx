'use client';
import {LoadingPlaceholder} from './loading-placeholder';

import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { ArrowRight, BookOpen, Check, ChevronDown, ChevronRight, Clipboard, Code2, Download, FileCode2, Folder, KeyRound, ListFilter, Menu, RefreshCw, Search, ShieldCheck, UnlockKeyhole, X, Zap } from 'lucide-react';
import { DocsBody, DocsDescription, DocsTitle } from 'fumadocs-ui/layouts/docs/page';
import { catalogOrigin, defaultSite, exampleValue, filterCatalog, projectCatalog, requestExample, snippet, type ApiDoc, type Catalog, type Field, type SiteInfo, type SnippetLanguage } from '../lib/catalog';

import { TestPanel, useTestingSession, type TestingSession } from '../app/testing';

import { SiteHeader, SiteFooter } from './site-chrome';

const emptyCatalog: Catalog = { version: 1, base_url: '', apis: [] };
const languageLabels: Record<SnippetLanguage, string> = { curl: 'cURL', javascript: 'JavaScript', python: 'Python', go: 'Go' };
const locationLabels = { path: '路径参数', query: '查询参数', header: '请求头参数', body: '请求内容' };
const typeLabels: Record<string, string> = { string: '文本', integer: '整数', number: '数字', boolean: '是或否', array: '列表', object: '对象' };
const authLabel = (auth: ApiDoc['authentication']) => auth === 'none' ? '无需验证' : 'KEY 认证';

function MethodBadge({ method }: { method: string }) { return <span className={`method-badge method-${method.toLowerCase()}`}>{method}</span>; }

function CopyButton({ text, label = '复制', iconOnly = false, disabled = false }: { text: string; label?: string; iconOnly?: boolean; disabled?: boolean }) {
  const [status, setStatus] = useState<'idle' | 'copying' | 'copied' | 'failed'>('idle');
  const feedbackTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const mounted = useRef(true);
  const currentText = useRef(text); currentText.current = text;
  useEffect(() => { clearTimeout(feedbackTimer.current); setStatus('idle'); }, [text]);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; clearTimeout(feedbackTimer.current); }; }, []);
  async function copy() {
    if (status === 'copying') return;
    setStatus('copying');
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable');
      await Promise.race([navigator.clipboard.writeText(text), new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error('timeout')), 1800); })]);
      if (mounted.current) setStatus(currentText.current === text ? 'copied' : 'idle');
    } catch { if (mounted.current) setStatus(currentText.current === text ? 'failed' : 'idle'); }
    finally { clearTimeout(timer); clearTimeout(feedbackTimer.current); feedbackTimer.current = setTimeout(() => { if (mounted.current) setStatus('idle'); }, 2500); }
  }
  const caption = status === 'copying' ? '复制中…' : status === 'copied' ? '已复制' : status === 'failed' ? '请手动复制' : label;
  return <span className="copy-control"><button type="button" className={iconOnly ? 'icon-button' : 'quiet-button copy-button'} aria-label={label} title={caption} onClick={() => void copy()} disabled={disabled || status === 'copying'}>{status === 'copied' ? <Check size={15} aria-hidden="true" /> : <Clipboard size={15} aria-hidden="true" />}{!iconOnly && <span>{caption}</span>}</button><span className="sr-only" role="status" aria-live="polite">{status === 'copied' ? '已复制到剪贴板' : ''}</span>{status === 'failed' && <span className="copy-feedback" role="status">复制不可用，请选中代码手动复制。</span>}</span>;
}

export default function PublicCatalog({guideOnly=false}:{guideOnly?:boolean}) {
  const session = useTestingSession();
  useEffect(()=>{if(!guideOnly&&new URLSearchParams(location.search).get('view')==='guide')location.replace('/guide')},[guideOnly]);
  const [catalog, setCatalog] = useState<Catalog>(emptyCatalog);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const [query, setQuery] = useState('');
  const [method, setMethod] = useState('all');
  const [auth, setAuth] = useState('all');
  const [category, setCategory] = useState('all');
  const [pageOrigin, setPageOrigin] = useState('');
  const [selected, setSelected] = useState('');
  const [view, setView] = useState<'reference' | 'guide'>('reference');
  const [navigationOpen, setNavigationOpen] = useState(false);
  const searchInput = useRef<HTMLInputElement>(null);
  const mobileNavigation = useRef<HTMLDetailsElement>(null);

  useEffect(() => {
    setPageOrigin(window.location.origin);if(new URLSearchParams(location.search).get('view')==='guide')setView('guide');
    const shortcut = (event: KeyboardEvent) => { if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); searchInput.current?.focus(); } };
    document.addEventListener('keydown', shortcut);
    return () => document.removeEventListener('keydown', shortcut);
  }, []);
  useEffect(() => {
    const controller = new AbortController(); let disposed = false;
    const timeout = setTimeout(() => controller.abort(), 12000);
    setLoading(true); setError('');
    void (async () => {
      try {
        const response = await fetch('/catalog.json', { credentials: 'omit', cache: 'no-store', headers: { Accept: 'application/json' }, signal: controller.signal });
        if (!response.ok) throw new Error('catalog unavailable');
        const data = projectCatalog(await response.json());
        if (!disposed) { setCatalog(data); setSelected(previous=>{const target=new URLSearchParams(location.search).get('api');return data.apis.find(a=>a.id===target)?.id||(data.apis.some(a=>a.id===previous)?previous:data.apis[0]?.id||'')}); }
      } catch { if (!disposed) setError('公开目录暂时不可用，请稍后重试。'); }
      finally { clearTimeout(timeout); if (!disposed) setLoading(false); }
    })();
    return () => { disposed = true; clearTimeout(timeout); controller.abort(); };
  }, [reload]);

  const site = catalog.site || defaultSite;
  const baseUrl = catalogOrigin(catalog, pageOrigin);
  const categories = useMemo(() => Array.from(new Set(catalog.apis.map(api => api.category))), [catalog.apis]);
  const methods = useMemo(() => Array.from(new Set(catalog.apis.flatMap(api => api.methods || [api.method]))), [catalog.apis]);
  const filtered = useMemo(() => filterCatalog(catalog.apis, { query, method, authentication: auth, category }), [catalog.apis, query, method, auth, category]);
  const current = filtered.find(api => api.id === selected) || filtered[0];
  useEffect(() => {
    document.title = site.public_title;
    document.querySelector('meta[name="description"]')?.setAttribute('content', site.description);
    let keywords = document.querySelector('meta[name="keywords"]');
    if (!keywords) { keywords = document.createElement('meta'); keywords.setAttribute('name', 'keywords'); document.head.appendChild(keywords); }
    keywords.setAttribute('content', site.keywords);
  }, [site.public_title, site.description, site.keywords]);

  function selectApi(id: string) {
    setSelected(id); setView('reference');
    if (mobileNavigation.current) mobileNavigation.current.open = false;
    requestAnimationFrame(() => document.getElementById('api-documentation')?.scrollIntoView({ block: 'start', behavior: 'auto' }));
  }
  function chooseView(next: 'reference' | 'guide') { if(next==='guide'){location.assign('/guide');return};setView(next); if (mobileNavigation.current) mobileNavigation.current.open = false; }
  function resetFilters() { setQuery(''); setMethod('all'); setAuth('all'); setCategory('all'); }
  function downloadCatalog() {
    const objectUrl = URL.createObjectURL(new Blob([JSON.stringify(catalog, null, 2)], { type: 'application/json' }));
    const anchor = document.createElement('a'); anchor.href = objectUrl; anchor.download = 'public-api-catalog.json';
    document.body.appendChild(anchor); anchor.click(); anchor.remove();
    setTimeout(() => URL.revokeObjectURL(objectUrl), 15000);
  }
  if(guideOnly) return <div className="public-site guide-page"><SiteHeader site={site} session={session} active="guide"/><main className="guide-page-main">{loading?<PageState title="正在读取接入指南…" subtitle="稍后即可查看接入步骤。" loading/>:error?<PageState title="接入指南暂不可用" subtitle={error} retry={()=>setReload(v=>v+1)}/>:<Guide site={site} baseUrl={baseUrl} apis={catalog.apis} onReference={()=>location.assign('/docs')}/>}</main><SiteFooter site={site}/></div>;
  const navigation = <Directory apis={filtered} selected={current?.id || ''} total={catalog.apis.length} loading={loading} categories={categories} methods={methods} query={query} method={method} authentication={auth} category={category} view={view} onSelect={selectApi} onView={chooseView} onMethod={setMethod} onAuth={setAuth} onCategory={setCategory} onReset={resetFilters} />;
  return <div className="public-catalog">
    <a className="skip-link" href="#api-documentation">跳转到接口说明</a>
    <SiteHeader site={site} session={session} active="docs"><label className="header-search"><Search size={16} aria-hidden="true"/><span className="sr-only">搜索接口或参数</span><input ref={searchInput} type="search" value={query} onChange={event=>setQuery(event.target.value)} placeholder="搜索接口或参数" aria-label="搜索接口或参数"/><kbd>⌘ / Ctrl K</kbd></label></SiteHeader>
    <details ref={mobileNavigation} id="mobile-directory" className="mobile-navigation" onToggle={event => setNavigationOpen(event.currentTarget.open)}><summary><Folder size={16} aria-hidden="true" />接口目录<span>{filtered.length} 个接口</span><ChevronDown size={16} aria-hidden="true" /></summary>{navigation}</details>
    <main className={`docs-layout${view === 'guide' ? ' guide-layout' : ''}`}>
      <aside className="directory-panel" aria-label="接口导航">{navigation}</aside>
      {loading ? <PageState title="正在读取公开接口…" subtitle="接口说明与调用示例即将显示。" loading /> : error ? <PageState title="公开目录暂时不可用" subtitle={error} retry={() => setReload(value => value + 1)} /> : view === 'guide' ? <Guide site={site} baseUrl={baseUrl} apis={catalog.apis} onReference={() => chooseView('reference')} /> : !current ? <PageState title={catalog.apis.length ? '没有匹配的接口' : '暂时没有公开接口'} subtitle={catalog.apis.length ? '换一个关键词，或重置请求方法与认证筛选。' : '已公开并发布的接口会显示在这里。'} retry={catalog.apis.length ? resetFilters : undefined} retryLabel="重置筛选" /> : <ApiDocument session={session} key={`${current.id}:${method}:${auth}`} api={current} site={site} baseUrl={baseUrl} preferredMethod={method} preferredAuth={auth} />}
    </main>
    <SiteFooter site={site}/>
  </div>;
}

type DirectoryProps = { apis: ApiDoc[]; selected: string; total: number; loading: boolean; categories: string[]; methods: string[]; query: string; method: string; authentication: string; category: string; view: 'reference' | 'guide'; onSelect: (id: string) => void; onView: (view: 'reference' | 'guide') => void; onMethod: (method: string) => void; onAuth: (auth: string) => void; onCategory: (category: string) => void; onReset: () => void };
function Directory(props: DirectoryProps) {
  const groups = Array.from(new Set(props.apis.map(api => api.category)));
  const filtering = Boolean(props.query || props.method !== 'all' || props.authentication !== 'all' || props.category !== 'all');
  return <div className="directory-inner">
    <div className="directory-views"><strong>接口目录</strong></div>
    <div className="directory-count" role="status" aria-live="polite">{props.loading ? <span className="ui-placeholder-line" aria-hidden="true"/> : `${props.apis.length} / ${props.total} 个公开接口`}<span>{props.categories.length} 个分类</span></div>
    <details className="directory-filters" open={filtering}><summary><ListFilter size={15} aria-hidden="true" />筛选接口<ChevronDown size={14} aria-hidden="true" /></summary><div><label>请求方法<select value={props.method} onChange={event => props.onMethod(event.target.value)}><option value="all">全部方法</option>{props.methods.map(method => <option key={method}>{method}</option>)}</select></label><label>认证方式<select value={props.authentication} onChange={event => props.onAuth(event.target.value)}><option value="all">全部认证方式</option><option value="api_key">KEY 认证</option><option value="none">无需验证</option></select></label><label>接口分类<select value={props.category} onChange={event => props.onCategory(event.target.value)}><option value="all">全部分类</option>{props.categories.map(category => <option key={category}>{category}</option>)}</select></label>{filtering && <button type="button" className="quiet-button filter-reset" onClick={props.onReset}><X size={13} aria-hidden="true" />重置筛选</button>}</div></details>
    <nav className="endpoint-navigation" aria-label="已公开接口">{groups.map(category => <details key={category} className="endpoint-group" open><summary><Folder size={14} aria-hidden="true" /><strong>{category}</strong><span>{props.apis.filter(api => api.category === category).length}</span><ChevronDown size={13} aria-hidden="true" /></summary><div>{props.apis.filter(api => api.category === category).map(api => <button type="button" key={api.id} className={`endpoint-link${props.view === 'reference' && props.selected === api.id ? ' active' : ''}`} aria-current={props.view === 'reference' && props.selected === api.id ? 'page' : undefined} onClick={() => props.onSelect(api.id)}><strong>{api.title}</strong></button>)}</div></details>)}{!props.loading && !props.apis.length && <p className="directory-empty">暂无匹配接口。</p>}</nav>
    <p className="directory-footer"><ShieldCheck size={14} aria-hidden="true" />公开目录不包含管理配置或调用密钥。</p>
  </div>;
}

function PageState({ title, subtitle, loading, retry, retryLabel = '重新加载' }: { title: string; subtitle: string; loading?: boolean; retry?: () => void; retryLabel?: string }) {
  if(loading)return <LoadingPlaceholder className="document-state"/>;
  return <section id="api-documentation" className="document-state" tabIndex={-1} role={loading ? 'status' : 'region'} aria-label={title}><span className="state-icon" aria-hidden="true">{loading ? <RefreshCw size={24} /> : <FileCode2 size={24} />}</span><h1>{title}</h1><p>{subtitle}</p>{retry && <button type="button" className="primary-button" onClick={retry}>{retryLabel}</button>}</section>;
}
function Guide({ site, baseUrl, apis, onReference }: { site: SiteInfo; baseUrl: string; apis: ApiDoc[]; onReference: () => void }) {
  const sample=apis.find(api=>(api.methods||[api.method]).includes('GET'))||apis[0];
  const method=sample?((sample.methods||[sample.method]).includes('GET')?'GET':sample.method):'GET';
  let example='';try {if(sample&&baseUrl)example=snippet(sample,baseUrl,method,'curl')}catch{}
  return <article id="api-documentation" className="guide-panel" tabIndex={-1}>
    <div className="breadcrumb"><BookOpen size={14} aria-hidden="true" />接入指南</div>
    <div className="guide-hero"><div><h1>从首次调用到接入你的程序</h1><p className="guide-description">按照接入步骤准备账号、凭据与参数，再处理响应和常见错误。具体接口字段请查阅接口文档。</p></div><span className="guide-count"><strong>{apis.length}</strong> 个公开接口</span></div>
    {site.announcement&&<p className="public-announcement">{site.announcement}</p>}
    <nav className="guide-nav" aria-label="指南章节"><a href="#guide-start">开始接入</a><a href="#guide-auth">账号与认证</a><a href="#guide-example">调用示例</a><a href="#guide-errors">响应与排查</a><a href="#guide-faq">常见问题</a></nav>
    <div className="guide-origin"><span>接口调用地址</span><code>{baseUrl||<span className="ui-placeholder-line" aria-hidden="true"/>}</code><CopyButton text={baseUrl} label="复制调用地址" disabled={!baseUrl} /></div>
    <section id="guide-start" className="guide-section"><SectionTitle>三步开始接入</SectionTitle><div className="guide-step-grid">
      <div><span>01</span><h3>选择适合的接口</h3><p>按分类、用途或请求路径查找接口。同一接口的多种调用方式合并展示，切换方法后查看对应参数。</p><button type="button" className="quiet-button" onClick={onReference}>浏览接口文档<ArrowRight size={14} aria-hidden="true" /></button></div>
      <div><span>02</span><h3>准备请求参数</h3><p>填写必需的路径、查询、请求头及请求内容。编辑区会同步生成代码，不会自动发送请求。</p><small>写入操作前，请确认数据和调用范围。</small></div>
      <div><span>03</span><h3>测试并接入程序</h3><p>登录后，可对已开放测试的接口发起真实调用，无需填写 KEY。完成确认后，将示例接入自己的程序。</p><small>只有主动确认并发送，才会执行测试。</small></div>
    </div></section>
    <section id="guide-auth" className="guide-section"><SectionTitle>账号登录与接口认证</SectionTitle><div className="guide-auth-grid">
      <div><ShieldCheck size={20} aria-hidden="true" /><h3>网页在线测试</h3><p>前台与后台共用账号登录状态。有权限的用户可测试已展示的接口，使用一次性授权，不显示或保存调用 KEY。</p><ul><li>前后台使用同一 HTTPS 网站地址。</li><li>授权仅适用于当前接口、方法和参数。</li><li>测试可能消耗套餐次数、余额或修改业务数据。</li></ul></div>
      <div><KeyRound size={20} aria-hidden="true" /><h3>外部程序调用</h3><p>标记为 KEY 认证的接口仍需在请求头携带 <code>X-API-Key</code>。登录 Cookie 不能代替 KEY；无需验证的接口可匿名调用。</p><ul><li>使用自己的调用凭据，不写入公开代码。</li><li>配置专用地址时，只使用该调用地址。</li><li>按接口约定发送 JSON 和其他参数。</li></ul></div>
    </div></section>
    <section id="guide-example" className="guide-section"><SectionTitle>从当前接口开始</SectionTitle>{sample?<><div className="guide-example-meta"><strong>{sample.title}</strong><MethodBadge method={method}/><code>{sample.path}</code></div><p>以下示例来自当前公开目录。参数值是接入示例，请在执行前按实际需求调整；需要 KEY 时，在你自己的调用环境中填写。</p>{example?<div className="guide-code"><div><span>cURL 调用示例</span><CopyButton text={example} label="复制示例" /></div><pre tabIndex={0}><code><HighlightedCode code={example}/></code></pre></div>:<p className="section-empty">请打开接口文档，选择调用方式并检查参数后查看示例。</p>}<p className="small">接口文档还提供 JavaScript、Python 和 Go 示例，可按你的调用环境选择。</p></>:<div className="section-empty">暂时没有公开接口。接口发布后，这里会显示对应调用示例。</div>}</section>
    <section id="guide-errors" className="guide-section"><SectionTitle>读懂响应并排查问题</SectionTitle><div className="guide-table-wrap"><table className="guide-table"><thead><tr><th>状态</th><th>说明</th><th>建议处理</th></tr></thead><tbody>
      <tr><td><code>2xx</code></td><td>请求已处理</td><td>按实际响应内容继续处理；当前目录不预设返回数据。</td></tr>
      <tr><td><code>400</code></td><td>参数格式不正确</td><td>检查必填项、数据类型和 JSON 格式。</td></tr>
      <tr><td><code>401 / 403</code></td><td>认证、权限或测试授权不可用</td><td>检查登录状态、KEY、账号权限和接口测试开关。</td></tr>
      <tr><td><code>404 / 421</code></td><td>接口或调用地址不匹配</td><td>刷新目录，并核对路径、方法及实际调用域名。</td></tr>
      <tr><td><code>429</code></td><td>超过频率或额度</td><td>等待限流窗口恢复，减少重复测试，不要持续重试。</td></tr>
      <tr><td><code>5xx</code></td><td>服务暂时无法完成请求</td><td>保存状态码和时间，联系服务维护人员；写入请求不要盲目重试。</td></tr>
    </tbody></table></div><p className="guide-caution">停止等待、超时或无法读取响应，不代表请求未执行。重复提交前请先确认结果，尤其是写入类调用。</p></section>
    <section id="guide-faq" className="guide-section"><SectionTitle>常见问题</SectionTitle><div className="guide-faq">
      <details><summary>为什么登录后仍不能测试某个接口？</summary><p>需要同时满足账号具有测试权限、接口已发布且接口在公开目录展示。旧会话、设备或来源变更时，也可能需要重新登录。</p></details>
      <details><summary>为什么网页测试不用 KEY，程序接入仍要填写？</summary><p>网页测试使用账号范围内的一次性授权，仅适用于选定请求。业务接口的 KEY 要求没有改变，不能复制登录 Cookie 替代程序调用凭据。</p></details>
      <details><summary>如何判断结果是否来自缓存？</summary><p>服务端插件缓存命中时可能返回 <code>X-Plugin-Cache: HIT</code>。缓存有效期由接口管理员配置，浏览器或 CDN 不应缓存业务响应。</p></details>
      <details><summary>前后台登录状态为什么没有同步？</summary><p>请使用同一 HTTPS 主机名，不要混用 IP、不同域名或子域名。确认浏览器允许本站 Cookie，再刷新页面。</p></details>
      <details><summary>在哪里查看完整返回格式？</summary><p>以接口文档的约定和实际响应为准。需要更多说明时，请联系服务维护人员。</p></details>
    </div></section>
    <div className="guide-finish"><div><h2>准备好开始了吗？</h2><p>选择接口，检查参数，再开始一次有据可循的调用。</p></div><button type="button" className="primary-button" onClick={onReference}>浏览接口文档<ArrowRight size={16} aria-hidden="true" /></button></div>
  </article>;
}

export function ApiDocument({ api, site, baseUrl, preferredMethod, preferredAuth, session, testing=false }: { testing?:boolean;session: TestingSession; api: ApiDoc; site: SiteInfo; baseUrl: string; preferredMethod: string; preferredAuth: string }) {
  const methods = api.methods || [api.method];
  const [activeMethod, setActiveMethod] = useState((api.operations || [api]).find(operation => (preferredMethod === 'all' || operation.method === preferredMethod) && (preferredAuth === 'all' || operation.authentication === preferredAuth))?.method || methods[0]);
  const [examples, setExamples] = useState<Record<string, string>>({});
  const [testBusy, setTestBusy] = useState(false);
  const [language, setLanguage] = useState<SnippetLanguage>('curl');
  const operation = api.operations?.find(value => value.method === activeMethod);
  const current = { ...api, ...operation, method: activeMethod };
  const fields = [...current.parameters, ...current.body];
  let code = '', exampleError = '';
  try { code = snippet(api, baseUrl, activeMethod, language, examples); } catch (error) { exampleError = error instanceof Error ? error.message : '请检查参数。'; }
  let requestUrl = `${baseUrl}${api.path}`;
  try { requestUrl = requestExample(api, baseUrl, activeMethod, examples).url; } catch { /* The path template remains readable during validation. */ }
  const headerFields = current.parameters.filter(field => field.location === 'header' && !/^(authorization|x-api-key|cookie|host|content-length|content-type)$/i.test(field.name));
  const changeMethod = (method: string) => { setActiveMethod(method); setExamples({}); };
  const updateExample = (field: Field, value: string) => setExamples(previous => ({ ...previous, [`${field.location}:${field.name}`]: value }));
  const groups = (['path', 'query', 'header', 'body'] as const).map(location => ({ location, fields: fields.filter(field => field.location === location && (location !== 'header' || headerFields.includes(field))) })).filter(group => group.fields.length);
  const exampleOf = (field: Field) => examples[`${field.location}:${field.name}`] ?? formatExample(exampleValue(field, baseUrl));
  const languageKey = `code-${useId().replaceAll(':', '')}`;
  return <><article id="api-documentation" className="document-panel" tabIndex={-1}><DocsBody className="api-doc-body">
    <div className="document-intro"><strong>{site.hero_title.replace(/\n/g, ' ')}</strong><p>{site.hero_description.replace(/\n/g, ' ')}</p></div>{site.announcement && <p className="public-announcement">{site.announcement}</p>}
    <div className="breadcrumb">接口文档<ChevronRight size={13} aria-hidden="true" />{api.category}<ChevronRight size={13} aria-hidden="true" />{api.title}</div>
    <div className="endpoint-address"><MethodBadge method={activeMethod} /><code>{api.path}</code><CopyButton text={`${baseUrl}${api.path}`} label="复制接口地址" iconOnly disabled={!baseUrl} /></div>
    <DocsTitle className="endpoint-title">{api.title}</DocsTitle><DocsDescription className="endpoint-description">{api.summary || '查看接口的请求参数与调用方式。'}</DocsDescription>
    <a className="mobile-example-link" href="#request-examples"><Code2 size={15} aria-hidden="true" />查看调用示例<ArrowRight size={14} aria-hidden="true" /></a><div className="endpoint-meta"><span><Folder size={13} aria-hidden="true" />{api.category}</span><span>{current.authentication === 'none' ? <UnlockKeyhole size={13} aria-hidden="true" /> : <KeyRound size={13} aria-hidden="true" />}{authLabel(current.authentication)}</span></div>
    {methods.length > 1 && <section className="document-section"><SectionTitle>调用方式</SectionTitle><div className="method-choices" role="group" aria-label="选择调用方式">{methods.map(method => <button type="button" key={method} onClick={() => changeMethod(method)} aria-pressed={activeMethod === method}><MethodBadge method={method} /></button>)}</div></section>}
    <section className="document-section"><p className="call-price">每次成功调用：{(current.price_micros||0)>0?'¥ '+((current.price_micros||0)/1000000).toLocaleString('zh-CN',{maximumFractionDigits:6}):'免费'}。有效套餐可覆盖调用费用。</p><SectionTitle>请求标头<span>{current.body.length ? 'application/json' : '按接口要求填写'}</span></SectionTitle><div className="header-list">{current.authentication === 'api_key' && <div className="header-entry"><div><code>X-API-Key</code><span className="required-tag">程序调用必填</span></div><p>外部程序调用时填写自己的密钥；已开放的网页测试使用账号授权，无需填写 KEY。</p></div>}{current.body.length > 0 && <div className="header-entry"><div><code>Content-Type</code><span className="field-type">application/json</span></div><p>请求内容使用 JSON 格式。</p></div>}{headerFields.map((field, index) => <div className="header-entry" key={`${field.name}-${index}`}><div><code>{field.name}</code><span className={field.required ? 'required-tag' : 'optional-tag'}>{field.required ? '必填' : '可选'}</span></div><p>{typeLabels[field.type]}参数，可在右侧编辑示例值。</p></div>)}{current.authentication === 'none' && !current.body.length && !headerFields.length && <div className="section-empty"><UnlockKeyhole size={16} aria-hidden="true" />此接口无需调用密钥，未声明额外请求标头。</div>}</div></section>
    {groups.filter(group => group.location !== 'header').map(group => <section className="document-section" key={group.location}><SectionTitle>{locationLabels[group.location]}<span>{group.fields.length} 项参数</span></SectionTitle><div className="parameter-list">{group.fields.map((field, index) => <div className="parameter-row" key={`${field.name}-${index}`}><div className="parameter-definition"><code>{field.name}</code><span className="field-type">{field.type}</span><span className={field.required ? 'required-tag' : 'optional-tag'}>{field.required ? '必填' : '可选'}</span></div><p>{field.location === 'path' ? '替换请求路径中同名的占位参数。' : `${typeLabels[field.type]}类型的${locationLabels[field.location]}。`}</p></div>)}</div></section>)}
    {!groups.length && <section className="document-section"><SectionTitle>请求参数</SectionTitle><div className="section-empty">未声明额外参数，可参考调用示例接入。</div></section>}
    <section className="document-section response-notes"><SectionTitle>返回说明</SectionTitle><p>当前公开目录未提供响应示例。返回内容与状态码以接口的实际响应为准。</p><div><ShieldCheck size={16} aria-hidden="true" /><span>公开说明不包含内部转发地址、预设响应内容或访问密钥。</span></div></section>
  </DocsBody></article>
  <aside id="request-examples" className="examples-panel" aria-label={testing ? "调用示例与参数编辑" : "调用示例"}>
    <section className="request-code-card"><div className="sample-section-title"><h2>调用示例</h2><MethodBadge method={activeMethod}/></div><div className="code-card-heading"><div className="language-tabs" role="tablist" aria-label="调用示例语言">{(Object.keys(languageLabels) as SnippetLanguage[]).map((value, index, list) => <button type="button" role="tab" id={`${languageKey}-${value}`} key={value} aria-selected={language === value} aria-controls={`${languageKey}-panel`} tabIndex={language === value ? 0 : -1} onClick={() => setLanguage(value)} onKeyDown={event => { let target: SnippetLanguage | undefined; if (event.key === 'ArrowRight') target = list[(index + 1) % list.length]; if (event.key === 'ArrowLeft') target = list[(index + list.length - 1) % list.length]; if (event.key === 'Home') target = list[0]; if (event.key === 'End') target = list.at(-1); if (target) { event.preventDefault(); setLanguage(target); document.getElementById(`${languageKey}-${target}`)?.focus(); } }}>{value === 'javascript' ? 'JS' : languageLabels[value]}</button>)}</div><CopyButton text={code} label="复制调用代码" iconOnly disabled={Boolean(exampleError) || !code} /></div><div id={`${languageKey}-panel`} className="snippet-panel" role="tabpanel" aria-labelledby={`${languageKey}-${language}`}>{exampleError ? <p className="example-error" role="alert">{exampleError}</p> : <pre tabIndex={0} aria-label={`${languageLabels[language]} 调用代码`}><code><HighlightedCode code={code} /></code></pre>}</div><div className="code-language-caption"><Code2 size={13} aria-hidden="true" />{languageLabels[language]} 示例<span>{activeMethod}</span></div></section>
    {testing&&<section className="parameter-editor"><div className="editor-heading"><span className="editor-dot" aria-hidden="true" /><h2>编辑调用参数</h2><span className="read-only-tag">仅生成示例</span></div><p>修改参数后，代码与调用地址同步更新。编辑参数不会发送实际请求。</p>{fields.length ? <div className="example-fields">{fields.filter(field => field.location !== 'header' || headerFields.includes(field)).map((field, index) => <label key={`${field.location}-${field.name}-${index}`}><span><code>{field.name}</code><small>{locationLabels[field.location]} · {field.type}{field.required && ' · 必填'}</small></span>{field.type === 'boolean' ? <select aria-label={`${field.name} 示例`} value={exampleOf(field)} disabled={testBusy} onChange={event => updateExample(field, event.target.value)}><option value="true">true</option><option value="false">false</option></select> : ['object', 'array'].includes(field.type) ? <textarea aria-label={`${field.name} 示例`} value={exampleOf(field)} maxLength={500} rows={2} spellCheck={false} disabled={testBusy} onChange={event => updateExample(field, event.target.value)} /> : <input aria-label={`${field.name} 示例`} value={exampleOf(field)} maxLength={500} spellCheck={false} disabled={testBusy} onChange={event => updateExample(field, event.target.value)} />}</label>)}</div> : <div className="editor-empty">此接口未声明可编辑参数。</div>}<button type="button" className="quiet-button reset-examples" onClick={() => setExamples({})} disabled={testBusy || !Object.keys(examples).length}><RefreshCw size={14} aria-hidden="true" />恢复默认示例</button></section>}
    {testing?<TestPanel onBusyChange={setTestBusy} api={api} origin={baseUrl} method={activeMethod} values={examples} session={session}/>:session.username&&session.canTest&&(session.allowedApiIds===null||session.allowedApiIds.includes(api.id))&&current.test_enabled?<section className="testing-entry"><h2>在线测试</h2><p>在独立页面填写参数并查看返回结果。</p><a className="primary-button" href={'/playground?api='+encodeURIComponent(api.id)}>打开在线测试</a></section>:null}
    <section className="request-preview-card"><div><h2>完整调用地址</h2><CopyButton text={requestUrl} label="复制完整调用地址" iconOnly disabled={!baseUrl || Boolean(exampleError)} /></div><code>{requestUrl}</code></section><div className="integration-note"><KeyRound size={15} aria-hidden="true" /><p>{current.authentication === 'none' ? '无需验证，可直接在你的调用环境中执行示例。' : '调用前，请将示例中的密钥提示替换为你自己的调用密钥。'}</p></div>
  </aside></>;
}
function SectionTitle({ children }: { children: ReactNode }) { return <div className="section-title"><h2>{children}</h2></div>; }
function formatExample(value: unknown): string { return typeof value === 'string' ? value : JSON.stringify(value); }
function HighlightedCode({ code }: { code: string }) {
  const tokens = code.split(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\b(?:const|await|new|throw|if|import|from|with|as|func|package|defer|return|nil|true|false)\b|\b\d+(?:\.\d+)?\b)/g);
  return <>{tokens.map((value, index) => { const token = /^["']/.test(value) ? 'string' : /^\d/.test(value) ? 'number' : /^(const|await|new|throw|if|import|from|with|as|func|package|defer|return|nil|true|false)$/.test(value) ? 'keyword' : ''; return token ? <span className={`syntax-${token}`} key={index}>{value}</span> : value; })}</>;
}
