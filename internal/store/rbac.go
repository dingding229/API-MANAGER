package store

import "api-manager/internal/model"

var defaultPermissions = []model.Permission{
	{Code: "api.read", Description: "查看接口与 OpenAPI 文档"},
	{Code: "api.write", Description: "创建与修改接口"},
	{Code: "api.publish", Description: "发布、下线与回滚接口"},
	{Code: "api.delete", Description: "删除接口"},
	{Code: "credential.read", Description: "查看调用凭证列表"},
	{Code: "credential.reveal", Description: "查看完整调用密钥"},
	{Code: "credential.write", Description: "创建与吊销调用凭证"},
	{Code: "plugin.read", Description: "查看插件"},
	{Code: "plugin.manage", Description: "管理插件配置"},
	{Code: "user.read", Description: "查看用户、角色与权限"},
	{Code: "user.manage", Description: "创建用户与分配角色"},
	{Code: "audit.read", Description: "查看审计日志"},
	{Code: "observability.read", Description: "查看内置指标、日志、链路与告警"},
	{Code: "observability.manage", Description: "确认和管理内置告警"},
	{Code: "observability.logs.clear", Description: "清理全部应用日志（不包含审计日志）"},
}

var defaultRoles = []model.Role{
	{Name: "super_admin", Description: "完整管理权限", Permissions: []string{"*"}},
	{Name: "tenant_admin", Description: "平台管理权限（当前仅支持单租户）", Permissions: []string{"api.read", "api.write", "api.publish", "api.delete", "credential.read", "credential.reveal", "credential.write", "plugin.read", "plugin.manage", "user.read", "user.manage", "audit.read", "observability.read", "observability.manage"}},
	{Name: "operator", Description: "接口运维权限", Permissions: []string{"api.read", "api.write", "api.publish", "credential.read", "credential.write", "plugin.read", "audit.read", "observability.read", "observability.manage"}},
	{Name: "api_developer", Description: "接口开发权限", Permissions: []string{"api.read", "api.write", "credential.read", "plugin.read", "observability.read"}},
	{Name: "viewer", Description: "只读权限", Permissions: []string{"api.read", "credential.read", "plugin.read", "observability.read"}},
}

func DefaultPermissions() []model.Permission {
	result := make([]model.Permission, len(defaultPermissions))
	copy(result, defaultPermissions)
	return result
}

func DefaultRoles() []model.Role {
	result := make([]model.Role, len(defaultRoles))
	for i, role := range defaultRoles {
		role.Permissions = append([]string(nil), role.Permissions...)
		result[i] = role
	}
	return result
}
