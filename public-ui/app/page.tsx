'use client';

import { useEffect, useMemo, useState } from 'react';
import { Clipboard, Code2, Folder, KeyRound, LockKeyhole, Search, ShieldCheck, UnlockKeyhole } from 'lucide-react';
import { DocsBody, DocsTitle, DocsDescription } from 'fumadocs-ui/layouts/docs/page';

import { command, projectCatalog, defaultSite, exampleValue, type ApiDoc, type Catalog } from '../lib/catalog';

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
  const [pageOrigin,setPageOrigin]=useState('');
  const [selected, setSelected] = useState('');

  useEffect(()=>{setPageOrigin(window.location.origin)},[]);
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
  const methods = useMemo(() => Array.from(new Set(catalog.apis.flatMap((api) => api.methods||[api.method]))), [catalog.apis]);
  const filtered = useMemo(() => catalog.apis.filter((api) => [api.title, api.summary, api.path, api.category].join(' ').toLowerCase().includes(query.toLowerCase()) && (api.operations||[api]).some(operation=>(method==='all'||operation.method===method)&&(auth==='all'||operation.authentication===auth)) && (category === 'all' || api.category === category)), [catalog.apis, query, method, auth, category]);
  const current = filtered.find((api) => api.id === selected) || filtered[0];

  const site=catalog.site || defaultSite;
  useEffect(()=>{document.title=site.public_title;const description=document.querySelector('meta[name="description"]');description?.setAttribute('content',site.description);let keywords=document.querySelector('meta[name="keywords"]');if(!keywords){keywords=document.createElement('meta');keywords.setAttribute('name','keywords');document.head.appendChild(keywords)}keywords.setAttribute('content',site.keywords)},[site.public_title,site.description,site.keywords]);
  return <>
    <header className="site-header"><div className="header-inner"><a className="brand" href="/" aria-label={`${site.name} ${site.subtitle}`}><span className="brand-mark" aria-hidden="true" /><span>{site.name}<small>{site.subtitle}</small></span></a><span className="header-note"><i />接口目录</span></div></header>
    <main>
      <section className="hero"><div><span className="kicker">开放服务</span><h1>{site.hero_title.split("\n").map((line,index)=><span key={index}>{index>0&&<br/>}{line}</span>)}</h1><p>{site.hero_description.split("\n").map((line,index)=><span key={index}>{index>0&&<br/>}{line}</span>)}</p></div><div className="hero-mark" aria-hidden="true">{'{'}<span /><span />{'}'}</div></section>
      {site.announcement&&<section className="site-announcement" aria-label="网站公告">{site.announcement}</section>}
      <section className="toolbar" aria-label="搜索及筛选接口"><label className="search"><Search size={19} /><span className="sr-only">搜索接口</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索接口名称、路径或用途" /></label><select aria-label="请求方法" value={method} onChange={(event) => setMethod(event.target.value)}><option value="all">全部请求方法</option>{methods.map((value) => <option key={value}>{value}</option>)}</select><select aria-label="认证方式" value={auth} onChange={(event) => setAuth(event.target.value)}><option value="all">全部认证方式</option><option value="api_key">KEY 认证</option><option value="none">无需验证</option></select></section>
      <div className="catalog-meta" role="status" aria-live="polite"><span>{loading ? '正在读取公开接口…' : `${catalog.apis.length} 个公开接口${filtered.length !== catalog.apis.length ? `，当前找到 ${filtered.length} 个` : ''}`}</span><span>{categories.length} 个分类</span></div>
      {error ? <div className="error-state" role="alert"><h2>{error}</h2><button onClick={() => void load()}>重新读取目录</button></div> : <section className="workspace"><aside className="sidebar"><div className="sidebar-title"><strong>接口分类</strong><span>{catalog.apis.length}</span></div><nav aria-label="接口分类">{[['all', '全部接口', catalog.apis.length], ...categories.map((value) => [value, value, catalog.apis.filter((api) => api.category === value).length])].map(([id, label, count]) => <button key={id} className={category === id ? 'active' : ''} onClick={() => setCategory(String(id))} aria-pressed={category === id}><Folder size={15} />{label}<em>{count}</em></button>)}</nav><div className="sidebar-note"><ShieldCheck size={17} /><p>按用途查找接口，<br />选择适合的调用方式。</p></div></aside><section className="endpoint-list"><div className="column-title"><strong>{category === 'all' ? '全部接口' : category}</strong><span>{filtered.length}</span></div>{!filtered.length ? <div className="empty">没有匹配的接口。<br />试试其他关键词或筛选条件。</div> : filtered.map((api) => <button key={api.id} className={`endpoint ${current?.id === api.id ? 'active' : ''}`} onClick={() => setSelected(api.id)} aria-pressed={current?.id === api.id}><span className="endpoint-head"><strong>{api.title}</strong><span className="endpoint-methods">{(api.methods||[api.method]).map(value=><b key={value} className={methodClass(value)}>{value}</b>)}</span></span><code>{api.path}</code><p>{api.summary || '查看参数与调用示例'}</p><small>{(api.operations||[api]).every(op=>op.authentication==='none') ? <UnlockKeyhole size={11} /> : <LockKeyhole size={11} />}{(api.operations||[api]).every(op=>op.authentication==='none')?'直接调用':'支持密钥调用'}</small></button>)}</section><section className="document" aria-label="接口文档">{current ? <Document key={`${current.id}:${method}:${auth}`} api={current} baseUrl={catalog.base_url||site.website_url||pageOrigin} preferredMethod={method} preferredAuth={auth} /> : <div className="initial"><Code2 size={42} /><h2>选择一个接口</h2><p>公开接口的调用文档会显示在这里。</p></div>}</section></section>}
      <section className="note"><span>01</span><div><strong>先读文档，再开始接入</strong><p>调用地址已自动填入，参数可以按需调整。需要认证的接口，请在调用时使用你自己的密钥。</p></div></section>
    </main><footer><strong>{site.name}</strong><span>{site.footer}{site.contact_email&&<> · <a href={`mailto:${site.contact_email}`}>{site.contact_email}</a></>}</span></footer>
  </>;
}

function Document({ api,baseUrl,preferredMethod,preferredAuth }: {api:ApiDoc;baseUrl:string;preferredMethod:string;preferredAuth:string}) {
  const methods=api.methods||[api.method];
  const [activeMethod,setActiveMethod]=useState((api.operations||[api]).find(operation=>(preferredMethod==='all'||operation.method===preferredMethod)&&(preferredAuth==='all'||operation.authentication===preferredAuth))?.method||methods[0]);
  const [examples,setExamples]=useState<Record<string,string>>({});
  const operation=api.operations?.find(value=>value.method===activeMethod);
  const selectedApi={...api,...operation,method:activeMethod};
  const [copied, setCopied] = useState(false);
  const [copying, setCopying] = useState(false);
  const [copyError, setCopyError] = useState('');
  let code='';let exampleError='';try{code=command(api,baseUrl,activeMethod,examples)}catch(error){exampleError=error instanceof Error?error.message:'请检查参数示例。'}
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
  const fields = [...selectedApi.parameters, ...selectedApi.body];
  return <DocsBody><div className="breadcrumb">接口目录 / {api.category}</div><div className="document-title"><div><span className={`method ${methodClass(activeMethod)}`}>{activeMethod}</span><DocsTitle>{api.title}</DocsTitle></div><span>{api.category}</span></div><DocsDescription className="summary">{api.summary || '查看请求方式、参数及调用示例。'}</DocsDescription><code className="path">{baseUrl}{api.path}</code><section><h3>调用方式</h3><div className="document-methods" role="group" aria-label="选择调用方式">{methods.map(value=><button type="button" key={value} className={activeMethod===value?'selected':''} aria-pressed={activeMethod===value} onClick={()=>{setActiveMethod(value);setExamples({})}}>{value}</button>)}</div></section><section><h3>{selectedApi.authentication === 'none' ? <UnlockKeyhole size={16} /> : <KeyRound size={16} />}认证方式</h3><div className="auth-card"><strong>{authLabel(selectedApi.authentication)}</strong><p>{selectedApi.authentication === 'none' ? '无需密钥即可调用。' : <>在请求头中加入 <code>X-API-Key</code>。</>}</p></div></section><section><h3><Folder size={16} />请求参数</h3>{fields.length ? <div className="table-scroll" tabIndex={0} aria-label="请求参数表"><table><thead><tr><th>参数名称</th><th>位置</th><th>类型</th><th>是否必填</th><th>示例</th></tr></thead><tbody>{fields.map((field) => <tr key={`${field.location}-${field.name}`}><td><code>{field.name}</code></td><td>{{path:'地址',query:'查询',header:'请求头',body:'内容'}[field.location]}</td><td>{{string:'文本',integer:'整数',number:'数字',boolean:'是/否',array:'列表',object:'对象'}[field.type]||field.type}</td><td>{field.required ? <mark>必填</mark> : '可选'}</td><td><input className="example-input" aria-label={`${field.name} 示例`} maxLength={500} value={examples[`${field.location}:${field.name}`]??formatExample(exampleValue(field,baseUrl))} onChange={event=>setExamples(value=>({...value,[`${field.location}:${field.name}`]:event.target.value}))}/></td></tr>)}</tbody></table></div> : <div className="no-params">未声明额外参数，可参考调用示例接入。</div>}</section><section><h3><Code2 size={16} />调用示例</h3><div className="code-card"><div><span>cURL</span><button onClick={copy} disabled={copying||Boolean(exampleError)} aria-label="复制调用示例"><Clipboard size={14} />{copying ? '复制中…' : copied ? '已复制' : '复制示例'}</button></div>{exampleError?<p role="alert">{exampleError}</p>:<pre tabIndex={0}>{code}</pre>}{copyError && <p role="status">{copyError}</p>}</div></section></DocsBody>;
}

function formatExample(value:unknown):string{return typeof value==='string'?value:JSON.stringify(value);}
