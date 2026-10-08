package plugin

import (
	"api-manager/internal/model"
	"api-manager/internal/schema"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Route contracts are authoritative. A missing declaration is never treated as
// an empty request, and schemas cannot pull external references/resources.
func ValidateRequestContracts(m Manifest) error {
	if len(m.Routes) == 0 {
		return errors.New("插件必须声明接口及请求参数；无参数接口也须显式声明")
	}
	for _, r := range m.Routes {
		if r.ParametersSchema == nil || r.RequestSchema == nil {
			return fmt.Errorf("接口 %s %s 未声明 parameters_schema 或 request_schema", r.Method, r.Path)
		}
		if unsafeContractValue(r.ParametersSchema, 0) || unsafeContractValue(r.RequestSchema, 0) || unsafeContractValue(r.ResponseSchema, 0) {
			return errors.New("参数声明不允许引用外部结构或超过层级限制")
		}
		if e := validateContractSchema(r.ParametersSchema, 0, false); e != nil {
			return fmt.Errorf("接口 %s 请求参数声明无效: %w", r.Path, e)
		}
		if e := validateContractSchema(r.RequestSchema, 0, false); e != nil {
			return fmt.Errorf("接口 %s 请求正文声明无效: %w", r.Path, e)
		}
		props, _ := r.ParametersSchema["properties"].(map[string]any)
		if len(props) != 3 {
			return errors.New("请求参数只能声明 path、query、header 三个位置")
		}
		if r.ParametersSchema["type"] != "object" || r.ParametersSchema["additionalProperties"] != false {
			return errors.New("参数根结构必须是封闭对象")
		}
		for _, location := range []string{"path", "query", "header"} {
			section, ok := props[location].(map[string]any)
			if !ok || section["type"] != "object" || section["properties"] == nil {
				return fmt.Errorf("接口 %s 必须显式声明 %s 参数对象", r.Path, location)
			}
		}
		for _, loc := range []string{"path", "query"} {
			section := props[loc].(map[string]any)
			if section["additionalProperties"] != false {
				return errors.New("路径与查询参数必须明确拒绝未声明字段")
			}
		}
		path, _ := props["path"].(map[string]any)
		fields, _ := path["properties"].(map[string]any)
		for _, parameter := range routeParameter.FindAllStringSubmatch(r.Path, -1) {
			if _, ok := fields[parameter[1]]; !ok {
				return fmt.Errorf("路径参数 %s 未声明", parameter[1])
			}
		}
		for k := range fields {
			if !strings.Contains(r.Path, "{"+k+"}") {
				return fmt.Errorf("声明的路径参数 %s 不存在", k)
			}
		}
		for _, doc := range []map[string]any{r.ParametersSchema, r.RequestSchema, r.ResponseSchema} {
			if doc == nil {
				continue
			}
			raw, e := json.Marshal(doc)
			if e != nil || len(raw) > 32768 {
				return errors.New("插件接口结构不能超过 32 KiB")
			}
			if e = schema.ValidateSchema(raw); e != nil {
				return e
			}
		}
	}
	return nil
}
func validateContractSchema(doc map[string]any, depth int, field bool) error {
	if depth > 16 {
		return errors.New("参数层级过深")
	}
	if _, ok := doc["$ref"]; ok {
		return errors.New("参数结构不能使用外部或间接引用")
	}
	if _, ok := doc["$id"]; ok {
		return errors.New("不允许定义外部结构来源")
	}
	if _, ok := doc["$dynamicRef"]; ok {
		return errors.New("参数结构不能使用引用")
	}
	if doc["type"] == nil {
		return errors.New("参数必须声明类型")
	}
	if field {
		description, _ := doc["description"].(string)
		if strings.TrimSpace(description) == "" || len(description) > 2048 {
			return errors.New("每个参数必须填写说明")
		}
	}
	if properties, ok := doc["properties"].(map[string]any); ok {
		for name, value := range properties {
			child, ok := value.(map[string]any)
			if !ok || name == "__proto__" || name == "constructor" || name == "prototype" {
				return errors.New("参数字段无效")
			}
			if e := validateContractSchema(child, depth+1, true); e != nil {
				return fmt.Errorf("%s: %w", name, e)
			}
		}
	}
	if doc["type"] == "array" {
		item, ok := doc["items"].(map[string]any)
		if !ok {
			return errors.New("数组必须声明元素结构")
		}
		if e := validateContractSchema(item, depth+1, false); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) Contract(name, path string, methods []string) (Route, error) {
	var selected *model.Plugin
	for _, item := range m.store.ListPlugins() {
		if item.Name != name {
			continue
		}
		if selected == nil || item.Enabled {
			copy := item
			selected = &copy
		}
		if item.Enabled {
			break
		}
	}
	if selected == nil {
		return Route{}, errors.New("请先安装声明了请求参数的插件")
	}
	var manifest Manifest
	raw, _ := json.Marshal(selected.Manifest)
	if json.Unmarshal(raw, &manifest) != nil {
		return Route{}, errors.New("插件声明不可读取")
	}
	if e := ValidateRequestContracts(manifest); e != nil {
		return Route{}, e
	}
	var found *Route
	for _, method := range methods {
		var match *Route
		for _, r := range manifest.Routes {
			if r.Path == path && r.Method == method {
				copy := r
				match = &copy
				break
			}
		}
		if match == nil {
			return Route{}, fmt.Errorf("插件未声明 %s %s", method, path)
		}
		if found == nil {
			found = match
		} else {
			a, _ := json.Marshal([]any{found.ParametersSchema, found.RequestSchema, found.ResponseSchema})
			b, _ := json.Marshal([]any{match.ParametersSchema, match.RequestSchema, match.ResponseSchema})
			if string(a) != string(b) {
				return Route{}, errors.New("同一接口的多种请求方式必须使用一致的参数结构")
			}
		}
	}
	if found == nil {
		return Route{}, errors.New("请选择插件声明的请求方式")
	}
	return *found, nil
}

func unsafeContractValue(value any, depth int) bool {
	if depth > 24 {
		return true
	}
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "$ref" || key == "$dynamicRef" || key == "$recursiveRef" || key == "$id" || unsafeContractValue(child, depth+1) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if unsafeContractValue(child, depth+1) {
				return true
			}
		}
	}
	return false
}
