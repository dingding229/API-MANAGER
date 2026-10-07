// Package catalog exports a narrow read-only projection, never administration models.
package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"api-manager/internal/model"
)

type Store interface{ ListAPIs() []model.API }
type Catalog struct {
	store        Store
	baseURL      string
	siteProvider func() model.PublicSiteInfo
}
type Operation struct {
	PriceMicros    int64       `json:"price_micros"`
	TestEnabled    bool        `json:"test_enabled"`
	Method         string      `json:"method"`
	Authentication string      `json:"authentication"`
	Parameters     []Parameter `json:"parameters"`
	Body           []Parameter `json:"body"`
}
type Document struct {
	PriceMicros    int64       `json:"price_micros"`
	Methods        []string    `json:"methods"`
	Operations     []Operation `json:"operations"`
	ID             string      `json:"id"`
	Title          string      `json:"title"`
	Summary        string      `json:"summary"`
	Category       string      `json:"category"`
	Method         string      `json:"method"`
	Path           string      `json:"path"`
	Authentication string      `json:"authentication"`
	Parameters     []Parameter `json:"parameters"`
	Body           []Parameter `json:"body"`
}
type Parameter struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
type Response struct {
	Version int                   `json:"version"`
	BaseURL string                `json:"base_url"`
	APIs    []Document            `json:"apis"`
	Site    *model.PublicSiteInfo `json:"site,omitempty"`
}

func New(store Store, baseURL string) *Catalog {
	baseURL = strings.TrimRight(baseURL, "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		baseURL = ""
	}
	return &Catalog{store: store, baseURL: baseURL}
}
func (c *Catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "read-only catalog"})
		return
	}
	var apis []model.API
	if checked, ok := c.store.(interface{ ListPublicAPIsChecked() ([]model.API, error) }); ok {
		var err error
		apis, err = checked.ListPublicAPIsChecked()
		if err != nil {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "catalog unavailable"})
			return
		}
	} else if checked, ok := c.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		var err error
		apis, err = checked.ListAPIsChecked()
		if err != nil {
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "catalog unavailable"})
			return
		}
	} else {
		apis = c.store.ListAPIs()
	}
	result := Response{Version: 1, BaseURL: c.baseURL, APIs: make([]Document, 0)}
	if c.siteProvider != nil {
		info := c.siteProvider()
		result.Site = &info
		result.BaseURL = info.WebsiteURL
		if info.APIDomain != "" {
			result.BaseURL = info.APIDomain
		}
	}
	visible := 0
	grouped := map[string]int{}
	for _, a := range apis {
		if !a.PublicVisible || !a.Enabled || a.PublishedAt == nil || strings.TrimSpace(a.PublicTitle) == "" || (a.AuthMode != "api_key" && a.AuthMode != "none") || !strings.HasPrefix(a.Path, "/api/") || strings.ContainsAny(a.Path, "?\r\n#") {
			continue
		}
		if visible >= 200 {
			break
		}
		visible++
		for _, method := range a.HTTPMethods() {
			digest := sha256.Sum256([]byte(a.Path))
			category := a.PublicCategory
			if category == "" {
				category = "通用接口"
			}
			d := Document{PriceMicros: a.PriceMicros, ID: hex.EncodeToString(digest[:8]), Title: a.PublicTitle, Summary: a.PublicSummary, Category: category, Method: method, Path: a.Path, Authentication: a.AuthMode, Parameters: make([]Parameter, 0), Body: make([]Parameter, 0)}
			// Export only property names/types/required flags. No example/default values,
			// schema descriptions, response body, upstream or credential fields cross this boundary.
			d.Body = fields(a.RequestSchema, "body")
			var params struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if json.Unmarshal(a.ParametersSchema, &params) == nil {
				for _, location := range []string{"path", "query", "header"} {
					d.Parameters = append(d.Parameters, fields(params.Properties[location], location)...)
				}
			}
			// Path placeholders are documented even if the administrator supplied no schema.
			for _, part := range strings.Split(a.Path, "/") {
				if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
					name := strings.Trim(part, "{}")
					found := false
					for index, p := range d.Parameters {
						if p.Name == name && p.Location == "path" {
							found = true
							d.Parameters[index].Required = true
						}
					}
					if !found {
						d.Parameters = append(d.Parameters, Parameter{Name: name, Location: "path", Type: "string", Required: true})
					}
				}
			}
			operation := Operation{PriceMicros: a.PriceMicros, TestEnabled: a.PublicVisible, Method: method, Authentication: d.Authentication, Parameters: d.Parameters, Body: d.Body}
			if index, exists := grouped[a.Path]; exists {
				duplicate := false
				for _, existing := range result.APIs[index].Methods {
					if existing == method {
						duplicate = true
					}
				}
				if !duplicate {
					result.APIs[index].Methods = append(result.APIs[index].Methods, method)
					result.APIs[index].Operations = append(result.APIs[index].Operations, operation)
				}
			} else {
				d.Methods = []string{method}
				d.Operations = []Operation{operation}
				grouped[a.Path] = len(result.APIs)
				result.APIs = append(result.APIs, d)
			}
		}
	}
	sort.Slice(result.APIs, func(i, j int) bool {
		if result.APIs[i].Category != result.APIs[j].Category {
			return result.APIs[i].Category < result.APIs[j].Category
		}
		if result.APIs[i].Title != result.APIs[j].Title {
			return result.APIs[i].Title < result.APIs[j].Title
		}
		if result.APIs[i].Path != result.APIs[j].Path {
			return result.APIs[i].Path < result.APIs[j].Path
		}
		return result.APIs[i].Method < result.APIs[j].Method
	})
	_ = json.NewEncoder(w).Encode(result)
}
func fields(raw json.RawMessage, location string) []Parameter {
	var schema struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	result := make([]Parameter, 0)
	if json.Unmarshal(raw, &schema) != nil {
		return result
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(result) >= 40 || len(name) > 80 || strings.ContainsAny(name, "\r\n") {
			continue
		}
		t := schema.Properties[name].Type
		switch t {
		case "string", "number", "integer", "boolean", "object", "array":
		default:
			t = "string"
		}
		required := false
		for _, r := range schema.Required {
			if r == name {
				required = true
			}
		}
		result = append(result, Parameter{Name: name, Location: location, Type: t, Required: required})
	}
	return result
}

func (c *Catalog) SetSiteProvider(provider func() model.PublicSiteInfo) { c.siteProvider = provider }
