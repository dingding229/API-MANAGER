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
var Version = "0.3.27-dev"
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
