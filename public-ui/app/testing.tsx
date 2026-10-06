'use client';

import { useEffect, useRef, useState } from 'react';
import { LogIn, LogOut, Play, Square, X } from 'lucide-react';
import { projectCatalog, catalogOrigin, type ApiDoc } from '../lib/catalog';
import { boundedResponse, liveRequest } from '../lib/testing';

export function useTestingSession() {
  const [username, setUsername] = useState('');
  const [checking, setChecking] = useState(true);
  async function refresh(signal?: AbortSignal) {
    const response = await fetch('/test/v1/session', { credentials: 'same-origin', cache: 'no-store', signal: signal || AbortSignal.timeout(8000) });
    if (response.status === 401) { setUsername(''); return false; }
    if (!response.ok) throw new Error('无法确认登录状态，请稍后重试。');
    const data = await response.json();
    if (typeof data.username !== 'string' || !data.username) throw new Error('无法确认登录状态。');
    setUsername(data.username); return true;
  }
  useEffect(() => { const controller = new AbortController(); let disposed = false; const timer = setTimeout(() => controller.abort(), 8000); void refresh(controller.signal).catch(() => {}).finally(() => { clearTimeout(timer); if (!disposed) setChecking(false); }); return () => { disposed = true; controller.abort(); clearTimeout(timer); }; }, []);
  return { username, checking, refresh, setUsername };
}
export type TestingSession = ReturnType<typeof useTestingSession>;

export function SessionControl({ session }: { session: TestingSession }) {
  const dialog = useRef<HTMLDialogElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const lock = useRef(false);
  async function authenticate(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (lock.current) return; lock.current = true; setBusy(true); setError('');
    const fields = new FormData(event.currentTarget);
    const password = String(fields.get('password') || '');
    try {
      const response = await fetch('/test/v1/login', { method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { 'Content-Type': 'application/json', 'X-API-Test': '1' }, body: JSON.stringify({ username: fields.get('username'), password }), signal: AbortSignal.timeout(10000) });
      const data = await response.json();
      if (!response.ok || typeof data.username !== 'string') throw new Error(data.error || '登录未完成，请稍后重试。');
      session.setUsername(data.username); form.current?.reset(); dialog.current?.close();
    } catch (error) { setError(error instanceof Error ? error.message : '登录未完成。'); }
    finally { if (form.current) { const input = form.current.querySelector<HTMLInputElement>('[name=password]'); if (input) input.value = ''; } lock.current = false; setBusy(false); }
  }
  async function logout() {
    if (lock.current) return; lock.current = true; setBusy(true); setError('');
    try {
      const response = await fetch('/test/v1/logout', { method: 'POST', credentials: 'same-origin', cache: 'no-store', headers: { 'X-API-Test': '1' }, signal: AbortSignal.timeout(8000) });
      if (!response.ok) throw new Error('退出未完成，请稍后重试。');
      session.setUsername('');
    } catch (error) { setError(error instanceof Error ? error.message : '退出未完成。'); }
    finally { lock.current = false; setBusy(false); }
  }
  function close() { if (busy) return; form.current?.reset(); setError(''); dialog.current?.close(); }
  return <div className="session-control">{session.username ? <><span className="session-username" title={session.username}>{session.username}</span><button type="button" className="quiet-button" onClick={() => void logout()} disabled={busy}><LogOut size={15} aria-hidden="true" /><span>退出</span></button>{error && <span className="session-error" role="alert">{error}</span>}</> : <button type="button" className="quiet-button" disabled={session.checking} onClick={() => { setError(''); dialog.current?.showModal(); }}><LogIn size={15} aria-hidden="true" /><span>{session.checking ? '检查登录…' : '登录测试'}</span></button>}
    <dialog ref={dialog} className="testing-login" aria-labelledby="testing-login-title" onCancel={event => { event.preventDefault(); close(); }}><div className="testing-login-head"><h2 id="testing-login-title">登录账号</h2><button type="button" className="icon-button" aria-label="关闭登录" disabled={busy} onClick={close}><X size={18} aria-hidden="true" /></button></div><p>使用已有账号登录后，可在文档中测试接口。</p><form ref={form} onSubmit={event => void authenticate(event)}><label>用户名<input name="username" autoComplete="username" required maxLength={80} disabled={busy} autoFocus /></label><label>密码<input name="password" type="password" autoComplete="current-password" required maxLength={128} disabled={busy} /></label>{error && <p className="example-error" role="alert">{error}</p>}<button className="primary-button" type="submit" disabled={busy}>{busy ? '登录中…' : '登录'}</button></form></dialog>
  </div>;
}

export function TestPanel({ api, origin, method, values, session, onBusyChange }: { onBusyChange: (value: boolean) => void; api: ApiDoc; origin: string; method: string; values: Record<string, string>; session: TestingSession }) {
  const [key, setKey] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState<{ status: number; duration: number; text: string; truncated: boolean; contentType: string } | null>(null);
  const inflight = useRef<AbortController | null>(null);
  const sequence = useRef(0);
  useEffect(() => { onBusyChange(busy); }, [busy, onBusyChange]);
  const current = api.operations?.find(value => value.method === method) || api;
  useEffect(() => { sequence.current++; inflight.current?.abort(); inflight.current = null; setBusy(false); setConfirmed(false); setError(''); setResult(null); setKey(''); return () => { sequence.current++; inflight.current?.abort(); }; }, [api.id, method, session.username, origin]);
  useEffect(() => { setConfirmed(false); }, [values, key]);
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
      const request = liveRequest(fresh, origin, method, values, key, window.location.origin);
      const started = performance.now();
      const response = await fetch(request.url, { method, headers: request.headers, body: request.body, credentials: 'omit', cache: 'no-store', redirect: 'error', referrerPolicy: 'no-referrer', signal: controller.signal });
      const data = await boundedResponse(response);
      if (sequence.current === run) setResult({ ...data, status: response.status, duration: Math.round(performance.now() - started), contentType: response.headers.get('Content-Type') || '未提供' });
    } catch (error) {
      if (sequence.current === run) setError(controller.signal.aborted ? '测试已停止或超过 15 秒。停止等待不会撤销已经执行的请求。' : error instanceof TypeError ? '无法读取响应，请检查调用域名、HTTPS 和跨域设置；请求可能已执行，请勿盲目重试。' : error instanceof Error ? error.message : '测试未完成。');
    } finally { clearTimeout(timer); if (sequence.current === run) { inflight.current = null; setBusy(false); setConfirmed(false); } }
  }
  return <section className="api-test-panel" aria-labelledby="api-test-title"><div className="editor-heading"><h2 id="api-test-title">在线测试</h2><span className="read-only-tag">真实调用</span></div>{!session.username ? <p>请先在页面顶部登录，然后测试此接口。浏览文档无需登录。</p> : <><p>使用上方填写的参数发起请求，计入接口额度。不会自动重试。</p>{current.authentication === 'api_key' && <label className="test-key">调用密钥<input aria-label="测试调用密钥" type="password" value={key} autoComplete="off" maxLength={512} disabled={busy} onChange={event => setKey(event.target.value)} placeholder="填写你的调用密钥" /><small>仅用于此次页面中的测试，不会写入浏览器存储。</small></label>}<label className="test-confirm"><input type="checkbox" checked={confirmed} disabled={busy} onChange={event => setConfirmed(event.target.checked)} /><span>我确认发送真实请求，可能消耗额度或修改数据。</span></label><div className="test-actions"><button type="button" className="primary-button" onClick={() => void send()} disabled={busy || !confirmed}><Play size={14} aria-hidden="true" />{busy ? '请求中…' : '发送请求'}</button>{busy && <button type="button" className="quiet-button" onClick={() => inflight.current?.abort()}><Square size={13} aria-hidden="true" />停止等待</button>}</div></>}{error && <p role="alert" className="example-error">{error}</p>}{result && <div className="test-response" role="status"><div><strong>HTTP {result.status}</strong><span>{result.duration} ms</span></div><p>{result.contentType}</p>{result.truncated && <p>响应超过 128 KiB，仅展示前面部分。</p>}<pre tabIndex={0} aria-label="接口实际响应">{result.text || '响应内容为空。'}</pre><button type="button" className="quiet-button" onClick={() => setResult(null)}>清除响应</button></div>}</section>;
}
