package account

import (
	"api-manager/internal/model"
	"errors"
	"net"
	"regexp"
	"strings"
)

var emailDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func normalizeEmailDomains(values []string) ([]string, error) {
	if len(values) > 64 {
		return nil, errors.New("最多填写 64 个邮箱后缀")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		v := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "@"))
		if len(v) > 253 || !strings.Contains(v, ".") || net.ParseIP(v) != nil {
			return nil, errors.New("请填写有效邮箱后缀，例如 example.com")
		}
		for _, label := range strings.Split(v, ".") {
			if !emailDomainLabel.MatchString(label) {
				return nil, errors.New("邮箱后缀只填写域名，不包含通配符、网址或端口")
			}
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}
func emailDomainAllowed(cfg model.SecuritySettings, email string) bool {
	if len(cfg.AllowedEmailDomains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, allowed := range cfg.AllowedEmailDomains {
		if domain == allowed {
			return true
		}
	}
	return false
}
