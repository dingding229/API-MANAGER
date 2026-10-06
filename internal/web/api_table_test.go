package web

import (
	"os/exec"
	"strings"
	"testing"
)

func TestAPIControlsHaveScopedAlignmentAndStableColumnWidths(t *testing.T) {
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`<table class="api-table">`, `<col class="api-auth-column">`, `<col class="api-status-column">`, `<col class="api-actions-column">`, `class="api-control-cell"`, `class="api-row-actions" role="group"`, `class="api-status-dot" aria-hidden="true"`, `role="region" aria-label="已配置接口" tabindex="0"`} {
		if !strings.Contains(string(js), marker) {
			t.Errorf("missing scoped API table markup: %s", marker)
		}
	}
	css, err := assets.ReadFile("assets/controls.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{`.api-table-wrap .api-table { table-layout: fixed; min-width: 790px; }`, `.api-table .api-auth-column { width: 96px; }`, `.api-table .api-status-column { width: 104px; }`, `.api-table .api-actions-column { width: 210px; }`, `.api-table th, .api-table td { padding: 16px; vertical-align: middle; }`, `.api-table .api-row-actions { display: flex; align-items: center; justify-content: flex-start; gap: 8px; min-height: 44px; flex-wrap: nowrap; }`, `.api-table .api-row-button:hover:not(:disabled)`} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("missing scoped API layout rule: %s", rule)
		}
	}
	if !strings.Contains(string(css), "white-space: nowrap;") {
		t.Fatal("API controls can wrap")
	}
}

func TestAPIRowPreservesPermissionsEscapingAndActionIdentifiers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node unavailable; renderer test requires Node")
	}
	source, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(source)
	start := strings.Index(js, "function apiRow(item) {")
	end := strings.Index(js, "\nfunction selected(")
	if start < 0 || end <= start {
		t.Fatal("API row renderer not found")
	}
	script := `const assert=require('node:assert/strict');
const esc=value=>String(value??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
let permissions=[];const can=value=>permissions.includes(value);
const authLabel=value=>value==='none'?'无需验证':'KEY';
` + js[start:end] + `
const item={id:'api_test',name:'测试接口',methods:['GET','POST'],path:'/api/test/{id}',auth_mode:'api_key',enabled:true,plugin:'example-plugin'};
permissions=['api.write','api.publish','api.delete'];
let rendered=apiRow(item);
assert.ok(rendered.includes('data-edit-api="api_test"'));
assert.ok(rendered.includes('data-action="unpublish"'));
assert.ok(rendered.includes('data-action="delete"'));
assert.ok(rendered.includes('GET / POST /api/test/{id}'));
assert.ok(rendered.includes('aria-label="编辑接口 测试接口"'));
assert.ok(rendered.includes('is-live'));
assert.ok(rendered.includes('已发布'));
assert.equal((rendered.match(/<button /g)||[]).length,3);
rendered=apiRow({...item,enabled:false,auth_mode:'none'});
assert.ok(rendered.includes('data-action="publish"'));
assert.ok(rendered.includes('is-draft'));
assert.ok(rendered.includes('无需验证'));
permissions=['api.read'];rendered=apiRow(item);
assert.equal((rendered.match(/<button /g)||[]).length,0);
assert.ok(rendered.includes('api-no-actions'));
permissions=['api.write'];rendered=apiRow({...item,id:'" data-action="delete',name:'<img src=x onerror=alert(1)>',plugin:'<script>alert(1)</script>'});
assert.equal((rendered.match(/<button /g)||[]).length,1);
assert.ok(!rendered.includes('<img'));
assert.ok(!rendered.includes('<script>'));
assert.ok(rendered.includes('&lt;img'));
assert.ok(rendered.includes('&quot;'));
`
	if output, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("API row regression failed: %v\n%s", err, output)
	}
}
