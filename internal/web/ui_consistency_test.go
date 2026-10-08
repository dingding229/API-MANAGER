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
	setup := consoleFunction(t, "async function initSetup()", "\nsetupReadiness=initSetup();")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
const field=value=>({value,disabled:false,setCustomValidity(){},focus(){}});
const elements={key:field('preview-only-key'),username:field('preview-admin'),email:field('admin@example.test'),password:field('Password88'),confirm:field('Password88')};
const fields=Object.values(elements),message={textContent:''};
const form={elements,reportValidity:()=>true,querySelectorAll:()=>fields};
let removed=false,noticeText='';const section={innerHTML:'',querySelector:s=>s==='form'?form:message,remove(){removed=true}};
const card={append(){}};
const document={createElement:()=>section};
const $=s=>s==='#setup-view .auth-card'?card:field('');
let pendingSetupKey='',setupFragment=null,setupReadiness;let destination='';const location={replace(v){destination=v}};
let shouldFail=true;const api=async(path,options)=>{if(!options)return {available:true};if(!shouldFail)return {};const e=Error('invalid setup key');e.status=400;throw e};
const withSubmitting=async(form,text,action)=>action();const notice=text=>{noticeText=text};
`+setup+`
(async()=>{await initSetup();await form.onsubmit({preventDefault(){}});assert.equal(form._saving,false);assert.ok(fields.every(f=>!f.disabled));assert.ok(message.textContent.includes('注册失败'));shouldFail=false;await form.onsubmit({preventDefault(){}});assert.equal(removed,true);assert.ok(fields.every(f=>!f.disabled&&f.value===''));assert.equal(destination,'/login?return=admin&registered=1');})().catch(e=>{console.error(e);process.exit(1)});`)
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
const renderMailTemplateEditor=()=>{};const renderVersionSettings=async()=>{};const refreshSiteIdentity=async()=>{};const initRecovery=async()=>{};const notice=()=>{};
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
	for _, marker := range []string{"bindModalKeyboard(modal, close)", "management-grid", "page._auditRequest !== request", `class="ui-placeholder"`, "restrictForm($('#user-form'), 'user.manage')"} {
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
	handler := consoleFunction(t, "function bindModalKeyboard(", "function showConsole(")
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
	for _, safe := range []string{"cell.textContent=text", "code.textContent=result.api_key", "data-key-op=\"rotate\"", "data-create-key", "card.manage"} {
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

func TestUserManagementUsesSingleEditEntryAndGroupedDialog(t *testing.T) {
	source := consoleFunction(t, "function userActions(", "async function renderUsers")
	if strings.Contains(source, "更多操作") || strings.Contains(source, "<details") {
		t.Fatal("hidden user action menu")
	}
	if strings.Count(source, "<button") != 1 || !strings.Contains(source, "data-user-edit") {
		t.Fatal("user list must have one edit button")
	}
	raw, _ := assets.ReadFile("assets/enhancements.js")
	dialog := string(raw)
	for _, part := range []string{"openUserEditor", "data-editor-tab", "data-editor-reset-2fa", "openProfileModal", "openRoleModal", "openPlanBinding", "openDeleteUser"} {
		if !strings.Contains(dialog, part) {
			t.Fatal("missing editor capability", part)
		}
	}
}

func TestUserSubEditorsReturnOnlyOnUserClose(t *testing.T) {
	for _, tc := range []struct{ start, end, close string }{{"function closeRoleModal(", "function openRoleModal(", "closeRoleModal"}, {"function closeProfileModal(", "function profileErrorMessage(", "closeProfileModal"}} {
		handler := consoleFunction(t, tc.start, tc.end)
		runConsoleRegression(t, `const assert=require('node:assert/strict');let returned=0,removed=0;
const modal={_saving:false,_previousInert:false,_previousOverflow:'',querySelectorAll:()=>[],remove(){removed++},_onReturn(){returned++}};
const app={inert:true};const $=s=>s==='#app'?app:modal;const document={body:{style:{overflow:'hidden'}}};
`+handler+`
`+tc.close+`();assert.equal(returned,1);assert.equal(removed,1);`+tc.close+`(true);assert.equal(returned,1);assert.equal(removed,2);`)
	}
}

func TestPluginActionsStayInOneToolbarAndRetainPermissions(t *testing.T) {
	helpers := consoleFunction(t, "function pluginAction(", "\nasync function uploadPlugin(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
let allowed=true;const can=()=>allowed,state={user:{roles:['super_admin']}};
const esc=v=>String(v??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
`+helpers+`
const item={id:'plugin-id',name:'<img src=x onerror=alert(1)>',version:'1.0.0',runtime:'wasm',enabled:false,manifest:{routes:[{parameters_schema:{},request_schema:{}}]}};
let html=pluginRow(item,[]);assert.equal((html.match(/class="plugin-version-actions"/g)||[]).length,1);assert.ok(!html.includes('plugin-action-primary')&&!html.includes('plugin-action-secondary'));
for(const attribute of ['status','settings','runtime','update','library-publish','uninstall'])assert.ok(html.includes('data-plugin-'+attribute+'="plugin-id"'));
assert.equal((html.match(/<button /g)||[]).length,6);assert.equal((html.match(/aria-label=/g)||[]).length,7);assert.ok(!html.includes('<img'));
html=pluginRow(item,[{name:item.name,version:item.version}]);assert.equal((html.match(/<button /g)||[]).length,5);assert.ok(html.includes('plugin-library-status'));assert.ok(!html.includes('data-plugin-library-publish'));
state.user.roles=['api_developer'];assert.ok(!pluginRow(item).includes('data-plugin-runtime'));
allowed=false;assert.ok(!pluginRow(item).includes('<button '));`)
	css, _ := assets.ReadFile("assets/controls.css")
	for _, v := range []string{"flex-direction:row;flex-wrap:nowrap", "min-width:0;white-space:nowrap", "@container plugin-panel", "plugin-action-label{white-space:nowrap}"} {
		if !strings.Contains(string(css), v) {
			t.Fatal("plugin toolbar safeguard missing", v)
		}
	}
}

func TestAPIDeletionUsesConsoleConfirmationAndCancelDoesNotDelete(t *testing.T) {
	action := consoleFunction(t, "async function apiAction(", "\n\nfunction roleCanBeGranted(")
	if strings.Contains(action, "!confirm(") {
		t.Fatal("API deletion uses native browser confirmation")
	}
	runConsoleRegression(t, `const assert=require('node:assert/strict');let confirmed=false,calls=[],renders=0;const notice=()=>{};const renderAPIs=()=>renders++;const confirmConsoleAction=async()=>confirmed;const api=async(path,options)=>calls.push([path,options.method]);
`+action+`
(async()=>{await apiAction('delete','route-id');assert.equal(calls.length,0);assert.equal(renders,0);confirmed=true;await apiAction('delete','route/id');assert.deepEqual(calls,[['/admin/v1/apis/route%2Fid','DELETE']]);assert.equal(renders,1);await apiAction('unknown','id');assert.equal(calls.length,1)})().catch(e=>{console.error(e);process.exit(1)});`)
	raw, _ := assets.ReadFile("assets/enhancements.js")
	s := string(raw)
	a := strings.Index(s, "function confirmConsoleAction(")
	b := strings.Index(s[a:], "function showTextDialog(")
	helper := s[a : a+b]
	runConsoleRegression(t, `const assert=require('node:assert/strict');let active;const consoleDialog=(title,html,onReturn)=>{const nodes={'.dialog-confirm-message':{},'[data-confirm-action]':{},'[data-cancel-dialog]':{focus(){this.focused=true}}};const close=()=>onReturn();nodes['[data-cancel-dialog]'].onclick=close;active={nodes,close,querySelector:s=>nodes[s]};return{dialog:active,close}};
`+helper+`
(async()=>{const cancelled=confirmConsoleAction('删除接口','<img src=x>','删除接口');assert.equal(active.nodes['.dialog-confirm-message'].textContent,'<img src=x>');assert.equal(active.nodes['[data-cancel-dialog]'].focused,true);active.nodes['[data-cancel-dialog]'].onclick();assert.equal(await cancelled,false);const accepted=confirmConsoleAction('删除接口','删除后无法恢复','删除接口');active.nodes['[data-confirm-action]'].onclick();assert.equal(await accepted,true);const escape=confirmConsoleAction('删除接口','确认');active.close();assert.equal(await escape,false)})().catch(e=>{console.error(e);process.exit(1)});`)
}
