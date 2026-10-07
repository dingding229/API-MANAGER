package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigurableAdminRouting(t *testing.T) {
	for _, base := range []string{"/admin", "/ops", "/staff/console"} {
		mux := http.NewServeMux()
		deny := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
		MountAdministration(mux, base, deny, deny)
		for _, tc := range []struct {
			path   string
			status int
		}{{base + "/", 200}, {base + "/app.js", 200}, {base + "/app.css", 200}, {base + "/controls.css", 200}, {"/admin/v1/apis", 401}, {base + "/unsupported/", 404}} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("%s: %d want %d", tc.path, w.Code, tc.status)
			}
			if tc.path == base+"/" && (!strings.Contains(w.Body.String(), base+"/app.js") || strings.Contains(w.Body.String(), "__ADMIN_PATH__")) {
				t.Fatal("custom assets/base not rendered")
			}
		}
	}
}
func TestUnsafeAdminPathsAreRejected(t *testing.T) {
	for _, path := range []string{"/", "/api", "/auth/v1", "/admin/v1", "/admin/", "/admin/../api", "/admin?secret", "//admin", "/health", "/metrics", "/_next", "/admin%2f"} {
		if ValidAdminPath(path) {
			t.Fatalf("unsafe path accepted: %s", path)
		}
	}
}

func TestConsoleNavigationContainsOnlyApplicationFeatures(t *testing.T) {
	w := httptest.NewRecorder()
	Console().ServeHTTP(w, httptest.NewRequest("GET", "/admin/", nil))
	html := w.Body.String()
	for _, name := range []string{"总览", "接口管理", "调用凭证", "用户管理", "角色与权限", "插件", "运行观测", "审计日志", "网站设置", "用户余额", "套餐管理", "调用日志", "注册与登录", "卡密管理"} {
		if !strings.Contains(html, name) {
			t.Fatalf("missing navigation %s", name)
		}
	}
	if strings.Count(html, "data-page=") != 14 {
		t.Fatal("unexpected extra navigation entry")
	}
}

func TestConsoleLoadsCanonicalControlsAfterPageStyles(t *testing.T) {
	w := httptest.NewRecorder()
	Console().ServeHTTP(w, httptest.NewRequest("GET", "/admin/", nil))
	html := w.Body.String()
	base := strings.Index(html, "/admin/app.css")
	controls := strings.Index(html, "/admin/controls.css")
	if base < 0 || controls <= base {
		t.Fatal("canonical control stylesheet must load after page styles")
	}
	css := httptest.NewRecorder()
	Console().ServeHTTP(css, httptest.NewRequest("GET", "/admin/controls.css", nil))
	if css.Code != 200 || !strings.Contains(css.Body.String(), "--control-height: 44px") {
		t.Fatal("shared control styles missing")
	}
}

func TestUserTableUsesScopedCenteredRowLayout(t *testing.T) {
	css, err := assets.ReadFile("assets/app.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range []string{
		".user-table td { vertical-align: middle; }",
		".user-table .user-cell {\n  display: flex;\n  align-items: center;",
		".user-table .actions {\n  align-items: center;",
		".user-table .actions button {\n  display: inline-flex;\n  align-items: center;\n  justify-content: center;",
		".user-table { --user-row-control-height: 44px; }",
		".console{grid-template-columns:minmax(0,1fr)}",
	} {
		if !strings.Contains(string(css), rule) {
			t.Errorf("missing scoped user table rule: %s", rule)
		}
	}
	if !strings.Contains(string(css), ".observation-table td{vertical-align:top}") {
		t.Error("multi-line observation tables must retain top alignment")
	}
	controlsCSS, err := assets.ReadFile("assets/controls.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(controlsCSS), ".profile-modal .password-input-row") {
		t.Error("profile password controls missing")
	}
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, markup := range []string{`<table class="user-table">`, `<strong>${esc(u.nickname||u.username)}</strong>`, `data-user-filter`, `u.uid||u.id`} {
		if !strings.Contains(string(js), markup) {
			t.Errorf("missing centered user cell markup: %s", markup)
		}
	}
}

func TestUserProfileEditingAssetsAreAvailable(t *testing.T) {
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"account-settings", "data-user-profile", "user-profile-modal", "/auth/v1/me", "current_password", "password_confirm"} {
		if !strings.Contains(string(js), marker) {
			t.Errorf("missing profile editing asset marker: %s", marker)
		}
	}
	controls, err := assets.ReadFile("assets/controls.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(controls), ".profile-modal .password-input-row") {
		t.Error("profile password visibility control styles missing")
	}
	html := httptest.NewRecorder()
	Console().ServeHTTP(html, httptest.NewRequest("GET", "/admin/", nil))
	if !strings.Contains(html.Body.String(), `id="account-settings"`) {
		t.Error("account settings control missing from console shell")
	}
}

func TestPasswordPolicyIsConsistentInConsole(t *testing.T) {
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	html, err := assets.ReadFile("assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "byteLength < 8") || !strings.Contains(string(js), "8–72") {
		t.Fatal("profile password policy not eight bytes")
	}
	if strings.Contains(string(js)+string(html), `minlength="12"`) {
		t.Fatal("old password minimum remains")
	}
	if !strings.Contains(string(html), `id="account-settings"`) {
		t.Fatal("self-service account settings missing")
	}
}

func TestOverviewAndRecoveryAssets(t *testing.T) {
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"/admin/v1/overview", "gateway_requests_total", "overview-panels", "profile-email", "forgot-password", "reset-password"} {
		if !strings.Contains(string(js), marker) {
			t.Errorf("missing feature: %s", marker)
		}
	}
	if strings.Contains(string(js), "用户名或邮箱") {
		t.Fatal("username/email combined control remains")
	}
}

func TestSettingsRefreshDoesNotReplaceAnotherPage(t *testing.T) {
	js, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), "async function renderSiteSettings() {\n  if(state.page!=='settings')return;") {
		t.Fatal("late settings response can replace another active view")
	}
}
