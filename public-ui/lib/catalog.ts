export type Field = { name: string; location: 'path' | 'query' | 'header' | 'body'; type: string; required: boolean };
export type ApiDoc = { id: string; title: string; summary: string; category: string; method: string; path: string; authentication: 'api_key' | 'none'; parameters: Field[]; body: Field[] };
export type Catalog = { version: number; base_url: string; apis: ApiDoc[] };

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
  const apis = data.apis.slice(0, 200).flatMap((entry): ApiDoc[] => {
    const api = object(entry);
    if (!['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'].includes(String(api.method)) || !['api_key', 'none'].includes(String(api.authentication)) || typeof api.path !== 'string' || api.path.length > 512 || !api.path.startsWith('/api/') || /[\x00-\x20\x7f?#\\]/.test(api.path)) return [];
    if (typeof api.id !== 'string' || !api.id || typeof api.title !== 'string' || !api.title.trim()) return [];
    return [{ id: text(api.id, 64), title: text(api.title, 120), summary: text(api.summary, 600), category: text(api.category, 48) || '通用接口', method: api.method as string, path: api.path, authentication: api.authentication as ApiDoc['authentication'], parameters: fields(api.parameters), body: fields(api.body) }];
  });
  let base = '';
  if (typeof data.base_url === 'string' && data.base_url) {
    try {
      const url = new URL(data.base_url);
      if (!url.username && !url.password && !url.search && !url.hash && (url.pathname === '/' || url.pathname === '') && (url.protocol === 'https:' || (url.protocol === 'http:' && ['localhost', '127.0.0.1'].includes(url.hostname)))) base = url.origin;
    } catch { /* An invalid optional origin must not hide otherwise safe docs. */ }
  }
  return { version: 1, base_url: base, apis };
}

export const shellQuote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
function exampleValue(type: string): unknown {
  switch (type) {
    case 'integer': case 'number': return 1;
    case 'boolean': return true;
    case 'array': return [];
    case 'object': return {};
    default: return 'YOUR_VALUE';
  }
}
export function command(api: ApiDoc, baseUrl: string) {
  const path = api.path.replace(/\{[^}]+\}/g, 'YOUR_VALUE');
  const query = api.parameters.filter((field) => field.location === 'query').map((field) => `${encodeURIComponent(field.name)}=YOUR_VALUE`).join('&');
  const url = `${baseUrl || 'https://YOUR_API_DOMAIN'}${path}${query ? `?${query}` : ''}`;
  const lines = [`curl -X ${api.method} ${shellQuote(url)}`];
  if (api.authentication === 'api_key') lines.push(`  -H ${shellQuote('X-API-Key: YOUR_API_KEY')}`);
  for (const field of api.parameters.filter((field) => field.location === 'header')) {
    // Auth/transport headers cannot be replaced by an example parameter.
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(field.name) || /^(authorization|x-api-key|cookie|host|content-length|content-type)$/i.test(field.name)) continue;
    lines.push(`  -H ${shellQuote(`${field.name}: YOUR_VALUE`)}`);
  }
  if (api.body.length) lines.push(`  -H ${shellQuote('Content-Type: application/json')}`, `  --data ${shellQuote(JSON.stringify(Object.fromEntries(api.body.map((field) => [field.name, exampleValue(field.type)]))))}`);
  return lines.join(' \\\n');
}
