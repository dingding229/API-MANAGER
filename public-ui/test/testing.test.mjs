import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { liveRequest, boundedResponse } from '../lib/testing.ts';
const api = { id: 'safe', path: '/api/item/{id}', method: 'POST', authentication: 'api_key', parameters: [{name:'id',type:'integer',location:'path',required:true}], body:[{name:'message',type:'string',location:'body',required:true}] };
test('real tests use only a declared API origin and KEY/none, never admin sessions', () => {
  const request=liveRequest(api,'https://api.example.com','POST',{'path:id':'12','body:message':'hello'},'my-api-key','https://docs.example.com');
  assert.equal(request.url,'https://api.example.com/api/item/12');assert.deepEqual(request.headers.find(([name])=>name==='X-API-Key'),['X-API-Key','my-api-key']);assert.equal(JSON.parse(request.body).message,'hello');
  assert.throws(()=>liveRequest(api,'https://api.example.com','PUT',{},'key','https://docs.example.com'));
  for(const key of ['', 'x\r\nCookie: secret','含中文','x'.repeat(513)])assert.throws(()=>liveRequest(api,'https://api.example.com','POST',{},key,'https://docs.example.com'));
  assert.throws(()=>liveRequest(api,'http://api.example.com','POST',{},'key','https://docs.example.com'));
  assert.throws(()=>liveRequest({...api,method:'GET'},'https://api.example.com','GET',{},'key','https://docs.example.com'));
  assert.equal(liveRequest({...api,authentication:'none'},'https://api.example.com','POST',{},'ignored','https://docs.example.com').headers.some(([name])=>name==='X-API-Key'),false);
});
test('bounded response handles empty, multibyte and oversized bodies', async () => {
  assert.deepEqual(await boundedResponse(new Response(null)),{text:'',truncated:false});
  assert.deepEqual(await boundedResponse(new Response('实际返回'),100),{text:'实际返回',truncated:false});
  const large=await boundedResponse(new Response('a'.repeat(1024)),20);assert.equal(large.text.length,20);assert.equal(large.truncated,true);
});
test('testing remains an explicit action with volatile credentials and safe output',()=>{
  const source=readFileSync(new URL('../app/testing.tsx',import.meta.url),'utf8');
  assert.doesNotMatch(source,/\/admin\/v1|\/auth\/v1|localStorage|sessionStorage|dangerouslySetInnerHTML/);
  assert.match(source,/\/test\/v1\/session/);assert.match(source,/credentials: 'omit'/);assert.match(source,/redirect:\s*'error'/);assert.match(source,/cache: 'no-store'/);assert.match(source,/confirmed/);assert.match(source,/data\.status/);assert.match(source,/\/test\/v1\/prepare/);assert.match(source,/\/test\/v1\/invoke/);assert.doesNotMatch(source,/fetch\(request\.url/);
});

test('both UIs share cookie authentication and react to login events',()=>{
 const source=readFileSync(new URL('../app/testing.tsx',import.meta.url),'utf8');
 assert.match(source,/BroadcastChannel\('api-manager-auth'\)/);assert.match(source,/visibilitychange/);assert.match(source,/session\.refresh\(\)/);assert.match(source,/X-API-Request/);
 assert.doesNotMatch(source,/sessionStorage|localStorage|Authorization/);
});
