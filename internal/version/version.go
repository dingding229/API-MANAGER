// Package version exposes build identity and a bounded check of official stable tags.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Release builds replace these values via -ldflags. Local builds are never
// reported as a verified release, even when their base version matches a tag.
var Version = "0.3.39-dev"
var Revision = "development"
var BuiltAt = ""

const repository = "https://api.github.com/repos/dingding229/API-MANAGER/tags?per_page=100"
const tagsURL = "https://github.com/dingding229/API-MANAGER/tags"

var stable = regexp.MustCompile(`^v?([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})$`)

type Info struct {
	Current     string     `json:"current"`
	Revision    string     `json:"revision"`
	BuiltAt     string     `json:"built_at,omitempty"`
	Latest      string     `json:"latest,omitempty"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	NextCheckAt *time.Time `json:"next_check_at,omitempty"`
	Source      string     `json:"source"`
}

func Current() Info {
	return Info{Current: Version, Revision: Revision, BuiltAt: BuiltAt, Status: "unchecked", Message: "尚未检查更新。", Source: tagsURL}
}

type Checker struct {
	mu      sync.Mutex
	client  *http.Client
	token   string
	cached  Info
	expires time.Time
}

func NewChecker(token string) *Checker {
	return &Checker{token: token, client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}}
}
func (c *Checker) SetToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = token
	c.expires = time.Time{}
	c.cached = Info{}
}
func (c *Checker) TokenConfigured() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.token != "" }

var errCredentialMissing = errors.New("credential_missing")
var errCredentialInvalid = errors.New("credential_invalid")
var errSourceDenied = errors.New("source_denied")
var errRateLimited = errors.New("rate_limited")

func parts(v string) ([3]int, bool) {
	m := stable.FindStringSubmatch(v)
	var p [3]int
	if m == nil {
		return p, false
	}
	for i := range p {
		p[i], _ = strconv.Atoi(m[i+1])
	}
	return p, true
}
func compare(a, b [3]int) int {
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}
func (c *Checker) Check(ctx context.Context) Info {
	// Only one network check per process/hour (five minutes after a failure),
	// including simultaneous users. Never accept a client-supplied URL or token.
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.Before(c.expires) {
		return c.cached
	}
	info := Current()
	info.CheckedAt = &now
	info.Status = "unavailable"
	info.Message = "无法检查最新版本，请检查网络及私有仓库读取凭据。"
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	latest, err := c.fetch(ctx)
	ttl := 5 * time.Minute
	if err != nil {
		switch {
		case errors.Is(err, errCredentialMissing):
			info.Status = "credential_required"
			info.Message = "请在网站设置中配置私有仓库读取凭据。"
		case errors.Is(err, errCredentialInvalid):
			info.Status = "credential_invalid"
			info.Message = "仓库读取凭据无效或已过期，请更新凭据。"
		case errors.Is(err, errSourceDenied):
			info.Status = "access_denied"
			info.Message = "读取被拒绝，请确认凭据可访问 API-MANAGER 私有仓库。"
		case errors.Is(err, errRateLimited):
			info.Status = "rate_limited"
			info.Message = "版本来源请求过于频繁，请稍后再试。"
		default:
			info.Status = "unavailable"
			info.Message = "版本来源暂不可用，请检查服务器网络后重试。"
		}
	}
	if err == nil {
		ttl = time.Hour
		info.Latest = strings.TrimPrefix(latest, "v")
		current, ok := parts(info.Current)
		last, _ := parts(latest)
		switch {
		case !ok || info.Revision == "development":
			info.Status = "development"
			info.Message = "当前为本地构建，无法确认与正式版本一致。"
		case compare(current, last) < 0:
			info.Status = "update_available"
			info.Message = "发现新版本。"
		case compare(current, last) > 0:
			info.Status = "ahead"
			info.Message = "当前版本高于仓库最新稳定标签。"
		default:
			info.Status = "current"
			info.Message = "当前版本与最新稳定标签一致。"
		}
	}
	c.expires = now.Add(ttl)
	next := c.expires
	info.NextCheckAt = &next
	c.cached = info
	return info
}
func (c *Checker) fetch(ctx context.Context) (string, error) {
	latest := ""
	var maximum [3]int
	// Fail rather than silently claim 'latest' if the bounded scan is incomplete.
	for page := 1; page <= 10; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, repository+"&page="+strconv.Itoa(page), nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "API-Manager-Version-Check")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, (256<<10)+1))
		_ = resp.Body.Close()
		switch resp.StatusCode {
		case 401:
			return "", errCredentialInvalid
		case 404:
			if c.token == "" {
				return "", errCredentialMissing
			}
			return "", errSourceDenied
		case 403:
			if resp.Header.Get("X-RateLimit-Remaining") == "0" {
				return "", errRateLimited
			}
			return "", errSourceDenied
		case 429:
			return "", errRateLimited
		}
		if readErr != nil || len(data) > 256<<10 || resp.StatusCode != 200 {
			return "", errors.New("version source unavailable")
		}
		var tags []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data, &tags) != nil {
			return "", errors.New("invalid version response")
		}
		for _, t := range tags {
			if p, ok := parts(t.Name); ok && (latest == "" || compare(p, maximum) > 0) {
				latest = t.Name
				maximum = p
			}
		}
		if len(tags) < 100 {
			if latest == "" {
				return "", errors.New("no stable tag")
			}
			return latest, nil
		}
	}
	return "", errors.New("incomplete version source")
}
