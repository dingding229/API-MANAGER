package api

import (
	"api-manager/internal/model"
	"encoding/json"
	"errors"
)

func (a *Admin) applyPluginContract(request *model.CreateAPIRequest) error {
	if a.pluginManager == nil {
		return errors.New("插件管理不可用")
	}
	route, e := a.pluginManager.Contract(request.Plugin, request.Path, normalizedMethods(*request))
	if e != nil {
		return e
	}
	request.ParametersSchema, _ = json.Marshal(route.ParametersSchema)
	request.RequestSchema, _ = json.Marshal(route.RequestSchema)
	request.ResponseSchema, _ = json.Marshal(route.ResponseSchema)
	request.Description = route.Description
	if request.PublicVisible {
		request.PublicSummary = route.Description
		if route.Name != "" {
			request.PublicTitle = route.Name
		}
	}
	return nil
}
