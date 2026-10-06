import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {projectCatalog} from '../lib/catalog.ts';
test('guide covers real API onboarding, authentication, errors and FAQs without development prompts',()=>{
 const source=readFileSync(new URL('../app/page.tsx',import.meta.url),'utf8');
 for(const section of ['guide-start','guide-auth','guide-example','guide-errors','guide-faq'])assert.ok(source.includes(section));
 assert.match(source,/snippet\(sample,baseUrl,method,'curl'\)/);assert.doesNotMatch(source,/YOUR_API_DOMAIN|YOUR_VALUE|prompt|codex|ChatGPT/);
});
test('public test capability is strictly a boolean allowlist and no credentials survive projection',()=>{
 const result=projectCatalog({version:1,apis:[{id:'public',title:'Test',path:'/api/test',operations:[{method:'GET',authentication:'api_key',test_enabled:true,parameters:[],body:[],internal_token:'SECRET'}]}]});
 assert.equal(result.apis[0].operations[0].test_enabled,true);assert.equal(JSON.stringify(result).includes('SECRET'),false);
});
