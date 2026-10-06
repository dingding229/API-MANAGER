'use client';

import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from 'react';
import { ArrowRight, BookOpen, Check, ChevronDown, ChevronRight, Clipboard, Code2, Download, FileCode2, Folder, KeyRound, ListFilter, Menu, RefreshCw, Search, ShieldCheck, UnlockKeyhole, X, Zap } from 'lucide-react';
import { DocsBody, DocsDescription, DocsTitle } from 'fumadocs-ui/layouts/docs/page';
import { catalogOrigin, defaultSite, exampleValue, filterCatalog, projectCatalog, requestExample, snippet, type ApiDoc, type Catalog, type Field, type SiteInfo, type SnippetLanguage } from '../lib/catalog';

import { SessionControl, TestPanel, useTestingSession, type TestingSession } from './testing';

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

export default function PublicCatalog() {
  const session = useTestingSession();
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
    setPageOrigin(window.location.origin);
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
        if (!disposed) { setCatalog(data); setSelected(previous => data.apis.some(api => api.id === previous) ? previous : data.apis[0]?.id || ''); }
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
  function chooseView(next: 'reference' | 'guide') { setView(next); if (mobileNavigation.current) mobileNavigation.current.open = false; }
  function resetFilters() { setQuery(''); setMethod('all'); setAuth('all'); setCategory('all'); }
  function downloadCatalog() {
    const objectUrl = URL.createObjectURL(new Blob([JSON.stringify(catalog, null, 2)], { type: 'application/json' }));
    const anchor = document.createElement('a'); anchor.href = objectUrl; anchor.download = 'public-api-catalog.json';
    document.body.appendChild(anchor); anchor.click(); anchor.remove();
    setTimeout(() => URL.revokeObjectURL(objectUrl), 15000);
  }
  const navigation = <Directory apis={filtered} selected={current?.id || ''} total={catalog.apis.length} loading={loading} categories={categories} methods={methods} query={query} method={method} authentication={auth} category={category} view={view} onSelect={selectApi} onView={chooseView} onMethod={setMethod} onAuth={setAuth} onCategory={setCategory} onReset={resetFilters} />;
  return <div className="public-catalog">
    <a className="skip-link" href="#api-documentation">跳转到接口说明</a>
    <header className="docs-header">
      <a className="docs-brand" href="/" aria-label={`${site.name} ${site.subtitle}`}><span className="docs-brand-icon" aria-hidden="true"><Zap size={19} strokeWidth={2.4} /></span><span className="docs-brand-name">{site.name}<small>{site.subtitle}</small></span><span className="header-label">API 文档</span></a>
      <label className="global-search"><Search size={17} aria-hidden="true" /><span className="sr-only">搜索接口或参数</span><input ref={searchInput} type="search" aria-label="搜索接口或参数" value={query} onChange={event => { setQuery(event.target.value); setView('reference'); }} placeholder="搜索接口、参数或请求路径…" /><kbd>⌘ / Ctrl K</kbd></label>
      <div className="header-actions"><SessionControl session={session} /><span className="header-origin" title={baseUrl}><span>调用地址</span><code>{baseUrl || '正在读取…'}</code></span><button type="button" className="quiet-button download-button" aria-label="下载公开目录" onClick={downloadCatalog} disabled={loading || Boolean(error) || !catalog.apis.length}><Download size={15} aria-hidden="true" /><span>下载目录</span></button><button type="button" className="icon-button refresh-button" aria-label="刷新公开目录" title="刷新公开目录" onClick={() => setReload(value => value + 1)} disabled={loading}><RefreshCw size={16} aria-hidden="true" /></button><button type="button" className="icon-button mobile-menu-toggle" aria-label={navigationOpen ? '收起接口导航' : '展开接口导航'} aria-expanded={navigationOpen} aria-controls="mobile-directory" onClick={() => { const navigation = mobileNavigation.current; if (navigation) { navigation.open = !navigation.open; if (navigation.open) requestAnimationFrame(() => navigation.scrollIntoView({block:'start',behavior:'auto'})); } }}><Menu size={19} aria-hidden="true" /></button></div>
    </header>
    <details ref={mobileNavigation} id="mobile-directory" className="mobile-navigation" onToggle={event => setNavigationOpen(event.currentTarget.open)}><summary><Folder size={16} aria-hidden="true" />接口目录<span>{filtered.length} 个接口</span><ChevronDown size={16} aria-hidden="true" /></summary>{navigation}</details>
    <main className={`docs-layout${view === 'guide' ? ' guide-layout' : ''}`}>
      <aside className="directory-panel" aria-label="接口导航">{navigation}</aside>
      {loading ? <PageState title="正在读取公开接口…" subtitle="接口说明与调用示例即将显示。" loading /> : error ? <PageState title="公开目录暂时不可用" subtitle={error} retry={() => setReload(value => value + 1)} /> : view === 'guide' ? <Guide site={site} baseUrl={baseUrl} apiCount={catalog.apis.length} onReference={() => chooseView('reference')} /> : !current ? <PageState title={catalog.apis.length ? '没有匹配的接口' : '暂时没有公开接口'} subtitle={catalog.apis.length ? '换一个关键词，或重置请求方法与认证筛选。' : '已公开并发布的接口会显示在这里。'} retry={catalog.apis.length ? resetFilters : undefined} retryLabel="重置筛选" /> : <Document session={session} key={`${current.id}:${method}:${auth}`} api={current} site={site} baseUrl={baseUrl} preferredMethod={method} preferredAuth={auth} />}
    </main>
    <footer className="docs-footer"><strong>{site.name}</strong><span>{site.footer}{site.contact_email && <> · <a href={`mailto:${encodeURIComponent(site.contact_email).replace('%40', '@')}`}>{site.contact_email}</a></>}</span><span>仅展示已公开的接口</span></footer>
  </div>;
}

type DirectoryProps = { apis: ApiDoc[]; selected: string; total: number; loading: boolean; categories: string[]; methods: string[]; query: string; method: string; authentication: string; category: string; view: 'reference' | 'guide'; onSelect: (id: string) => void; onView: (view: 'reference' | 'guide') => void; onMethod: (method: string) => void; onAuth: (auth: string) => void; onCategory: (category: string) => void; onReset: () => void };
function Directory(props: DirectoryProps) {
  const groups = Array.from(new Set(props.apis.map(api => api.category)));
  const filtering = Boolean(props.query || props.method !== 'all' || props.authentication !== 'all' || props.category !== 'all');
  return <div className="directory-inner">
    <div className="directory-views" role="group" aria-label="目录视图"><button type="button" aria-pressed={props.view === 'reference'} onClick={() => props.onView('reference')}><FileCode2 size={15} aria-hidden="true" />接口文档</button><button type="button" aria-pressed={props.view === 'guide'} onClick={() => props.onView('guide')}><BookOpen size={15} aria-hidden="true" />接入指南</button></div>
    <div className="directory-count" role="status" aria-live="polite">{props.loading ? '正在加载…' : `${props.apis.length} / ${props.total} 个公开接口`}<span>{props.categories.length} 个分类</span></div>
    <details className="directory-filters" open={filtering}><summary><ListFilter size={15} aria-hidden="true" />筛选接口<ChevronDown size={14} aria-hidden="true" /></summary><div><label>请求方法<select value={props.method} onChange={event => props.onMethod(event.target.value)}><option value="all">全部方法</option>{props.methods.map(method => <option key={method}>{method}</option>)}</select></label><label>认证方式<select value={props.authentication} onChange={event => props.onAuth(event.target.value)}><option value="all">全部认证方式</option><option value="api_key">KEY 认证</option><option value="none">无需验证</option></select></label><label>接口分类<select value={props.category} onChange={event => props.onCategory(event.target.value)}><option value="all">全部分类</option>{props.categories.map(category => <option key={category}>{category}</option>)}</select></label>{filtering && <button type="button" className="quiet-button filter-reset" onClick={props.onReset}><X size={13} aria-hidden="true" />重置筛选</button>}</div></details>
    <nav className="endpoint-navigation" aria-label="已公开接口">{groups.map(category => <details key={category} className="endpoint-group" open><summary><Folder size={14} aria-hidden="true" /><strong>{category}</strong><span>{props.apis.filter(api => api.category === category).length}</span><ChevronDown size={13} aria-hidden="true" /></summary><div>{props.apis.filter(api => api.category === category).map(api => { const methods = api.methods || [api.method]; const first = props.method !== 'all' && methods.includes(props.method) ? props.method : methods[0]; return <button type="button" key={api.id} className={`endpoint-link${props.view === 'reference' && props.selected === api.id ? ' active' : ''}`} aria-current={props.view === 'reference' && props.selected === api.id ? 'page' : undefined} onClick={() => props.onSelect(api.id)}><span><strong>{api.title}</strong><code>{api.path}</code></span><span className="endpoint-link-methods" title={methods.join(' / ')}><MethodBadge method={first} />{methods.length > 1 && <small>+{methods.length - 1}</small>}</span></button>; })}</div></details>)}{!props.loading && !props.apis.length && <p className="directory-empty">暂无匹配接口。</p>}</nav>
    <p className="directory-footer"><ShieldCheck size={14} aria-hidden="true" />公开目录不包含管理配置或调用密钥。</p>
  </div>;
}

function PageState({ title, subtitle, loading, retry, retryLabel = '重新加载' }: { title: string; subtitle: string; loading?: boolean; retry?: () => void; retryLabel?: string }) {
  return <section id="api-documentation" className="document-state" tabIndex={-1} role={loading ? 'status' : 'region'} aria-label={title}><span className="state-icon" aria-hidden="true">{loading ? <RefreshCw size={24} /> : <FileCode2 size={24} />}</span><h1>{title}</h1><p>{subtitle}</p>{retry && <button type="button" className="primary-button" onClick={retry}>{retryLabel}</button>}</section>;
}
function Guide({ site, baseUrl, apiCount, onReference }: { site: SiteInfo; baseUrl: string; apiCount: number; onReference: () => void }) {
  return <article id="api-documentation" className="guide-panel" tabIndex={-1}><div className="breadcrumb"><BookOpen size={14} aria-hidden="true" />接入指南</div><h1>{site.hero_title}</h1><p className="guide-description">{site.hero_description}</p>{site.announcement && <p className="public-announcement">{site.announcement}</p>}<div className="guide-origin"><span>接口调用地址</span><code>{baseUrl || '正在读取…'}</code><CopyButton text={baseUrl} label="复制调用地址" disabled={!baseUrl} /></div><div className="guide-steps"><section><span>01</span><div><h2>选择接口与调用方式</h2><p>目录中有 {apiCount} 个公开接口。同一接口支持多种调用方式时，切换请求方法即可查看对应参数。</p></div></section><section><span>02</span><div><h2>按接口要求填写参数</h2><p>区分路径、查询、请求头与请求内容中的参数。右侧编辑会更新示例；只有登录并确认后，点击“发送请求”才会进行真实调用。</p></div></section><section><span>03</span><div><h2>使用自己的调用密钥</h2><p>需要认证的接口，在请求头中添加 <code>X-API-Key</code>；标记为“无需验证”的接口不需要密钥。</p></div></section><section><span>04</span><div><h2>复制示例开始接入</h2><p>调用示例支持 cURL、JavaScript、Python 与 Go。返回内容以接口的实际响应为准。</p></div></section></div><button type="button" className="primary-button" onClick={onReference}>浏览接口文档<ArrowRight size={16} aria-hidden="true" /></button></article>;
}

function Document({ api, site, baseUrl, preferredMethod, preferredAuth, session }: { session: TestingSession; api: ApiDoc; site: SiteInfo; baseUrl: string; preferredMethod: string; preferredAuth: string }) {
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
    <section className="document-section"><SectionTitle>请求标头<span>{current.body.length ? 'application/json' : '按接口要求填写'}</span></SectionTitle><div className="header-list">{current.authentication === 'api_key' && <div className="header-entry"><div><code>X-API-Key</code><span className="required-tag">必填</span></div><p>在你的调用环境中填写自己的密钥。此页面不会保存密钥。</p></div>}{current.body.length > 0 && <div className="header-entry"><div><code>Content-Type</code><span className="field-type">application/json</span></div><p>请求内容使用 JSON 格式。</p></div>}{headerFields.map((field, index) => <div className="header-entry" key={`${field.name}-${index}`}><div><code>{field.name}</code><span className={field.required ? 'required-tag' : 'optional-tag'}>{field.required ? '必填' : '可选'}</span></div><p>{typeLabels[field.type]}参数，可在右侧编辑示例值。</p></div>)}{current.authentication === 'none' && !current.body.length && !headerFields.length && <div className="section-empty"><UnlockKeyhole size={16} aria-hidden="true" />此接口无需调用密钥，未声明额外请求标头。</div>}</div></section>
    {groups.filter(group => group.location !== 'header').map(group => <section className="document-section" key={group.location}><SectionTitle>{locationLabels[group.location]}<span>{group.fields.length} 项参数</span></SectionTitle><div className="parameter-list">{group.fields.map((field, index) => <div className="parameter-row" key={`${field.name}-${index}`}><div className="parameter-definition"><code>{field.name}</code><span className="field-type">{field.type}</span><span className={field.required ? 'required-tag' : 'optional-tag'}>{field.required ? '必填' : '可选'}</span></div><p>{field.location === 'path' ? '替换请求路径中同名的占位参数。' : `${typeLabels[field.type]}类型的${locationLabels[field.location]}。`}</p><span className="parameter-example" title={exampleOf(field)}>示例：<code>{exampleOf(field)}</code></span></div>)}</div></section>)}
    {!groups.length && <section className="document-section"><SectionTitle>请求参数</SectionTitle><div className="section-empty">未声明额外参数，可参考调用示例接入。</div></section>}
    <section className="document-section response-notes"><SectionTitle>返回说明</SectionTitle><p>当前公开目录未提供响应示例。返回内容与状态码以接口的实际响应为准。</p><div><ShieldCheck size={16} aria-hidden="true" /><span>公开说明不包含内部转发地址、预设响应内容或访问密钥。</span></div></section>
  </DocsBody></article>
  <aside id="request-examples" className="examples-panel" aria-label="调用示例与参数编辑">
    <section className="request-code-card"><div className="code-card-heading"><div className="language-tabs" role="tablist" aria-label="调用示例语言">{(Object.keys(languageLabels) as SnippetLanguage[]).map((value, index, list) => <button type="button" role="tab" id={`${languageKey}-${value}`} key={value} aria-selected={language === value} aria-controls={`${languageKey}-panel`} tabIndex={language === value ? 0 : -1} onClick={() => setLanguage(value)} onKeyDown={event => { let target: SnippetLanguage | undefined; if (event.key === 'ArrowRight') target = list[(index + 1) % list.length]; if (event.key === 'ArrowLeft') target = list[(index + list.length - 1) % list.length]; if (event.key === 'Home') target = list[0]; if (event.key === 'End') target = list.at(-1); if (target) { event.preventDefault(); setLanguage(target); document.getElementById(`${languageKey}-${target}`)?.focus(); } }}>{value === 'javascript' ? 'JS' : languageLabels[value]}</button>)}</div><CopyButton text={code} label="复制调用代码" iconOnly disabled={Boolean(exampleError) || !code} /></div><div id={`${languageKey}-panel`} className="snippet-panel" role="tabpanel" aria-labelledby={`${languageKey}-${language}`}>{exampleError ? <p className="example-error" role="alert">{exampleError}</p> : <pre tabIndex={0} aria-label={`${languageLabels[language]} 调用代码`}><code><HighlightedCode code={code} /></code></pre>}</div><div className="code-language-caption"><Code2 size={13} aria-hidden="true" />{languageLabels[language]} 示例<span>{activeMethod}</span></div></section>
    <section className="parameter-editor"><div className="editor-heading"><span className="editor-dot" aria-hidden="true" /><h2>编辑调用参数</h2><span className="read-only-tag">仅生成示例</span></div><p>修改参数后，代码与调用地址同步更新。编辑参数不会发送实际请求。</p>{fields.length ? <div className="example-fields">{fields.filter(field => field.location !== 'header' || headerFields.includes(field)).map((field, index) => <label key={`${field.location}-${field.name}-${index}`}><span><code>{field.name}</code><small>{locationLabels[field.location]} · {field.type}{field.required && ' · 必填'}</small></span>{field.type === 'boolean' ? <select aria-label={`${field.name} 示例`} value={exampleOf(field)} disabled={testBusy} onChange={event => updateExample(field, event.target.value)}><option value="true">true</option><option value="false">false</option></select> : ['object', 'array'].includes(field.type) ? <textarea aria-label={`${field.name} 示例`} value={exampleOf(field)} maxLength={500} rows={2} spellCheck={false} disabled={testBusy} onChange={event => updateExample(field, event.target.value)} /> : <input aria-label={`${field.name} 示例`} value={exampleOf(field)} maxLength={500} spellCheck={false} disabled={testBusy} onChange={event => updateExample(field, event.target.value)} />}</label>)}</div> : <div className="editor-empty">此接口未声明可编辑参数。</div>}<button type="button" className="quiet-button reset-examples" onClick={() => setExamples({})} disabled={testBusy || !Object.keys(examples).length}><RefreshCw size={14} aria-hidden="true" />恢复默认示例</button></section>
    <TestPanel onBusyChange={setTestBusy} api={api} origin={baseUrl} method={activeMethod} values={examples} session={session} />
    <section className="request-preview-card"><div><h2>完整调用地址</h2><CopyButton text={requestUrl} label="复制完整调用地址" iconOnly disabled={!baseUrl || Boolean(exampleError)} /></div><code>{requestUrl}</code></section><div className="integration-note"><KeyRound size={15} aria-hidden="true" /><p>{current.authentication === 'none' ? '无需验证，可直接在你的调用环境中执行示例。' : '调用前，请将示例中的密钥提示替换为你自己的调用密钥。'}</p></div>
  </aside></>;
}
function SectionTitle({ children }: { children: ReactNode }) { return <div className="section-title"><h2>{children}</h2></div>; }
function formatExample(value: unknown): string { return typeof value === 'string' ? value : JSON.stringify(value); }
function HighlightedCode({ code }: { code: string }) {
  const tokens = code.split(/("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|\b(?:const|await|new|throw|if|import|from|with|as|func|package|defer|return|nil|true|false)\b|\b\d+(?:\.\d+)?\b)/g);
  return <>{tokens.map((value, index) => { const token = /^["']/.test(value) ? 'string' : /^\d/.test(value) ? 'number' : /^(const|await|new|throw|if|import|from|with|as|func|package|defer|return|nil|true|false)$/.test(value) ? 'keyword' : ''; return token ? <span className={`syntax-${token}`} key={index}>{value}</span> : value; })}</>;
}
