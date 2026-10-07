'use client';

import { useEffect, useRef, useState } from 'react';
import { LogIn, LogOut, Play, Square, X } from 'lucide-react';
import { projectCatalog, catalogOrigin, type ApiDoc } from '../lib/catalog';
import { requestExample } from '../lib/catalog';

export function useTestingSession() {
  const [username, setUsername] = useState('');
  const [checking, setChecking] = useState(true);
  const [allowedApiIds,setAllowedApiIds]=useState<string[]|null>(null);
  const [canTest,setCanTest]=useState(false);const [canTestWrite,setCanTestWrite]=useState(false);
  const sessionEpoch = useRef(0);
  async function refresh(signal?: AbortSignal) {
    const epoch=++sessionEpoch.current;
    const response = await fetch('/test/v1/session', { credentials: 'same-origin', cache: 'no-store', signal: signal || AbortSignal.timeout(8000) });
    if(signal?.aborted) throw new Error('登录检查已取消。');
    if (response.status === 401) { if(epoch===sessionEpoch.current){setUsername('');setAllowedApiIds(null);setCanTest(false);setCanTestWrite(false)} return false; }
    if (!response.ok) throw new Error('无法确认登录状态，请稍后重试。');
    const data = await response.json();
    if (typeof data.username !== 'string' || !data.username) throw new Error('无法确认登录状态。');
    if(signal?.aborted) throw new Error('登录检查已取消。');
    if(epoch!==sessionEpoch.current) return false;
    setAllowedApiIds(Array.isArray(data.allowed_api_ids)?data.allowed_api_ids.filter((id:unknown)=>typeof id==='string'):null);setUsername(data.username);setCanTest(data.can_test===true);setCanTestWrite(data.can_test_write===true); return true;
  }
  useEffect(() => {
    let controller: AbortController | null = null; let disposed=false; let pending=false;
    const channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel('api-manager-auth') : null;
    const update = async () => { if(pending || document.hidden || disposed) return; pending=true; controller=new AbortController();const timeout=setTimeout(()=>controller?.abort(),8000);try { await refresh(controller.signal); } catch {} finally { clearTimeout(timeout);pending=false; if(!disposed)setChecking(false); } };
    void update(); const visible=()=>{if(!document.hidden)void update()};
    window.addEventListener('focus',visible);document.addEventListener('visibilitychange',visible);channel?.addEventListener('message',visible);
    const timer=setInterval(visible,60000);
    return()=>{disposed=true;controller?.abort();clearInterval(timer);window.removeEventListener('focus',visible);document.removeEventListener('visibilitychange',visible);channel?.close()};
  }, []);
  function changed(name: string) { sessionEpoch.current++;setUsername(name); const channel=typeof BroadcastChannel==='function'?new BroadcastChannel('api-manager-auth'):null;channel?.postMessage('changed');channel?.close(); }
  return { username, checking, refresh, allowedApiIds, canTest,canTestWrite,setUsername: changed };
}
export type TestingSession = ReturnType<typeof useTestingSession>;

export function SessionControl({ session, returnTo='docs' }: { session: TestingSession; returnTo?:'home'|'docs'|'guide'|'playground' }) {
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 async function logout(){if(busy)return;setBusy(true);setError('');try{
  const response=await fetch('/test/v1/logout',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'X-API-Request':'1'},signal:AbortSignal.timeout(10000)});
  if(!response.ok)throw Error('退出未完成，请稍后重试。');
  await session.refresh();const channel=new BroadcastChannel('api-manager-auth');channel.postMessage('changed');channel.close();
 }catch(e){setError(e instanceof Error?e.message:'退出未完成')}finally{setBusy(false)}}
 return <div className="session-control">{session.username?<><span className="session-username" title={session.username}>{session.username}</span><button type="button" className="quiet-button" disabled={busy} onClick={()=>void logout()}><LogOut size={15} aria-hidden="true"/><span>退出</span></button></>:session.checking?<span className="session-checking" role="status" aria-label="正在确认登录状态"><span className="ui-placeholder-line" aria-hidden="true"/></span>:<a className="quiet-button" href={'/login?return='+returnTo}><LogIn size={15} aria-hidden="true"/><span>登录</span></a>}{!session.checking&&!session.username&&<a className="quiet-button" href={'/register?return='+returnTo}>注册</a>}{error&&<span className="session-error" role="alert">{error}</span>}</div>
}

export function TestPanel({ api, origin, method, values, session, onBusyChange }: { onBusyChange: (value: boolean) => void; api: ApiDoc; origin: string; method: string; values: Record<string, string>; session: TestingSession }) {
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<{ status: number; duration: number; text: string; truncated: boolean; contentType: string; cache?: string } | null>(null);
  const inflight = useRef<AbortController | null>(null);
  const sequence = useRef(0);
  useEffect(() => { onBusyChange(busy); }, [busy, onBusyChange]);
  const current = api.operations?.find(value => value.method === method) || {...api,test_enabled:false};
  useEffect(() => { sequence.current++; inflight.current?.abort(); inflight.current = null; setBusy(false); setConfirmed(false); setError(''); setResult(null); return () => { sequence.current++; inflight.current?.abort(); }; }, [api.id, method, session.username, origin]);
  useEffect(() => { setConfirmed(false); }, [values]);
  async function send() {
    if (inflight.current || !confirmed || !session.username) return;
    const controller = new AbortController(); inflight.current = controller; const run = ++sequence.current;
    setBusy(true); setError(''); setResult(null);
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      if (!await session.refresh(controller.signal)) throw new Error('登录已过期，请重新登录。');
      // Re-read current visibility and configured call origin, not a stale doc.
      const exported = await fetch('/catalog.json', { credentials: 'omit', cache: 'no-store', signal: controller.signal });
      if (!exported.ok) throw new Error('无法确认接口状态，请刷新目录。');
      const catalog = projectCatalog(await exported.json());
      const fresh = catalog.apis.find(value => value.id === api.id && value.path === api.path);
      if (!fresh || !(fresh.methods || [fresh.method]).includes(method) || catalogOrigin(catalog, window.location.origin) !== origin) throw new Error('接口状态或调用地址已更新，请刷新目录。');
      if(!fresh.operations?.find(value=>value.method===method)?.test_enabled) throw new Error('此接口未开放在线测试。');
      const preview=requestExample(fresh,origin,method,values);const parsed=new URL(preview.url);
      const input={api_id:fresh.id,method,path:parsed.pathname+parsed.search,headers:Object.fromEntries(preview.headers.filter(([name])=>name.toLowerCase()!=='x-api-key')),body:preview.body||''};
      const options={method:'POST',credentials:'same-origin' as const,cache:'no-store' as const,redirect:'error' as const,headers:{'Content-Type':'application/json','X-API-Request':'1'},signal:controller.signal};
      const grantResponse=await fetch('/test/v1/prepare',{...options,body:JSON.stringify({request:input})});
      const grant=await grantResponse.json();if(!grantResponse.ok || typeof grant.ticket!=='string')throw new Error(grant.error||'无法获取测试授权。');
      const started=performance.now();
      const response=await fetch('/test/v1/invoke',{...options,body:JSON.stringify({request:input,ticket:grant.ticket})});
      const data=await response.json();if(!response.ok)throw new Error(data.error||'测试未完成。');
      if(typeof data.status!=='number'||data.status<200||data.status>599||typeof data.body!=='string'||data.body.length>131072)throw new Error('测试响应不可用。');
      if(sequence.current===run)setResult({status:data.status,duration:typeof data.duration_ms==='number'?data.duration_ms:Math.round(performance.now()-started),text:data.body,truncated:data.truncated===true,contentType:data.content_type||'未提供',cache:['HIT','MISS'].includes(data.cache)?data.cache:''});
    } catch (error) {
      if (sequence.current === run) setError(controller.signal.aborted ? '测试已停止或超过 15 秒。停止等待不会撤销已经执行的请求。' : error instanceof TypeError ? '无法读取响应，请检查调用域名、HTTPS 和跨域设置；请求可能已执行，请勿盲目重试。' : error instanceof Error ? error.message : '测试未完成。');
    } finally { clearTimeout(timer); if (sequence.current === run) { inflight.current = null; setBusy(false); setConfirmed(false); } }
  }
  return <section className="api-test-panel" aria-labelledby="api-test-title"><div className="editor-heading"><h2 id="api-test-title">在线测试</h2><span className="read-only-tag">真实调用</span></div>{!current.test_enabled ? <p>此接口未开放在线测试，可复制示例在自己的调用环境中接入。</p> : !session.username ? <p>请先在页面顶部登录，然后测试此接口。浏览文档无需登录。</p> : !session.canTest ? <p>当前账号没有在线测试权限，请联系管理员。</p> : !['GET','HEAD'].includes(method) && !session.canTestWrite ? <p>此调用方式需要接口修改权限。</p> : <><p>使用上方填写的参数发起请求，计入接口额度。不会自动重试。</p><p className="test-account-note">使用当前账号的一次性测试授权，无需填写调用 KEY。外部程序调用仍按接口要求认证。</p><label className="test-confirm"><input type="checkbox" checked={confirmed} disabled={busy} onChange={event => setConfirmed(event.target.checked)} /><span>我确认发送真实请求，可能消耗额度或修改数据。</span></label><div className="test-actions"><button type="button" className="primary-button" onClick={() => void send()} disabled={busy || !confirmed}><Play size={14} aria-hidden="true" />{busy ? '请求中…' : '发送请求'}</button>{busy && <button type="button" className="quiet-button" onClick={() => inflight.current?.abort()}><Square size={13} aria-hidden="true" />停止等待</button>}</div></>}{error && <p role="alert" className="example-error">{error}</p>}{result && <div className="test-response" role="status"><div><strong>HTTP {result.status}</strong><span>{result.duration===0?'<1':result.duration} ms</span></div><p>{result.contentType}{result.cache&&<span> · {result.cache==='HIT'?'命中服务端缓存':'未命中服务端缓存'}</span>}</p>{result.truncated && <p>响应超过 128 KiB，仅展示前面部分。</p>}<pre tabIndex={0} aria-label="接口实际响应">{result.text || '响应内容为空。'}</pre><button type="button" className="quiet-button" onClick={() => setResult(null)}>清除响应</button></div>}</section>;
}
