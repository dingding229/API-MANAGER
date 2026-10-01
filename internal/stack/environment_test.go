package stack

import (
	"strings"
	"testing"
)

func TestChildEnvironmentExcludesSecrets(t *testing.T) {
	t.Setenv("POSTGRES_PASSWORD", "do-not-inherit")
	t.Setenv("ADMIN_BOOTSTRAP_KEY", "do-not-inherit")
	t.Setenv("SMTP_PASSWORD", "do-not-inherit")
	t.Setenv("TZ", "Asia/Shanghai")
	env := strings.Join(childEnvironment(), "\n")
	if strings.Contains(env, "do-not-inherit") || !strings.Contains(env, "TZ=Asia/Shanghai") {
		t.Fatalf("unsafe child environment: %s", env)
	}
}
