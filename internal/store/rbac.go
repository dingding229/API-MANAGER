package store

import "api-manager/internal/model"

var defaultPermissions = []model.Permission{
	{Code: "account.profile", Description: "修改个人资料"},
	{Code: "account.security", Description: "管理个人账号安全"},
	{Code: "account.keys.read", Description: "查看个人调用凭据"},
	{Code: "account.keys.write", Description: "创建、重置和吊销个人调用凭据"},
	{Code: "account.keys.reveal", Description: "查看个人调用密钥"},
	{Code: "account.logs", Description: "查看个人调用日志"},
	{Code: "account.sessions", Description: "管理个人登录会话"},
	{Code: "account.billing.read", Description: "查看个人余额、套餐和用量"},
	{Code: "account.billing.purchase", Description: "购买与续订套餐"},
	{Code: "account.billing.redeem", Description: "兑换余额卡密和套餐卡密"},
	{Code: "billing.read", Description: "查看全站余额和套餐"},
	{Code: "billing.manage", Description: "管理套餐、绑定套餐和调整余额"},
	{Code: "card.manage", Description: "创建、查看和停用卡密"},

	{Code: "api.read", Description: "查看接口与 OpenAPI 文档"},
	{Code: "api.write", Description: "创建与修改接口"},
	{Code: "api.publish", Description: "发布、下线与回滚接口"},
	{Code: "api.delete", Description: "删除接口"},
	{Code: "credential.read", Description: "查看调用凭证列表"},
	{Code: "credential.reveal", Description: "查看完整调用密钥"},
	{Code: "credential.write", Description: "创建、重置和吊销调用凭证"},
	{Code: "plugin.read", Description: "查看插件"},
	{Code: "plugin.manage", Description: "管理插件配置"},
	{Code: "api.test", Description: "在公开文档中执行在线测试"},
	{Code: "user.read", Description: "查看用户、角色与权限"},
	{Code: "user.delete", Description: "删除用户（保留账务与审计）"},
	{Code: "user.manage", Description: "创建用户与分配角色"},
	{Code: "audit.read", Description: "查看审计日志"},
	{Code: "observability.read", Description: "查看内置指标、日志、链路与告警"},
	{Code: "observability.manage", Description: "确认和管理内置告警"},
	{Code: "observability.logs.clear", Description: "清理全部应用日志（不包含审计日志）"},
}

var defaultRoles = []model.Role{
	{Name: "super_admin", DisplayName: "管理员", Description: "管理所有用户、接口与网站设置", Permissions: []string{"*"}},
	{Name: "member", DisplayName: "普通用户", Description: "使用个人账户、凭据、日志与公开在线测试", Permissions: []string{"api.test", "account.profile", "account.security", "account.keys.read", "account.keys.write", "account.keys.reveal", "account.logs", "account.sessions", "account.billing.read", "account.billing.purchase", "account.billing.redeem"}},
	{Name: "api_developer", DisplayName: "接口开发者", Description: "开发、发布与管理接口", Permissions: []string{"account.profile", "account.security", "account.keys.read", "account.keys.write", "account.keys.reveal", "account.logs", "account.sessions", "account.billing.read", "account.billing.purchase", "account.billing.redeem", "api.test", "api.read", "api.write", "api.publish", "api.delete", "plugin.read", "plugin.manage", "observability.read"}},
}

func SupportedRole(name string) bool {
	return name == "super_admin" || name == "member" || name == "api_developer"
}

func DefaultPermissions() []model.Permission {
	result := make([]model.Permission, len(defaultPermissions))
	copy(result, defaultPermissions)
	return result
}

func DefaultRoles() []model.Role {
	result := make([]model.Role, len(defaultRoles))
	for i, role := range defaultRoles {
		if role.DisplayName == "" {
			role.DisplayName = map[string]string{"super_admin": "超级管理员", "tenant_admin": "平台管理员", "operator": "运维人员", "api_developer": "接口开发者", "viewer": "只读用户"}[role.Name]
		}
		role.Permissions = append([]string(nil), role.Permissions...)
		result[i] = role
	}
	return result
}
