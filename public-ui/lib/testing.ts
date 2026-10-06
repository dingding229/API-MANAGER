import { requestExample, type ApiDoc } from './catalog.ts';

export const RESPONSE_LIMIT = 128 * 1024;
export function liveRequest(api: ApiDoc, origin: string, method: string, values: Record<string, string>, key: string, pageOrigin: string) {
  const request = requestExample(api, origin, method, values);
  const operation = api.operations?.find(value => value.method === method) || api;
  if (new URL(pageOrigin).protocol === 'https:' && new URL(request.url).protocol !== 'https:') throw new Error('安全页面只能测试 HTTPS 接口，请检查调用地址。');
  if ((method === 'GET' || method === 'HEAD') && request.body !== undefined) throw new Error('浏览器不能发送带请求内容的 GET 或 HEAD 请求，请使用调用示例。');
  if (operation.authentication === 'api_key') {
    if (!key.trim() || key.length > 512 || /[^\x21-\x7e]/.test(key)) throw new Error('请填写有效的调用密钥。');
    request.headers = request.headers.filter(([name]) => name.toLowerCase() !== 'x-api-key');
    request.headers.push(['X-API-Key', key]);
  } else {
    request.headers = request.headers.filter(([name]) => name.toLowerCase() !== 'x-api-key');
  }
  return request;
}
export async function boundedResponse(response: Response, limit = RESPONSE_LIMIT): Promise<{text: string; truncated: boolean}> {
  if (!response.body) return { text: '', truncated: false };
  const reader = response.body.getReader(); const decoder = new TextDecoder();
  let bytes = 0, text = '';
  try {
    for (;;) {
      const { value, done } = await reader.read(); if (done) return { text: text + decoder.decode(), truncated: false };
      const available = limit - bytes;
      text += decoder.decode(value.subarray(0, available), { stream: true }); bytes += value.byteLength;
      if (bytes > limit) { await reader.cancel(); return { text: text + decoder.decode(), truncated: true }; }
    }
  } finally { reader.releaseLock(); }
}
