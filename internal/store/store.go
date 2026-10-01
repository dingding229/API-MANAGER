package store

import (
	"context"

	"api-manager/internal/model"
)

type Store interface {
	CreateAPI(model.API) error
	UpdateAPI(model.API) error
	GetAPI(string) (model.API, error)
	ListAPIs() []model.API
	DeleteAPI(string) error

	CreateCredential(model.Credential) error
	UpdateCredential(model.Credential) error
	RotateCredential(string, string, string, string) (model.Credential, error)
	GetCredential(string) (model.Credential, error)
	ListCredentials() []model.Credential
	FindCredentialByHash(string) (model.Credential, bool)

	CreateUser(model.User) error
	GetUserByUsername(string) (model.User, error)
	GetUserByEmail(string) (model.User, error)
	ListUsers() []model.User
	CountUsers() int
	GetUserByID(string) (model.User, error)
	UpdateUserStatus(string, string) error
	UpdateUserProfile(string, model.UserProfileUpdate) (model.User, bool, error)

	ListPermissions() []model.Permission
	CreateRole(model.Role) error
	UpdateRolePermissions(string, []string) error
	ListRoles() []model.Role
	GetRoleByName(string) (model.Role, error)
	AssignUserRoles(string, []string) error
	ListUserRoles(string) []string
	GetUserPermissions(string) []string
	EnsureRBAC() error

	CreateRelease(model.API) (model.Release, error)
	ListReleases(string) []model.Release
	GetRelease(string, int) (model.Release, error)

	CreatePlugin(model.Plugin) error
	GetPlugin(string) (model.Plugin, error)
	ListPlugins() []model.Plugin
	SetPluginEnabled(string, bool) error
	DeletePlugin(string) error

	CreateAuditLog(model.AuditLog) error
	ListAuditLogs(model.AuditLogQuery) (model.AuditLogPage, error)
}

type HealthStore interface {
	Ping(context.Context) error
	Close()
}

// PluginDataStore is an optional, namespaced persistence boundary reserved for future WASM host capabilities.
type PluginDataStore interface {
	PutPluginData(model.PluginData) error
	GetPluginData(pluginName, namespace, key string) (model.PluginData, error)
}
