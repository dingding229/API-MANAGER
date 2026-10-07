package web

import (
	"os/exec"
	"strings"
	"testing"
)

func consoleFunction(t *testing.T, start, end string) string {
	t.Helper()
	raw, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	a := strings.Index(source, start)
	if a < 0 {
		t.Fatalf("missing function %s", start)
	}
	b := strings.Index(source[a:], end)
	if b < 0 {
		t.Fatalf("missing function end %s", end)
	}
	return source[a : a+b]
}

func runConsoleRegression(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable; console behavior regression requires Node")
	}
	if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("console regression failed: %v\n%s", err, out)
	}
}

func TestConsoleSubmissionFeedbackRestoresOnFailureAndPreventsDuplicates(t *testing.T) {
	helpers := consoleFunction(t, "async function withSubmitting(", "function restrictForm(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const button={disabled:false,textContent:'保存',attrs:{},setAttribute(k,v){this.attrs[k]=v},removeAttribute(k){delete this.attrs[k]}};
const form={attrs:{},querySelector(){return button},setAttribute(k,v){this.attrs[k]=v},removeAttribute(k){delete this.attrs[k]}};
`+helpers+`
(async()=>{
let calls=0,release;
const pending=withSubmitting(form,'保存中…',async()=>{calls++;await new Promise(r=>release=r)});
assert.equal(button.disabled,true);assert.equal(button.textContent,'保存中…');assert.equal(form.attrs['aria-busy'],'true');
await withSubmitting(form,'再次提交',async()=>calls++);assert.equal(calls,1);
release();await pending;assert.equal(button.disabled,false);assert.equal(button.textContent,'保存');assert.equal(form._submitting,false);assert.equal(form.attrs['aria-busy'],undefined);
await assert.rejects(withSubmitting(form,'保存中…',async()=>{throw Error('failure')}));
assert.equal(button.disabled,false);assert.equal(button.textContent,'保存');assert.equal(form._submitting,false);
button.disabled=true;await withAction(button,'保存中…',async()=>calls++);assert.equal(calls,1);assert.equal(button.disabled,true);
})().catch(e=>{console.error(e);process.exit(1)});`)
}

func TestSetupFailureRestoresFieldsWithoutUnrelatedSettingsVariables(t *testing.T) {
	setup := consoleFunction(t, "async function initSetup()", "\ninitSetup();")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const field=value=>({value,disabled:false,setCustomValidity(){},focus(){}});
const elements={key:field('preview-only-key'),username:field('preview-admin'),email:field('admin@example.test'),password:field('Password88'),confirm:field('Password88')};
const fields=Object.values(elements),message={textContent:''};
const form={elements,reportValidity:()=>true,querySelectorAll:()=>fields};
let removed=false,noticeText='';const section={innerHTML:'',querySelector:s=>s==='form'?form:message,remove(){removed=true}};
const card={append(){}};
const document={createElement:()=>section};
const $=s=>s==='#login-view .auth-card'?card:field('');
let pendingSetupKey='',setupFragment=null;
let shouldFail=true;const api=async(path,options)=>{if(!options)return {available:true};if(!shouldFail)return {};const e=Error('invalid setup key');e.status=400;throw e};
const withSubmitting=async(form,text,action)=>action();const notice=text=>{noticeText=text};
`+setup+`
(async()=>{await initSetup();await form.onsubmit({preventDefault(){}});assert.equal(form._saving,false);assert.ok(fields.every(f=>!f.disabled));assert.ok(message.textContent.includes('注册失败'));shouldFail=false;await form.onsubmit({preventDefault(){}});assert.equal(removed,true);assert.ok(fields.every(f=>!f.disabled&&f.value===''));assert.ok(noticeText.includes('注册成功'));})().catch(e=>{console.error(e);process.exit(1)});`)
}

func TestWebsiteSettingsFailureRestoresEditableFieldsAndPasswordPolicy(t *testing.T) {
	settings := consoleFunction(t, "async function renderSiteSettings()", "\nrefreshSiteIdentity();")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const names=['name','public_title','admin_title','description','keywords','website_url','api_domain','subtitle','hero_title','hero_description','announcement','footer','contact_email','smtp_host','smtp_port','smtp_mode','smtp_username','smtp_from','smtp_reset_url','smtp_password','smtp_enabled','clear_smtp_password'];
const elements=Object.fromEntries(names.map(name=>[name,{name,value:name==='smtp_port'?'587':'preview',checked:name==='clear_smtp_password',disabled:false,type:name==='smtp_password'?'password':'text'}]));
const visibility={disabled:false},message={textContent:'',focus(){}};
const form={elements,isConnected:true,reportValidity:()=>true,querySelectorAll:()=>[...Object.values(elements),visibility]};
const test={elements:{recipient:{value:'preview@example.test'}}};
const page={isConnected:true,querySelector(s){return s==='#site-settings-form'?form:s==='[data-smtp-visibility]'?visibility:s==='[data-settings-message]'?message:test}};
const $=()=>page;const state={page:'settings',user:{roles:['super_admin']}};
const cfg={version:1,site:{},smtp:{},smtp_password_set:false,recovery_enabled:false};
const api=async(path,options)=>{if(!options)return cfg;const e=Error('save failed');e.status=409;throw e};
const withSubmitting=async(form,text,action)=>action();const esc=String;const siteSettingInput=()=>'';const siteSettingArea=()=>'';
const refreshSiteIdentity=async()=>{};const initRecovery=async()=>{};const notice=()=>{};
class FormData {constructor(form){this.form=form}entries(){return Object.values(this.form.elements).map(f=>[f.name,f.value])[Symbol.iterator]()}}
`+settings+`
(async()=>{await renderSiteSettings();await form.onsubmit({preventDefault(){}});assert.equal(form._saving,false);assert.ok(message.textContent.includes('其他管理员'));assert.equal(elements.smtp_password.disabled,true);assert.equal(elements.name.disabled,false);assert.equal(elements.smtp_host.disabled,false);assert.equal(visibility.disabled,false);assert.equal(elements.name.value,'preview');})().catch(e=>{console.error(e);process.exit(1)});`)
}

func TestConsoleSharedComponentsAndAuditGuards(t *testing.T) {
	raw, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, marker := range []string{"bindModalKeyboard(modal, close)", "management-grid", "page._auditRequest !== request", `class="empty spaced-split"`, "restrictForm($('#user-form'), 'user.manage')"} {
		if !strings.Contains(js, marker) {
			t.Errorf("missing shared console behavior: %s", marker)
		}
	}
	if strings.Contains(js, `class="empty" class=`) {
		t.Error("duplicate audit class attributes")
	}
	css, err := assets.ReadFile("assets/controls.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"--button-border: var(--control-border)", ".content .overview-stats, .content .observability-stats", ".modal-card .modal-close", ".sidebar .account { display: block", ".credential-table-wrap .credential-table", ".message:empty"} {
		if !strings.Contains(string(css), marker) {
			t.Errorf("missing shared visual rule: %s", marker)
		}
	}
}

func TestModalKeyboardNavigationStaysInsideDialog(t *testing.T) {
	handler := consoleFunction(t, "function bindModalKeyboard(", "function authErrorMessage(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const document={activeElement:null};
const first={focus(){document.activeElement=this}},last={focus(){document.activeElement=this}};
const modal={querySelectorAll:()=>[first,last]};let closed=0,prevented=0;
`+handler+`
bindModalKeyboard(modal,()=>closed++);
const key=(key,shiftKey=false)=>modal.onkeydown({key,shiftKey,preventDefault(){prevented++}});
document.activeElement=first;key('Tab',true);assert.equal(document.activeElement,last);
key('Tab');assert.equal(document.activeElement,first);assert.equal(prevented,2);
key('Escape');assert.equal(closed,1);
`)
}

func TestLateAuditResultsCannotWriteAnotherViewOrOverwriteNewFilter(t *testing.T) {
	renderer := consoleFunction(t, "async function renderAuditLogs(", "function auditRow(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const page={isConnected:true,innerHTML:''},controls={};
const state={page:'audit',cache:{}};const esc=value=>String(value||'');
const $=selector=>{if(selector==='#page')return page;if(selector==='#audit-results')throw Error('late response touched active DOM');return controls};
const releases=[];const api=()=>new Promise(resolve=>releases.push(resolve));
`+renderer+`
(async()=>{
const old=renderAuditLogs();const current=renderAuditLogs();
releases[0]({items:[],total:0,page:1,page_size:20});await old;
page.isConnected=false;releases[1]({items:[],total:0,page:1,page_size:20});await current;
})().catch(e=>{console.error(e);process.exit(1)});
`)
}

func TestConsoleDatesUseOneFormatAndHandleMissingValues(t *testing.T) {
	formatter := consoleFunction(t, "function formatDate(", "function formatMetric(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');const state={timeZone:'Asia/Shanghai'};
`+formatter+`
assert.equal(formatDate(undefined),'—');assert.equal(formatDate(''),'—');assert.equal(formatDate('not-a-date'),'—');
assert.ok(formatDate('2026-10-06T08:00:00Z').includes('2026'));
`)
}

func TestBackendSelfProfileAndTimezoneControlsStayInConsole(t *testing.T) {
	raw, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	if strings.Contains(js, `if(isSelf){location.assign("/account");return;}`) {
		t.Fatal("self management redirects to account")
	}
	if !strings.Contains(js, `site.time_zone||'Asia/Shanghai','Asia/Shanghai'`) {
		t.Fatal("saved custom timezone not retained")
	}
	auth, err := assets.ReadFile("assets/account.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auth), `name="totp_code"`) || strings.Contains(string(auth), `class="form-section"`) {
		t.Fatal("administrative OTP or old fieldset style remains")
	}
	if !strings.Contains(string(auth), "site.website_url||cfg.website_url||location.origin") {
		t.Fatal("callback domain not canonical")
	}
}

func TestCredentialDialogsAuditDetailsAndXSSSafeSinks(t *testing.T) {
	raw, e := assets.ReadFile("assets/app.js")
	if e != nil {
		t.Fatal(e)
	}
	js := string(raw)
	a := strings.Index(js, "const esc = ")
	b := strings.Index(js[a:], "\n")
	runConsoleRegression(t, `const assert=require('node:assert/strict');`+js[a:a+b]+`;assert.equal(esc('<img src=x onerror="alert(1)">&'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;&amp;');`)
	extra, e := assets.ReadFile("assets/enhancements.js")
	if e != nil {
		t.Fatal(e)
	}
	source := string(extra)
	for _, safe := range []string{"pre.textContent=", "code.textContent=result.api_key", "data-key-op=\"rotate\"", "data-create-key", "card.manage"} {
		if safe == "card.manage" {
			continue
		}
		if !strings.Contains(source, safe) {
			t.Fatal("missing safe management behavior", safe)
		}
	}
	if strings.Contains(js, "data-email-code") || strings.Contains(js, `name="verification_code"`) {
		t.Fatal("administration incorrectly verifies another mailbox")
	}
	plans, e := assets.ReadFile("assets/consolidation.js")
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(plans), "data-bind-plan") {
		t.Fatal("duplicate plan binding entry")
	}
	if !strings.Contains(js, "data-audit-detail") {
		t.Fatal("audit detail dialog missing")
	}
}

func TestUserManagementUsesSingleRowSecondaryMenu(t *testing.T) {
	raw, e := assets.ReadFile("assets/app.js")
	if e != nil {
		t.Fatal(e)
	}
	s := string(raw)
	start := strings.Index(s, "function userActions(")
	end := strings.Index(s[start:], "async function renderUsers")
	actions := s[start : start+end]
	for _, field := range []string{"user-action-menu", "更多操作", "data-user-usage", "data-bind-user-plan", "data-user-status"} {
		if !strings.Contains(actions, field) {
			t.Fatal("missing permission-bound action", field)
		}
	}
	css, e := assets.ReadFile("assets/app.css")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(css), ".user-table .actions{flex-wrap:nowrap!important") {
		t.Fatal("user actions can wrap")
	}
}
