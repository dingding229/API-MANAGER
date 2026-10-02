export type Field = { name: string; location: 'path' | 'query' | 'header' | 'body'; type: string; required: boolean };
export type Operation = { method:string; authentication: 'api_key'|'none'; parameters:Field[]; body:Field[] };
export type ApiDoc = { methods?: string[]; operations?: Operation[]; id: string; title: string; summary: string; category: string; method: string; path: string; authentication: 'api_key' | 'none'; parameters: Field[]; body: Field[] };
export type Catalog = { version: number; base_url: string; apis: ApiDoc[]; site?: SiteInfo };

export function sourceUrl(value: string) {
  const url = new URL(value);
  const privateHttp = url.protocol === 'http:' && ['api-manager', 'localhost', '127.0.0.1'].includes(url.hostname);
  if (url.username || url.password || url.search || url.hash || url.pathname !== '/public/v1/catalog' || (url.protocol !== 'https:' && !privateHttp)) throw new Error('disallowed export');
  return url;
}

const object = (value: unknown): Record<string, unknown> => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
const text = (value: unknown, limit: number) => typeof value === 'string' ? value.slice(0, limit) : '';
function fields(value: unknown): Field[] {
  return Array.isArray(value) ? value.slice(0, 40).flatMap((item) => {
    const field = object(item);
    if (typeof field.name !== 'string' || !field.name || /[\x00-\x1f\x7f]/.test(field.name) || !['path', 'query', 'header', 'body'].includes(String(field.location)) || !['string', 'integer', 'number', 'boolean', 'array', 'object'].includes(String(field.type))) return [];
    return [{ name: field.name.slice(0, 80), location: field.location as Field['location'], type: field.type as string, required: field.required === true }];
  }) : [];
}

// Strict DTO projection, shared by the actual Next.js route and boundary tests.
export function projectCatalog(value: unknown): Catalog {
  const data = object(value);
  if (data.version !== 1 || !Array.isArray(data.apis)) throw new Error('invalid catalog');
  const allowed=['GET','POST','PUT','PATCH','DELETE','HEAD','OPTIONS'];
  const byPath=new Map<string,ApiDoc>();
  for (const entry of data.apis.slice(0,1400)) {
    const api=object(entry);
    if(typeof api.path!=='string'||api.path.length>512||!api.path.startsWith('/api/')||/[\x00-\x20\x7f?#\\]/.test(api.path)||typeof api.id!=='string'||!api.id||typeof api.title!=='string'||!api.title.trim())continue;
    const source=Array.isArray(api.operations)&&api.operations.length?api.operations.slice(0,7):[{method:api.method,authentication:api.authentication,parameters:api.parameters,body:api.body}];
    const operations:Operation[]=source.flatMap(value=>{const op=object(value);if(!allowed.includes(String(op.method))||!['api_key','none'].includes(String(op.authentication)))return [];return [{method:String(op.method),authentication:op.authentication as Operation['authentication'],parameters:fields(op.parameters),body:fields(op.body)}]});
    if(!operations.length)continue;
    const existing=byPath.get(api.path);
    if(existing){for(const operation of operations){if(!existing.methods!.includes(operation.method)){existing.methods!.push(operation.method);existing.operations!.push(operation)}};continue;}
    if(byPath.size>=200)continue;
    const first=operations[0];
    const unique=operations.filter((value,index,list)=>list.findIndex(item=>item.method===value.method)===index);
    byPath.set(api.path,{id:text(api.id,64),title:text(api.title,120),summary:text(api.summary,600),category:text(api.category,48)||'通用接口',method:first.method,authentication:first.authentication,path:api.path,parameters:first.parameters,body:first.body,methods:unique.map(value=>value.method),operations:unique});
  }
  const apis=[...byPath.values()];
  let base = '';
  if (typeof data.base_url === 'string' && data.base_url) {
    try {
      const url = new URL(data.base_url);
      if (!url.username && !url.password && !url.search && !url.hash && (url.pathname === '/' || url.pathname === '') && (url.protocol === 'https:' || url.protocol === 'http:')) base = url.origin;
    } catch { /* An invalid optional origin must not hide otherwise safe docs. */ }
  }
  return { version: 1, base_url: base, apis, ...(data.site ? {site:projectSite(data.site)} : {}) };
}

export const shellQuote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
export function exampleValue(field: Field,baseUrl:string): unknown {
  switch(field.type){case 'integer':case 'number':return 1;case 'boolean':return true;case 'array':return [];case 'object':return {};}
  const name=field.name.toLowerCase();
  if(/(^|_)(url|uri|origin|website|endpoint)($|_)/.test(name))return baseUrl;
  if(/(^|_)(domain|host|hostname)($|_)/.test(name)){try{return new URL(baseUrl).hostname}catch{return ''}}
  if(/(^|_)(id|page|limit|offset|count)($|_)/.test(name))return '1';
  if(name==='email'||name.endsWith('_email'))return 'example@example.com';
  if(/(^|_)(query|q|search|keyword|name|title)($|_)/.test(name))return 'example';
  return 'example';
}
function pathExample(name:string,api:ApiDoc,baseUrl:string):string {
 const field=api.parameters.find(item=>item.location==='path'&&item.name===name)||{name,location:'path' as const,type:'string',required:true};
 return String(exampleValue(field,baseUrl));
}
function bodyExample(field:Field,value:unknown):unknown {
 if(typeof value!=='string')return value;
 if(field.type==='integer'||field.type==='number'){const parsed=Number(value);if(!Number.isFinite(parsed)||(field.type==='integer'&&!Number.isInteger(parsed)))throw new Error('请填写有效数字。');return parsed;}
 if(field.type==='boolean'){if(!['true','false'].includes(value))throw new Error('请选择是或否。');return value==='true';}
 if(field.type==='array'||field.type==='object'){let parsed;try{parsed=JSON.parse(value)}catch{throw new Error('请填写有效内容。')};if(field.type==='array'?!Array.isArray(parsed):parsed===null||typeof parsed!=='object'||Array.isArray(parsed))throw new Error('内容格式不正确。');return parsed;}
 return value;
}
export function command(api:ApiDoc,baseUrl:string,selectedMethod=api.method,overrides:Record<string,string>={}) {
 const operation=api.operations?.find(op=>op.method===selectedMethod);
 const current={...api,...operation,method:operation?.method||selectedMethod};
 const origin=baseUrl.replace(/\/$/,'');
 if(!/^https?:\/\//.test(origin))return '请刷新页面后查看调用示例。';
 if(Object.values(overrides).some(value=>/[\r\n\x00]/.test(value)))return '参数示例不能包含换行。';
 const value=(field:Field)=>overrides[`${field.location}:${field.name}`]??exampleValue(field,origin);
 const path=current.path.replace(/\{([^}]+)\}/g,(_match,name)=>encodeURIComponent(overrides[`path:${name}`]??pathExample(name,current,origin)));
 const query=current.parameters.filter(field=>field.location==='query').map(field=>`${encodeURIComponent(field.name)}=${encodeURIComponent(String(value(field)))}`).join('&');
 const url=`${origin}${path}${query?`?${query}`:''}`;
 const lines=[`curl -X ${current.method} ${shellQuote(url)}`];
 if(current.authentication==='api_key')lines.push(`  -H ${shellQuote('X-API-Key: 填写你的调用密钥')}`);
 for(const field of current.parameters.filter(field=>field.location==='header')){
  if(!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(field.name)||/^(authorization|x-api-key|cookie|host|content-length|content-type)$/i.test(field.name))continue;
  lines.push(`  -H ${shellQuote(`${field.name}: ${String(value(field))}`)}`);
 }
 if(current.body.length)lines.push(`  -H ${shellQuote('Content-Type: application/json')}`,`  --data ${shellQuote(JSON.stringify(Object.fromEntries(current.body.map(field=>[field.name,bodyExample(field,value(field))]))))}`);
 return lines.join(' \\\n');
}

export type SiteInfo = { api_domain:string; name: string; public_title: string; admin_title: string; description: string; keywords: string; website_url: string; api_base_url: string; subtitle: string; hero_title: string; hero_description: string; announcement: string; footer: string; contact_email: string };
export const defaultSite: SiteInfo = { api_domain:'', name:'API Manager',public_title:'API Manager · 开放接口目录',admin_title:'API Manager Console',description:'浏览已公开的 API 接口、参数和调用方式。',keywords:'',website_url:'',api_base_url:'',subtitle:'开放接口目录',hero_title:'找到接口，\n开始你的下一次调用。',hero_description:'从用途到参数，从认证方式到调用示例。\n让接口接入清晰、直接、有据可循。',announcement:'',footer:'已发布的服务信息',contact_email:'' };
export function projectSite(value: unknown): SiteInfo {
 const data=object(value); const result={...defaultSite};
 const limits:Record<keyof SiteInfo,number>={api_domain:512,name:80,public_title:120,admin_title:120,description:600,keywords:300,website_url:512,api_base_url:512,subtitle:80,hero_title:160,hero_description:600,announcement:600,footer:300,contact_email:254};
 for(const field of Object.keys(limits) as (keyof SiteInfo)[]) {if(typeof data[field]==='string') result[field]=text(data[field],limits[field]);}
 for(const field of ['website_url','api_base_url','api_domain'] as const) {
  if(!result[field]) continue;
  try {const u=new URL(result[field]);if(u.username||u.password||u.search||u.hash||(u.pathname!=='/'&&u.pathname!=='')||!(u.protocol==='https:'||u.protocol==='http:'))result[field]='';}
  catch {result[field]='';}
 }
 return result;
}
