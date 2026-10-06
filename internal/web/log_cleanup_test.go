package web

import (
	"strings"
	"testing"
)

func TestLogCleanupDialogRequiresConfirmationAndRestoresOnFailure(t *testing.T) {
	helpers := consoleFunction(t, "async function withSubmitting(", "function restrictForm(")
	cleanup := consoleFunction(t, "function closeLogCleanupModal(", "async function renderAuditLogs(")
	runConsoleRegression(t, `const assert=require('node:assert/strict');
let allowed=false,current=null,calls=[],shouldFail=true,refreshed=0,noticeText='',focusRestored=false;
const app={inert:false};const previous={isConnected:true,focus(){focusRestored=true}};
const button=()=>({disabled:false,textContent:'确认清理',attrs:{},setAttribute(k,v){this.attrs[k]=v},removeAttribute(k){delete this.attrs[k]},focus(){}});
const submit=button(),cancel=button(),close=button(),confirm={checked:false,disabled:false},error={textContent:'',focus(){}};
const form={elements:{confirm},attrs:{},reportValidity(){return confirm.checked},querySelector(){return submit},setAttribute(k,v){this.attrs[k]=v},removeAttribute(k){delete this.attrs[k]}};
const modal={isConnected:false,querySelector(s){return s==='form'?form:s==='.modal-close'?close:s==='[data-log-cleanup-cancel]'?cancel:error},querySelectorAll(){return [confirm,cancel,close]},addEventListener(){},remove(){this.isConnected=false;current=null}};
const document={activeElement:previous,body:{style:{overflow:''},appendChild(el){current=el;el.isConnected=true}},createElement(){return modal}};
const $=s=>s==='#app'?app:s==='#log-cleanup-modal'?current:null;
const can=()=>allowed,esc=String,formatMetric=String,closeRoleModal=()=>{},closeProfileModal=()=>{},bindModalKeyboard=()=>{};
const state={page:'observability'};const notice=text=>{noticeText=text};const renderObservability=async()=>{refreshed++};
const api=async(path,options)=>{calls.push({path,...options});if(shouldFail)throw Error('storage unavailable');return {cleared:true}};
`+helpers+cleanup+`
(async()=>{
openLogCleanupModal(32);assert.equal(current,null);
allowed=true;submit.disabled=true;openLogCleanupModal(32);
assert.equal(submit.disabled,true);assert.equal(app.inert,true);assert.equal(document.body.style.overflow,'hidden');
assert.ok(modal.innerHTML.includes('不仅是当前筛选结果'));assert.ok(modal.innerHTML.includes('审计日志'));
await form.onsubmit({preventDefault(){},currentTarget:form});assert.equal(calls.length,0);
confirm.checked=true;confirm.onchange();assert.equal(submit.disabled,false);
await form.onsubmit({preventDefault(){},currentTarget:form});
assert.equal(error.textContent,'storage unavailable');assert.equal(submit.disabled,false);assert.equal(confirm.disabled,false);assert.equal(modal._saving,false);assert.equal(current,modal);
shouldFail=false;await form.onsubmit({preventDefault(){},currentTarget:form});
assert.equal(calls.length,2);assert.equal(calls[1].method,'DELETE');assert.equal(calls[1].path,'/admin/v1/observability/logs');assert.deepEqual(JSON.parse(calls[1].body),{confirm:true});
assert.equal(current,null);assert.equal(app.inert,false);assert.equal(document.body.style.overflow,'');assert.equal(focusRestored,true);assert.equal(refreshed,1);assert.ok(noticeText.includes('应用日志已清理'));
})().catch(e=>{console.error(e);process.exit(1)});
`)
}

func TestSettingsPageUsesSharedContentWidthAndCleanupIsPermissionGated(t *testing.T) {
	css, err := assets.ReadFile("assets/controls.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".settings-page {display:grid;gap:20px;width:100%;min-width:0;}") {
		t.Fatal("settings page still has a separate fixed width cap")
	}
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"can('observability.logs.clear')", "data-clear-application-logs", "closeLogCleanupModal(true)", "Number(storage.logs || 0) === 0", "审计日志、请求链路与统计数据会保留"} {
		if !strings.Contains(string(js), marker) {
			t.Errorf("missing cleanup guard or scope hint: %s", marker)
		}
	}
}
