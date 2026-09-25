// Package upstream handles server-owned credentials and escaped path rewrites.
package upstream

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type Credential struct {
	Origin string `json:"origin"`
	APIKey string `json:"api_key"`
}

type Credentials map[string]Credential

// Parse never includes secret material in errors. Origins pin credentials to an
// operator-approved destination; route editors cannot redirect them elsewhere.
func Parse(raw string) (Credentials, error) {
	result := Credentials{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return nil, errors.New("invalid upstream credentials JSON")
	}
	for name, c := range result {
		u, err := url.Parse(c.Origin)
		if err != nil || name == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || c.APIKey == "" || strings.ContainsAny(c.APIKey, "\r\n") {
			return nil, errors.New("upstream credentials require a name, HTTP(S) origin and non-empty API key")
		}
		c.Origin = strings.ToLower(u.Scheme + "://" + u.Host)
		result[name] = c
	}
	return result, nil
}

func (c Credentials) Key(ref string, target *url.URL) (string, error) {
	if ref == "" {
		return "", nil
	}
	item, ok := c[ref]
	if !ok || target.User != nil || strings.ToLower(target.Scheme+"://"+target.Host) != item.Origin {
		return "", errors.New("upstream credential missing or destination not allowed")
	}
	return item.APIKey, nil
}

func ValidatePath(pattern, template string) error {
	if template == "" {
		return nil
	}
	if !strings.HasPrefix(template, "/") || strings.ContainsAny(template, "?#") {
		return errors.New("upstream_path must be an absolute path without query or fragment")
	}
	names := map[string]bool{}
	for _, segment := range strings.Split(pattern, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			names[segment] = true
		}
	}
	for _, segment := range strings.Split(template, "/") {
		if strings.ContainsAny(segment, "{}") && !names[segment] {
			return errors.New("upstream_path parameters must match complete route parameter segments")
		}
	}
	return nil
}

// Path substitutes escaped segments rather than decoded values, so an encoded
// slash inside a parameter cannot become a new upstream path component.
func Path(pattern, escapedPath, template string) (string, error) {
	if err := ValidatePath(pattern, template); err != nil {
		return "", err
	}
	patterns, values := strings.Split(pattern, "/"), strings.Split(escapedPath, "/")
	parts := strings.Split(template, "/")
	for i, segment := range parts {
		if !strings.HasPrefix(segment, "{") {
			parts[i] = url.PathEscape(segment)
			continue
		}
		if len(patterns) != len(values) {
			return "", errors.New("upstream path does not match route")
		}
		for j, p := range patterns {
			if p == segment {
				parts[i] = values[j]
				break
			}
		}
	}
	return strings.Join(parts, "/"), nil
}
