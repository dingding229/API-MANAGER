import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {validPasswordBytes} from '../lib/account-validation.ts';
const source=readFileSync(new URL('../app/account/page.tsx',import.meta.url),'utf8');
test('user center stores no login, TOTP or recovery secrets in browser storage',()=>{assert.doesNotMatch(source,/localStorage|sessionStorage|dangerouslySetInnerHTML/);assert.match(source,/credentials:'same-origin'/);assert.match(source,/cache:'no-store'/);assert.match(source,/api-manager-auth/)});
test('Turnstile tokens are separated by operation and reset after every request',()=>{for(const name of ['emailCaptcha','oauthCaptcha','totpCaptcha','profileCaptcha','manageCaptcha'])assert.match(source,new RegExp(name));assert.match(source,/epoch,reset/);assert.match(source,/expired-callback/);assert.match(source,/error-callback/);assert.match(source,/finally\{captcha.reset\(\)\}/)});
test('buying and revoking require explicit confirmation and purchase retry reuses operation',()=>{assert.match(source,/确认购买套餐/);assert.match(source,/确认 \/ 重试此购买/);assert.match(source,/operation_id:purchase.operation/);assert.match(source,/吊销调用凭据/);assert.match(source,/manageCaptcha/)});
test('critical profile changes include password, MFA and mailbox proofs',()=>{assert.match(source,/verification_code:fields.get\('code'\)/);assert.match(source,/current_password/);assert.match(source,/totp_code/);assert.match(source,/proofEmail/);assert.match(source,/登录验证|双重验证/);assert.match(source,/reset_password/)});

test('password length matches UTF-8 server validation, including Chinese',()=>{assert.equal(validPasswordBytes('1234567'),false);assert.equal(validPasswordBytes('12345678'),true);assert.equal(validPasswordBytes('中文密'),true);assert.equal(validPasswordBytes('中文'),false);assert.equal(validPasswordBytes('密'.repeat(24)),true);assert.equal(validPasswordBytes('密'.repeat(25)),false);assert.doesNotMatch(source,/minLength=\{8\}/)});

test('account changes destroy secrets, log state and pending confirmation dialogs',()=>{assert.match(source,/identity.current!==next.uid/);assert.match(source,/<CallLogs key=\{profile.uid\}/);assert.match(source,/<Keys key=\{profile.uid\}/);assert.match(source,/setRecovery\(\[\]\)/)});

test('credential secret stays in the dialog and loading does not unmount the directory',()=>{assert.match(source,/setVisibleKey\(result.api_key\)/);assert.match(source,/复制密钥/);assert.match(source,/dataLoading&&<p/);assert.doesNotMatch(source,/dataLoading\?<p/);assert.match(source,/新建调用凭据/)});
