package model

import (
	"errors"
	"strings"
)

// NormalizeMethods preserves the legacy primary method while validating the complete set.
func NormalizeMethods(method string, methods []string) ([]string, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if len(methods) == 0 {
		methods = []string{method}
	}
	if len(methods) > 7 {
		return nil, errors.New("select between one and seven HTTP methods")
	}
	result := make([]string, 0, len(methods))
	seen := map[string]bool{}
	for _, value := range methods {
		value = strings.ToUpper(strings.TrimSpace(value))
		switch value {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return nil, errors.New("invalid HTTP method")
		}
		if !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	if method != "" && !seen[method] {
		return nil, errors.New("primary method must belong to methods")
	}
	return result, nil
}
func (a API) HTTPMethods() []string {
	if len(a.Methods) != 0 {
		return a.Methods
	}
	return []string{strings.ToUpper(a.Method)}
}
func (a API) AllowsMethod(method string) bool {
	for _, value := range a.HTTPMethods() {
		if strings.EqualFold(value, method) {
			return true
		}
	}
	return false
}

// RoutesConflict permits deterministic exact-vs-parameter precedence, but not
// overlapping parameter routes or two owners of the same concrete route.
func RoutesConflict(a, b API) bool {
	overlap := false
	for _, method := range a.HTTPMethods() {
		if b.AllowsMethod(method) {
			overlap = true
			break
		}
	}
	if !overlap {
		return false
	}
	if a.Path == b.Path {
		return true
	}
	if !strings.Contains(a.Path, "{") || !strings.Contains(b.Path, "{") {
		return false
	}
	left, right := strings.Split(a.Path, "/"), strings.Split(b.Path, "/")
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		lp := strings.HasPrefix(left[i], "{") && strings.HasSuffix(left[i], "}")
		rp := strings.HasPrefix(right[i], "{") && strings.HasSuffix(right[i], "}")
		if !lp && !rp && left[i] != right[i] {
			return false
		}
	}
	return true
}
