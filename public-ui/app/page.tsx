'use client';

import { useEffect, useMemo, useState } from 'react';
import { Clipboard, Code2, Folder, KeyRound, LockKeyhole, Search, ShieldCheck, UnlockKeyhole } from 'lucide-react';
import { DocsBody, DocsTitle, DocsDescription } from 'fumadocs-ui/layouts/docs/page';

import { command, projectCatalog, type ApiDoc, type Catalog } from '../lib/catalog';

const emptyCatalog: Catalog = { version: 1, base_url: '', apis: [] };
const authLabel = (auth: ApiDoc['authentication']) => auth === 'none' ? '无需验证' : 'KEY 认证';
const methodClass = (method: string) => method === 'GET' ? 'method-get' : method === 'POST' ? 'method-post' : 'method-other';

export default function PublicCatalog() {
  const [catalog, setCatalog] = useState<Catalog>(emptyCatalog);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');
  const [method, setMethod] = useState('all');
  const [auth, setAuth] = useState('all');
  const [category, setCategory] = useState('all');
  const [selected, setSelected] = useState('');

  async function load() {
    setLoading(true); setError('');
    try {
      const response = await fetch('/catalog.json', { credentials: 'omit', cache: 'no-store', headers: { Accept: 'application/json' } });
      const data = projectCatalog(await response.json());
      if (!response.ok || data.version !== 1 || !Array.isArray(data.apis)) throw new Error('catalog unavailable');
      setCatalog(data); setSelected((current) => current || data.apis[0]?.id || '');
    } catch { setError('公开目录暂时不可用'); } finally { setLoading(false); }
  }
  useEffect(() => { void load(); }, []);

  const categories = useMemo(() => Array.from(new Set(catalog.apis.map((api) => api.category))), [catalog.apis]);
  const methods = useMemo(() => Array.from(new Set(catalog.apis.map((api) => api.method))), [catalog.apis]);
  const filtered = useMemo(() => catalog.apis.filter((api) => [api.title, api.summary, api.path, api.category].join(' ').toLowerCase().includes(query.toLowerCase()) && (method === 'all' || api.method === method) && (auth === 'all' || api.authentication === auth) && (category === 'all' || api.category === category)), [catalog.apis, query, method, auth, category]);
  const current = filtered.find((api) => api.id === selected) || filtered[0];

  return <>
    <header className="site-header"><div className="header-inner"><a className="brand" href="/" aria-label="API Manager 开放接口目录"><span className="brand-mark" aria-hidden="true" /><span>API Manager<small>开放接口目录</small></span></a><span className="header-note"><i />只读调用文档</span></div></header>
    <main>
      <section className="hero"><div><span className="kicker">面向开发者的接口文档</span><h1>找到接口，<br />开始你的下一次调用。</h1><p>从用途到参数，从认证方式到调用示例。<br />让接口接入清晰、直接、有据可循。</p></div><div className="hero-mark" aria-hidden="true">{'{'}<span /><span />{'}'}</div></section>
      <section className="toolbar" aria-label="搜索及筛选接口"><label className="search"><Search size={19} /><span className="sr-only">搜索接口</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索接口名称、路径或用途" /></label><select aria-label="请求方法" value={method} onChange={(event) => setMethod(event.target.value)}><option value="all">全部请求方法</option>{methods.map((value) => <option key={value}>{value}</option>)}</select><select aria-label="认证方式" value={auth} onChange={(event) => setAuth(event.target.value)}><option value="all">全部认证方式</option><option value="api_key">KEY 认证</option><option value="none">无需验证</option></select></section>
      <div className="catalog-meta" role="status" aria-live="polite"><span>{loading ? '正在读取公开接口…' : `${catalog.apis.length} 个公开接口${filtered.length !== catalog.apis.length ? `，当前找到 ${filtered.length} 个` : ''}`}</span><span>{categories.length} 个分类</span></div>
      {error ? <div className="error-state" role="alert"><h2>{error}</h2><button onClick={() => void load()}>重新读取目录</button></div> : <section className="workspace"><aside className="sidebar"><div className="sidebar-title"><strong>接口分类</strong><span>{catalog.apis.length}</span></div><nav aria-label="接口分类">{[['all', '全部接口', catalog.apis.length], ...categories.map((value) => [value, value, catalog.apis.filter((api) => api.category === value).length])].map(([id, label, count]) => <button key={id} className={category === id ? 'active' : ''} onClick={() => setCategory(String(id))} aria-pressed={category === id}><Folder size={15} />{label}<em>{count}</em></button>)}</nav><div className="sidebar-note"><ShieldCheck size={17} /><p>公开文档，不公开凭据。<br />KEY 请自行妥善保管。</p></div></aside><section className="endpoint-list"><div className="column-title"><strong>{category === 'all' ? '全部接口' : category}</strong><span>{filtered.length}</span></div>{!filtered.length ? <div className="empty">没有匹配的接口。<br />试试其他关键词或筛选条件。</div> : filtered.map((api) => <button key={api.id} className={`endpoint ${current?.id === api.id ? 'active' : ''}`} onClick={() => setSelected(api.id)} aria-pressed={current?.id === api.id}><span className="endpoint-head"><strong>{api.title}</strong><b className={methodClass(api.method)}>{api.method}</b></span><code>{api.path}</code><p>{api.summary || '查看参数与调用示例'}</p><small>{api.authentication === 'none' ? <UnlockKeyhole size={11} /> : <LockKeyhole size={11} />}{authLabel(api.authentication)} · 已公开</small></button>)}</section><section className="document" aria-label="接口文档">{current ? <Document key={current.id} api={current} baseUrl={catalog.base_url} /> : <div className="initial"><Code2 size={42} /><h2>选择一个接口</h2><p>公开接口的调用文档会显示在这里。</p></div>}</section></section>}
      <section className="note"><span>01</span><div><strong>先读文档，再开始接入</strong><p>示例中的域名、参数和 KEY 均为占位内容。页面不会收集、保存或测试你的 KEY。</p></div></section>
    </main><footer><strong>API Manager</strong><span>仅展示已授权公开的接口信息</span></footer>
  </>;
}

function Document({ api, baseUrl }: { api: ApiDoc; baseUrl: string }) {
  const [copied, setCopied] = useState(false);
  const [copying, setCopying] = useState(false);
  const [copyError, setCopyError] = useState('');
  const code = command(api, baseUrl);
  const copy = async () => {
    setCopying(true); setCopyError('');
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable');
      await Promise.race([
        navigator.clipboard.writeText(code),
        new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error('clipboard timeout')), 1800); }),
      ]);
      setCopied(true); setTimeout(() => setCopied(false), 2200);
    } catch { setCopyError('复制不可用，请手动选中示例复制。'); }
    finally { clearTimeout(timer); setCopying(false); }
  };
  const fields = [...api.parameters, ...api.body];
  return <DocsBody><div className="breadcrumb">接口目录 / {api.category}</div><div className="document-title"><div><span className={`method ${methodClass(api.method)}`}>{api.method}</span><DocsTitle>{api.title}</DocsTitle></div><span>{api.category}</span></div><DocsDescription className="summary">{api.summary || '查看请求方式、参数及调用示例。'}</DocsDescription><code className="path">{api.path}</code><section><h3>{api.authentication === 'none' ? <UnlockKeyhole size={16} /> : <KeyRound size={16} />}认证方式</h3><div className="auth-card"><strong>{authLabel(api.authentication)}</strong><p>{api.authentication === 'none' ? '此接口已明确允许匿名访问。' : <>在请求头中加入 <code>X-API-Key</code>，不要使用管理登录会话。</>}</p></div></section><section><h3><Folder size={16} />请求参数</h3>{fields.length ? <div className="table-scroll" tabIndex={0} aria-label="请求参数表"><table><thead><tr><th>参数名称</th><th>位置</th><th>类型</th><th>是否必填</th></tr></thead><tbody>{fields.map((field) => <tr key={`${field.location}-${field.name}`}><td><code>{field.name}</code></td><td>{field.location}</td><td>{field.type}</td><td>{field.required ? <mark>必填</mark> : '可选'}</td></tr>)}</tbody></table></div> : <div className="no-params">未声明额外参数，可参考调用示例接入。</div>}</section><section><h3><Code2 size={16} />调用示例</h3><div className="code-card"><div><span>cURL</span><button onClick={copy} disabled={copying} aria-label="复制调用示例"><Clipboard size={14} />{copying ? '复制中…' : copied ? '已复制' : '复制示例'}</button></div><pre tabIndex={0}>{code}</pre>{copyError && <p role="status">{copyError}</p>}</div></section></DocsBody>;
}
