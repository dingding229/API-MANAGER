import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { projectCatalog, sourceUrl, command, shellQuote } from '../lib/catalog.ts';

const api = { id: 'public-hash', title: '公开接口', summary: '独立公开说明', category: '示例', method: 'POST', path: '/api/example/{id}', authentication: 'api_key', parameters: [], body: [] };

test('only allowlisted public fields survive the real DTO projection', () => {
  const result = projectCatalog({ version: 1, base_url: 'https://api.example.com', private_field: 'SECRET-top', apis: [{ ...api, upstream_url: 'SECRET-upstream', auth_config: { key: 'SECRET-key' }, response_body: 'SECRET-response', parameters: [{ name: 'q', location: 'query', type: 'string', required: true, default: 'SECRET-default' }], body: [] }] });
  assert.equal(JSON.stringify(result).includes('SECRET'), false);
  assert.deepEqual(Object.keys(result.apis[0]).sort(), [...Object.keys(api),'methods','operations'].sort());
  assert.deepEqual(result.apis[0].parameters, [{ name: 'q', location: 'query', type: 'string', required: true }]);
});

test('fixed export source rejects credentials, admin routes, query and plaintext external targets', () => {
  for (const value of ['http://localhost/admin/v1/apis', 'https://u:p@example.com/public/v1/catalog', 'http://localhost/public/v1/catalog?target=admin', 'http://external.example/public/v1/catalog', 'file:///public/v1/catalog', 'https://example.com/public/v1/catalog#secret']) assert.throws(() => sourceUrl(value));
  assert.equal(sourceUrl('http://api-manager:8080/public/v1/catalog').pathname, '/public/v1/catalog');
});

test('invalid optional public origin falls back without taking down the catalog', () => {
  for (const base_url of ['not-a-url', 'https://u:p@example.com', 'https://example.com/admin', 'https://example.com?q=secret']) assert.equal(projectCatalog({ version: 1, base_url, apis: [api] }).base_url, '');
});

test('invalid catalog, methods, auth and paths fail closed', () => {
  assert.throws(() => projectCatalog({ version: 2, apis: [] }));
  for (const override of [{ method: '$(echo)' }, { authentication: 'jwt' }, { path: '/admin/v1/apis' }, { path: '/api/test\nsh' }, { path: '/api/foo?bar' }, { path: '/api/foo\\bar' }]) assert.equal(projectCatalog({ version: 1, apis: [{ ...api, ...override }] }).apis.length, 0);
  assert.equal(projectCatalog({ version: 1, apis: Array.from({length:1500},(_,i)=>({...api,id:'id'+i,path:'/api/item/'+i})) }).apis.length, 200);
});

test('cURL uses KEY only when required and accurately represents body/header types', () => {
  const code = command({ ...api, parameters: [{ name: 'X-Request-ID', location: 'header', type: 'string', required: true }, { name: 'Authorization', location: 'header', type: 'string', required: true }], body: ['number', 'boolean', 'array', 'object'].map((type) => ({ name: type, location: 'body', type, required: false })) }, 'https://api.example.com');
  assert.match(code, /X-API-Key: 填写你的调用密钥/);
  assert.match(code, /X-Request-ID: example/);
  assert.doesNotMatch(code, /Authorization:/);
  assert.match(code, /"boolean":true/);
  assert.match(code, /"array":\[\]/);
  assert.match(code, /"object":\{\}/);
  assert.doesNotMatch(command({ ...api, authentication: 'none' }, 'https://api.example.com'), /X-API-Key/);
});

test('POSIX quoting keeps shell metacharacters literal, including quotes in parameter names', () => {
  const hostile = "x'$(printf EXPLOITED)'`printf EXPLOITED`;echo EXPLOITED";
  const parsed = execFileSync('/bin/sh', ['-c', `printf '%s' ${shellQuote(hostile)}`], { encoding: 'utf8' });
  assert.equal(parsed, hostile);
  const code = command({ ...api, path: `/api/${hostile}`, body: [{ name: hostile, location: 'body', type: 'string', required: true }] }, 'https://api.example.com');
  const args = execFileSync('/bin/sh', ['-c', `curl(){ printf '%s\\0' "$@"; }; ${code}`], { encoding: 'utf8' }).split('\0').filter(Boolean);
  assert.equal(args[2], `https://api.example.com/api/${hostile}`);
  assert.equal(JSON.parse(args.at(-1))[hostile], 'example');
});

test('website public identity never contains SMTP credentials or private settings', () => {
 const value={version:1,base_url:'https://api.example.test',apis:[],site:{name:'配置网站',public_title:'公开标题',description:'<script>never execute</script>',smtp:{host:'SECRET-host',password:'SECRET-password'},smtp_password:'SECRET-password',encrypted_smtp_password:'SECRET-cipher',admin_token:'SECRET-token'}};
 const result=projectCatalog(value);assert.equal(result.site.name,'配置网站');assert.equal(result.site.public_title,'公开标题');assert.equal(JSON.stringify(result).includes('SECRET'),false);
 assert.equal(result.site.description,'<script>never execute</script>');
});

test('multiple methods and old per-method documents form one interface card',()=>{
 const source={version:1,base_url:'https://api.example.com',apis:[{...api,id:'get',method:'GET',authentication:'none'},{...api,id:'post',method:'POST',authentication:'api_key',body:[{name:'name',location:'body',type:'string',required:true}]}]};
 const result=projectCatalog(source);assert.equal(result.apis.length,1);assert.deepEqual(result.apis[0].methods,['GET','POST']);
 assert.doesNotMatch(command(result.apis[0],result.base_url,'GET'),/X-API-Key/);
 assert.match(command(result.apis[0],result.base_url,'POST'),/X-API-Key/);
 assert.match(command(result.apis[0],result.base_url,'POST'),/"name":"example"/);
});
test('explicit API origin and current website origin replace all domain and value placeholders',()=>{
 const op={...api,method:'GET',path:'/api/item/{id}',parameters:[{name:'domain',location:'query',type:'string',required:true},{name:'url',location:'query',type:'string',required:true},{name:'page',location:'query',type:'integer',required:false}]};
 for(const origin of ['https://api.example.com','http://185.99.135.205:8081']){
  const code=command(op,origin);assert.match(code,new RegExp(origin.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')));assert.doesNotMatch(code,/YOUR_API_DOMAIN|YOUR_VALUE/);
  assert.match(code,/\/api\/item\/1/);assert.match(code,/page=1/);
 }
 const changed=command(op,'https://api.example.com','GET',{'path:id':'42','query:page':'3'});assert.match(changed,/\/item\/42/);assert.match(changed,/page=3/);
});
test('editable parameters cannot inject headers or shell commands',()=>{
 const op={...api,parameters:[{name:'X-Request-ID',location:'header',type:'string',required:true}]};
 assert.equal(command(op,'https://api.example.com','POST',{'header:X-Request-ID':'x\r\nHost:evil.example'}),'参数示例不能包含换行。');
 const code=command({...api,path:'/api/item/{id}'},'https://api.example.com','POST',{'path:id':"'$(echo bad)"});assert.doesNotMatch(code,/\$\(echo bad\)/);
});
