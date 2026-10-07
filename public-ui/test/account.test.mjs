import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {validPasswordBytes} from '../lib/account-validation.ts';
const source=readFileSync(new URL('../components/account-experience.tsx',import.meta.url),'utf8');
test('user center stores no login, TOTP or recovery secrets in browser storage',()=>{assert.doesNotMatch(source,/localStorage|sessionStorage|dangerouslySetInnerHTML/);assert.match(source,/credentials:'same-origin'/);assert.match(source,/cache:'no-store'/);assert.match(source,/api-manager-auth/)});
test('CAPTCHA is per-action, OAuth has none and security only challenges password change',()=>{assert.match(source,/epoch,reset/);assert.match(source,/expired-callback/);assert.match(source,/error-callback/);assert.match(source,/finally\{captcha.reset\(\)\}/);assert.doesNotMatch(source,/oauthCaptcha|emailCaptcha|totpCaptcha|profileCaptcha/);assert.match(source,/operation==='password'&&<Captcha/)});
test('buying and revoking require explicit confirmation and purchase retry reuses operation',()=>{assert.match(source,/确认购买套餐/);assert.match(source,/确认 \/ 重试此购买/);assert.match(source,/operation_id:purchase.operation/);assert.match(source,/吊销调用凭据/);assert.match(source,/manageCaptcha/)});
test('critical profile changes include password, MFA and mailbox proofs',()=>{assert.match(source,/value.verification_code=fields.get\('code'\)/);assert.match(source,/current_password/);assert.match(source,/totp_code/);assert.match(source,/email!==sentEmail/);assert.match(source,/登录验证|双重验证/);assert.match(source,/reset_password/)});

test('password length matches UTF-8 server validation, including Chinese',()=>{assert.equal(validPasswordBytes('1234567'),false);assert.equal(validPasswordBytes('12345678'),true);assert.equal(validPasswordBytes('中文密'),true);assert.equal(validPasswordBytes('中文'),false);assert.equal(validPasswordBytes('密'.repeat(24)),true);assert.equal(validPasswordBytes('密'.repeat(25)),false);assert.doesNotMatch(source,/minLength=\{8\}/)});

test('account changes destroy secrets, log state and pending confirmation dialogs',()=>{assert.match(source,/identity.current!==next.uid/);assert.match(source,/<CallLogs key=\{profile.uid\}/);assert.match(source,/<Keys key=\{profile.uid\}/);assert.match(source,/setRecovery\(\[\]\)/)});

test('credential secret stays in the dialog and loading does not unmount the directory',()=>{assert.match(source,/setVisibleKey\(result.api_key\)/);assert.match(source,/复制密钥/);assert.match(source,/dataLoading&&<p/);assert.doesNotMatch(source,/dataLoading\?<p/);assert.match(source,/新建调用凭据/)});
test('actions and navigation require explicit account permissions',()=>{for(const p of ['account.keys.write','account.keys.reveal','account.billing.purchase','account.billing.redeem','account.security'])assert.ok(source.includes(p));assert.match(source,/重置密钥/);assert.match(source,/confirm:true/);assert.match(source,/siteDateInputToISO/);assert.match(source,/PlanUsage/);assert.match(source,/卡密兑换/)});
test('plan card layout does not reintroduce the stretched single-card override',()=>{const css=readFileSync(new URL('../app/account/account.css',import.meta.url),'utf8');assert.doesNotMatch(css,/account-plan-card:only-child/);assert.match(css,/account-plan-price strong\{white-space:nowrap/)});
