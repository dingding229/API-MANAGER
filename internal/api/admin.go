package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-manager/internal/audit"
	"api-manager/internal/auth"
	"api-manager/internal/model"
	"api-manager/internal/observability"
	"api-manager/internal/plugin"
	apiSchema "api-manager/internal/schema"
	"api-manager/internal/store"
	"api-manager/internal/upstream"
	"gopkg.in/yaml.v3"
)

type UserManager interface {
	ValidateSession(string) (model.User, error)
	Create(string, string, string) (model.User, error)
	CreateWithRoles(string, string, []string) (model.User, error)
	Can(string, string) bool
	CreateRole(string, string, []string) (model.Role, error)
	AssignRoles(string, []string) error
	SetStatus(string, string) error
}

type Admin struct {
	store                   store.Store
	plugins                 *plugin.Registry
	userManager             UserManager
	logger                  *slog.Logger
	auditor                 *audit.Service
	pluginManager           *plugin.Manager
	pluginLibrary           *plugin.Library
	credentialEncryptionKey string
	observabilityHub        *observability.Hub
	metrics                 *observability.Metrics
	productionMode          bool
}

func NewAdmin(s store.Store, plugins *plugin.Registry, logger *slog.Logger) *Admin {
	return &Admin{store: s, plugins: plugins, logger: logger, auditor: audit.New(s, logger)}
}

func NewAdminWithUserManagement(s store.Store, plugins *plugin.Registry, userManager UserManager, logger *slog.Logger) *Admin {
	return &Admin{store: s, plugins: plugins, userManager: userManager, logger: logger, auditor: audit.New(s, logger)}
}

func NewAdminWithUserManagementAndPluginManager(s store.Store, plugins *plugin.Registry, userManager UserManager, pluginManager *plugin.Manager, logger *slog.Logger) *Admin {
	return &Admin{store: s, plugins: plugins, userManager: userManager, pluginManager: pluginManager, logger: logger, auditor: audit.New(s, logger)}
}
func (a *Admin) SetProductionMode(enabled bool) { a.productionMode = enabled }

func (a *Admin) SetCredentialEncryptionKey(secret string) {
	if strings.TrimSpace(secret) != "" {
		a.credentialEncryptionKey = secret
	}
}
func (a *Admin) SetPluginLibrary(library *plugin.Library) { a.pluginLibrary = library }
func (a *Admin) SetObservability(hub *observability.Hub, metrics *observability.Metrics) {
	a.observabilityHub, a.metrics = hub, metrics
}

func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, authorized := a.requestActor(r)
	if !authorized {
		a.auditor.Record(r.Context(), audit.Actor{Type: "anonymous"}, r, "auth.admin.denied", "admin", "", http.StatusUnauthorized, nil)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "admin authentication required"})
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), auditActorContextKey{}, actor))
	if permission := requiredPermission(r); permission != "" && !a.hasPermission(r, permission) {
		a.recordAudit(r, "auth.permission.denied", "admin", "", http.StatusForbidden, map[string]any{"required": permission})
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission", "required": permission})
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/observability/summary":
		a.observabilitySummary(w)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/observability/logs":
		a.observabilityLogs(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/observability/traces":
		a.observabilityTraces(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/v1/observability/alerts/") && strings.HasSuffix(r.URL.Path, "/ack"):
		a.acknowledgeObservabilityAlert(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/audit-logs":
		a.listAuditLogs(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/apis":
		a.listAPIs(w)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/apis":
		a.createAPI(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/openapi.json":
		a.exportOpenAPI(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/openapi/import":
		a.importOpenAPI(w, r)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases"):
		a.releases(w, r)
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/rollback/"):
		a.rollback(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/v1/apis/") && strings.HasSuffix(r.URL.Path, "/publish"):
		a.setPublished(w, r, true)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/v1/apis/") && strings.HasSuffix(r.URL.Path, "/unpublish"):
		a.setPublished(w, r, false)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/v1/apis/"):
		a.updateAPI(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/v1/apis/"):
		a.getAPI(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/v1/apis/"):
		a.deleteAPI(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/credentials":
		a.listCredentials(w)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/admin/v1/credentials/") && strings.HasSuffix(r.URL.Path, "/key"):
		a.viewCredentialKey(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/credentials":
		a.createCredential(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/v1/credentials/") && strings.HasSuffix(r.URL.Path, "/rotate"):
		a.rotateCredential(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin/v1/credentials/") && strings.HasSuffix(r.URL.Path, "/revoke"):
		a.revokeCredential(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/plugins":
		a.uploadPlugin(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/plugins":
		a.listPlugins(w)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/plugin-library":
		a.listPluginLibrary(w)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/plugin-library":
		a.publishPluginLibrary(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/plugin-library/install":
		a.installPluginLibrary(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/v1/plugins/") && strings.HasSuffix(r.URL.Path, "/status"):
		a.updatePluginStatus(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/admin/v1/plugins/"):
		a.deletePlugin(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/users":
		a.listUsers(w)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/users":
		a.createUser(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/v1/users/") && strings.HasSuffix(r.URL.Path, "/roles"):
		a.assignUserRoles(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/v1/users/") && strings.HasSuffix(r.URL.Path, "/status"):
		a.updateUserStatus(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/roles":
		a.listRoles(w)
	case r.Method == http.MethodPost && r.URL.Path == "/admin/v1/roles":
		a.createRole(w, r)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/admin/v1/roles/") && strings.HasSuffix(r.URL.Path, "/permissions"):
		a.updateRolePermissions(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/admin/v1/permissions":
		a.listPermissions(w)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "admin route not found"})
	}
}

type auditActorContextKey struct{}

func (a *Admin) requestActor(r *http.Request) (audit.Actor, bool) {
	if a.userManager == nil {
		return audit.Actor{}, false
	}
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") || len(r.Header.Values("Authorization")) != 1 || r.Header.Get("X-API-Key") != "" {
		return audit.Actor{}, false
	}
	user, err := a.userManager.ValidateSession(auth.ExtractAPIKey(authorization))
	if err != nil {
		return audit.Actor{}, false
	}
	return audit.Actor{ID: user.ID, Type: "user", Email: user.Email}, true
}
func (a *Admin) hasPermission(r *http.Request, permission string) bool {
	actor, ok := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	return ok && actor.Type == "user" && a.userManager != nil && a.userManager.Can(actor.ID, permission)
}

func requiredPermission(r *http.Request) string {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/admin/v1/observability/alerts/") && strings.HasSuffix(path, "/ack") && r.Method == http.MethodPost:
		return "observability.manage"
	case strings.HasPrefix(path, "/admin/v1/observability/") && r.Method == http.MethodGet:
		return "observability.read"
	case path == "/admin/v1/audit-logs" && r.Method == http.MethodGet:
		return "audit.read"
	case path == "/admin/v1/apis" && r.Method == http.MethodGet, strings.HasPrefix(path, "/admin/v1/apis/") && r.Method == http.MethodGet, path == "/admin/v1/openapi.json":
		return "api.read"
	case path == "/admin/v1/apis" && r.Method == http.MethodPost, strings.HasPrefix(path, "/admin/v1/apis/") && r.Method == http.MethodPut, path == "/admin/v1/openapi/import" && r.Method == http.MethodPost:
		return "api.write"
	case strings.Contains(path, "/publish"), strings.Contains(path, "/unpublish"), strings.Contains(path, "/rollback/"):
		return "api.publish"
	case strings.Contains(path, "/releases"):
		return "api.read"
	case strings.HasPrefix(path, "/admin/v1/apis/") && r.Method == http.MethodDelete:
		return "api.delete"
	case strings.HasPrefix(path, "/admin/v1/credentials/") && strings.HasSuffix(path, "/key") && r.Method == http.MethodGet:
		return "credential.reveal"
	case path == "/admin/v1/credentials" && r.Method == http.MethodGet:
		return "credential.read"
	case path == "/admin/v1/credentials" && r.Method == http.MethodPost, strings.HasPrefix(path, "/admin/v1/credentials/") && (strings.HasSuffix(path, "/revoke") || strings.HasSuffix(path, "/rotate")) && r.Method == http.MethodPost:
		return "credential.write"
	case path == "/admin/v1/plugins" && r.Method == http.MethodGet, path == "/admin/v1/plugin-library" && r.Method == http.MethodGet:
		return "plugin.read"
	case path == "/admin/v1/plugins" && r.Method == http.MethodPost, strings.HasPrefix(path, "/admin/v1/plugins/"), path == "/admin/v1/plugin-library/install" && r.Method == http.MethodPost, path == "/admin/v1/plugin-library" && r.Method == http.MethodPost:
		return "plugin.manage"
	case path == "/admin/v1/users" && r.Method == http.MethodGet:
		return "user.read"
	case path == "/admin/v1/users" && r.Method == http.MethodPost, strings.Contains(path, "/roles"), strings.Contains(path, "/status"):
		return "user.manage"
	case path == "/admin/v1/roles" && r.Method == http.MethodGet, path == "/admin/v1/permissions":
		return "user.read"
	case path == "/admin/v1/roles" && r.Method == http.MethodPost, strings.HasSuffix(path, "/permissions"):
		return "user.manage"
	default:
		return ""
	}
}

func (a *Admin) listAPIs(w http.ResponseWriter) {
	items, err := a.listAPIsChecked()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "list APIs unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (a *Admin) listAPIsChecked() ([]model.API, error) {
	if checked, ok := a.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		return checked.ListAPIsChecked()
	}
	return a.store.ListAPIs(), nil
}

func (a *Admin) getAPI(w http.ResponseWriter, r *http.Request) {
	api, err := a.store.GetAPI(strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read api failed"})
		return
	}
	writeJSON(w, http.StatusOK, api)
}

func (a *Admin) createAPI(w http.ResponseWriter, r *http.Request) {
	var request model.CreateAPIRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Method = normalizeAPIMethod(request.Method)
	if err := validateAPIRequest(request, a.productionMode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := a.checkRouteConflict("", request.Method, request.Path); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	api := apiFromRequest(newID("api"), request, now, now)
	if err := a.store.CreateAPI(api); err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("api created", "api_id", api.ID, "name", api.Name)
	a.recordAudit(r, "api.create", "api", api.ID, http.StatusCreated, map[string]any{"name": api.Name, "method": api.Method, "path": api.Path, "auth_mode": api.AuthMode})
	writeJSON(w, http.StatusCreated, api)
}

func (a *Admin) updateAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/")
	current, err := a.store.GetAPI(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read api failed"})
		return
	}
	var request model.UpdateAPIRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Method = normalizeAPIMethod(request.Method)
	if err := validateAPIRequest(request, a.productionMode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// An existing release is live: editing it requires the publish capability.
	if current.PublishedAt != nil && !a.hasPermission(r, "api.publish") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "editing a published api requires api.publish"})
		return
	}
	if err := a.checkRouteConflict(current.ID, request.Method, request.Path); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	updated := apiFromRequest(current.ID, request, current.CreatedAt, time.Now().UTC())
	updated.Enabled, updated.PublishedAt = current.Enabled, current.PublishedAt
	if err := a.store.UpdateAPI(updated); err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("api updated", "api_id", updated.ID)
	a.recordAudit(r, "api.update", "api", updated.ID, http.StatusOK, map[string]any{"name": updated.Name, "method": updated.Method, "path": updated.Path, "auth_mode": updated.AuthMode})
	writeJSON(w, http.StatusOK, updated)
}

func (a *Admin) setPublished(w http.ResponseWriter, r *http.Request, published bool) {
	suffix := "/unpublish"
	if published {
		suffix = "/publish"
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/"), suffix)
	api, err := a.store.GetAPI(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "api not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read api failed"})
		return
	}
	if published {
		if err := a.checkRouteConflict(api.ID, api.Method, api.Path); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
	}
	now := time.Now().UTC()
	api.Enabled, api.UpdatedAt = published, now
	if published {
		api.PublishedAt = &now
	} else {
		api.PublishedAt = nil
	}
	if err := a.store.UpdateAPI(api); err != nil {
		writeStoreError(w, err)
		return
	}
	var release model.Release
	if published {
		release, err = a.store.CreateRelease(api)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create release failed"})
			return
		}
	}
	a.logger.Info("api publication changed", "api_id", api.ID, "published", published, "release_version", release.Version)
	action := "api.unpublish"
	if published {
		action = "api.publish"
	}
	a.recordAudit(r, action, "api", api.ID, http.StatusOK, map[string]any{"name": api.Name, "method": api.Method, "path": api.Path, "release_version": release.Version})
	writeJSON(w, http.StatusOK, map[string]any{"api": api, "release": release})
}

func (a *Admin) releases(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[1] != "releases" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "release route not found"})
		return
	}
	if len(parts) == 2 {
		writeJSON(w, http.StatusOK, a.store.ListReleases(parts[0]))
		return
	}
	version := 0
	if _, err := fmt.Sscanf(parts[2], "%d", &version); err != nil || version <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid release version"})
		return
	}
	release, err := a.store.GetRelease(parts[0], version)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "release not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read release failed"})
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (a *Admin) rollback(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[1] != "rollback" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "rollback route not found"})
		return
	}
	version := 0
	if _, err := fmt.Sscanf(parts[2], "%d", &version); err != nil || version <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid release version"})
		return
	}
	release, err := a.store.GetRelease(parts[0], version)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "release not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read release failed"})
		return
	}
	api := release.Snapshot
	if err := validateStoredAPI(api, a.productionMode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "release is incompatible with current KEY/security requirements"})
		return
	}
	api.ID = parts[0]
	if err := a.checkRouteConflict(api.ID, api.Method, api.Path); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC()
	api.Enabled, api.PublishedAt, api.UpdatedAt = true, &now, now
	if err := a.store.UpdateAPI(api); err != nil {
		writeStoreError(w, err)
		return
	}
	newRelease, err := a.store.CreateRelease(api)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create rollback release failed"})
		return
	}
	a.recordAudit(r, "api.rollback", "api", api.ID, http.StatusOK, map[string]any{"name": api.Name, "method": api.Method, "path": api.Path, "rolled_back_from": version, "release_version": newRelease.Version})
	writeJSON(w, http.StatusOK, map[string]any{"api": api, "release": newRelease, "rolled_back_from": version})
}

func (a *Admin) deleteAPI(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/admin/v1/apis/")
	api, _ := a.store.GetAPI(id)
	if err := a.store.DeleteAPI(id); err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("api deleted", "api_id", id)
	a.recordAudit(r, "api.delete", "api", id, http.StatusNoContent, map[string]any{"name": api.Name, "method": api.Method, "path": api.Path})
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) listCredentials(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, a.store.ListCredentials())
}

func (a *Admin) createCredential(w http.ResponseWriter, r *http.Request) {
	var request model.CreateCredentialRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	key := "ak_" + randomSecret(24)
	encryptedKey, err := auth.EncryptSecret(a.credentialEncryptionKey, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "credential encryption is not configured"})
		return
	}
	credential := model.Credential{ID: newID("cred"), Name: request.Name, Prefix: key[:10], Hash: auth.HashAPIKey(key), EncryptedKey: encryptedKey, KeyAvailable: true, CreatedAt: time.Now().UTC(), ExpiresAt: request.ExpiresAt}
	if err := a.store.CreateCredential(credential); err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("credential created", "credential_id", credential.ID, "name", credential.Name)
	a.recordAudit(r, "credential.create", "credential", credential.ID, http.StatusCreated, map[string]any{"name": credential.Name, "prefix": credential.Prefix, "expires_at": credential.ExpiresAt})
	writeJSON(w, http.StatusCreated, map[string]any{"credential": credential, "api_key": key})
}

func (a *Admin) rotateCredential(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/credentials/"), "/rotate")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}
	key := "ak_" + randomSecret(24)
	encryptedKey, err := auth.EncryptSecret(a.credentialEncryptionKey, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "credential encryption is not configured"})
		return
	}
	credential, err := a.store.RotateCredential(id, key[:10], auth.HashAPIKey(key), encryptedKey)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("credential rotated", "credential_id", credential.ID)
	a.recordAudit(r, "credential.rotate", "credential", credential.ID, http.StatusOK, map[string]any{"name": credential.Name, "prefix": credential.Prefix})
	writeJSON(w, http.StatusOK, map[string]any{"credential": credential, "api_key": key})
}

func (a *Admin) viewCredentialKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/credentials/"), "/key")
	if id == "" || strings.Contains(id, "/") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}
	credential, err := a.store.GetCredential(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read credential failed"})
		return
	}
	if credential.Revoked {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "revoked credentials cannot be viewed"})
		return
	}
	key, err := auth.DecryptSecret(a.credentialEncryptionKey, credential.EncryptedKey)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "this credential was created before secure key recovery; rotate it to create a viewable key"})
		return
	}
	a.recordAudit(r, "credential.view", "credential", credential.ID, http.StatusOK, map[string]any{"name": credential.Name, "prefix": credential.Prefix})
	writeJSON(w, http.StatusOK, map[string]any{"credential": credential, "api_key": key})
}

func (a *Admin) revokeCredential(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/credentials/"), "/revoke")
	credential, err := a.store.GetCredential(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "credential not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read credential failed"})
		return
	}
	credential.Revoked = true
	if err := a.store.UpdateCredential(credential); err != nil {
		writeStoreError(w, err)
		return
	}
	a.logger.Info("credential revoked", "credential_id", credential.ID)
	a.recordAudit(r, "credential.revoke", "credential", credential.ID, http.StatusOK, map[string]any{"name": credential.Name, "prefix": credential.Prefix})
	writeJSON(w, http.StatusOK, credential)
}

func (a *Admin) listPlugins(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"plugins": a.plugins.List(), "managed": a.store.ListPlugins()})
}
func (a *Admin) listPluginLibrary(w http.ResponseWriter) {
	if a.pluginLibrary == nil {
		writeJSON(w, http.StatusOK, []plugin.LibraryEntry{})
		return
	}
	entries, err := a.pluginLibrary.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list plugin library failed"})
		return
	}
	writeJSON(w, http.StatusOK, entries)
}
func (a *Admin) publishPluginLibrary(w http.ResponseWriter, r *http.Request) {
	if a.pluginLibrary == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin library is disabled"})
		return
	}
	var input struct {
		PluginID string `json:"plugin_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	entry, err := a.pluginLibrary.Publish(input.PluginID)
	if err != nil {
		writePluginError(w, err)
		return
	}
	a.recordAudit(r, "plugin.library_publish", "plugin", input.PluginID, http.StatusCreated, map[string]any{"name": entry.Name, "version": entry.Version})
	writeJSON(w, http.StatusCreated, entry)
}

func (a *Admin) installPluginLibrary(w http.ResponseWriter, r *http.Request) {
	if a.pluginLibrary == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin library is disabled"})
		return
	}
	name, version := r.URL.Query().Get("name"), r.URL.Query().Get("version")
	item, err := a.pluginLibrary.Install(r.Context(), name, version)
	if err != nil {
		writePluginError(w, err)
		return
	}
	a.recordAudit(r, "plugin.library_install", "plugin", item.ID, http.StatusCreated, map[string]any{"name": item.Name, "version": item.Version})
	writeJSON(w, http.StatusCreated, item)
}

func (a *Admin) uploadPlugin(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin management is disabled"})
		return
	}
	// ParseMultipartForm's argument only limits memory buffering, not total body size.
	// Bound the whole multipart request (file payload plus headers/form overhead).
	r.Body = http.MaxBytesReader(w, r.Body, a.pluginManager.MaxUploadBytes()+(64<<10))
	// Keep multipart metadata in memory, but spool large plugin files to /tmp.
	// #nosec G120 -- the whole body is capped above and file parts spill to bounded /tmp storage.
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid multipart plugin upload"})
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	manifestFile, _, err := r.FormFile("manifest")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifest file is required"})
		return
	}
	defer manifestFile.Close()
	wasmFile, _, err := r.FormFile("wasm")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wasm file is required"})
		return
	}
	defer wasmFile.Close()
	manifestBytes, err := io.ReadAll(io.LimitReader(manifestFile, a.pluginManager.MaxManifestBytes()+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read manifest failed"})
		return
	}
	wasmBytes, err := io.ReadAll(io.LimitReader(wasmFile, a.pluginManager.MaxUploadBytes()+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read wasm failed"})
		return
	}
	item, err := a.pluginManager.Upload(r.Context(), manifestBytes, wasmBytes)
	if err != nil {
		writePluginError(w, err)
		return
	}
	a.recordAudit(r, "plugin.upload", "plugin", item.ID, http.StatusCreated, map[string]any{"name": item.Name, "version": item.Version, "checksum": item.Checksum})
	writeJSON(w, http.StatusCreated, item)
}

func (a *Admin) updatePluginStatus(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin management is disabled"})
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/"), "/status")
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	var item model.Plugin
	var err error
	if request.Enabled {
		item, err = a.pluginManager.Enable(r.Context(), id)
	} else {
		item, err = a.pluginManager.Disable(id)
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	action := "plugin.disable"
	if request.Enabled {
		action = "plugin.enable"
	}
	a.recordAudit(r, action, "plugin", item.ID, http.StatusOK, map[string]any{"name": item.Name, "version": item.Version, "enabled": item.Enabled})
	writeJSON(w, http.StatusOK, item)
}

func (a *Admin) deletePlugin(w http.ResponseWriter, r *http.Request) {
	if a.pluginManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "plugin management is disabled"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/v1/plugins/")
	item, err := a.pluginManager.Delete(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, "plugin.delete", "plugin", item.ID, http.StatusNoContent, map[string]any{"name": item.Name, "version": item.Version})
	w.WriteHeader(http.StatusNoContent)
}

func (a *Admin) mayGrantRoles(r *http.Request, roles []string) bool {
	if a.hasPermission(r, "*") {
		return true
	}
	for _, role := range roles {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "super_admin" || role == "tenant_admin" {
			return false
		}
		assigned, err := a.store.GetRoleByName(role)
		if err != nil {
			return false
		}
		for _, permission := range assigned.Permissions {
			if permission == "*" || permission == "user.manage" {
				return false
			}
		}
	}
	return true
}

func (a *Admin) mayManageRoles(r *http.Request, id string, proposed []string) bool {
	if a.hasPermission(r, "*") {
		return true
	}
	actor, _ := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if actor.ID == id {
		return false
	}
	target, err := a.store.GetUserByID(id)
	if err != nil {
		return false
	}
	for _, role := range target.Roles {
		if !a.mayGrantRoles(r, []string{role}) {
			return false
		}
	}
	return a.mayGrantRoles(r, proposed)
}

func containsRole(roles []string, wanted string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), wanted) {
			return true
		}
	}
	return false
}

func (a *Admin) lastSuperAdmin(id string) bool {
	target, err := a.store.GetUserByID(id)
	if err != nil {
		return false
	}
	admin := false
	for _, role := range target.Roles {
		if role == "super_admin" {
			admin = true
		}
	}
	if !admin || target.Status != "active" {
		return false
	}
	for _, user := range a.store.ListUsers() {
		if user.ID == id || user.Status != "active" {
			continue
		}
		for _, role := range user.Roles {
			if role == "super_admin" {
				return false
			}
		}
	}
	return true
}

func (a *Admin) listUsers(w http.ResponseWriter) { writeJSON(w, http.StatusOK, a.store.ListUsers()) }

func (a *Admin) createUser(w http.ResponseWriter, r *http.Request) {
	if a.userManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "user authentication is disabled"})
		return
	}
	var request model.CreateUserRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	roles := request.Roles
	if len(roles) == 0 {
		roles = []string{request.Role}
	}
	if len(roles) == 1 && strings.TrimSpace(roles[0]) == "" {
		roles = []string{"viewer"}
	}
	if !a.mayGrantRoles(r, roles) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only super_admin can grant privileged roles"})
		return
	}
	user, err := a.userManager.CreateWithRoles(request.Email, request.Password, roles)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "user already exists"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	a.recordAudit(r, "user.create", "user", user.ID, http.StatusCreated, map[string]any{"email": user.Email, "roles": user.Roles, "status": user.Status})
	writeJSON(w, http.StatusCreated, user)
}

func (a *Admin) assignUserRoles(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/users/"), "/roles")
	var request model.UpdateUserRolesRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if a.lastSuperAdmin(id) && !containsRole(request.Roles, "super_admin") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot remove the last super_admin"})
		return
	}
	if !a.mayManageRoles(r, id, request.Roles) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only super_admin can manage privileged roles or their own roles"})
		return
	}
	if err := a.userManager.AssignRoles(id, request.Roles); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	user, err := a.store.GetUserByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	a.recordAudit(r, "user.roles.update", "user", user.ID, http.StatusOK, map[string]any{"email": user.Email, "roles": user.Roles})
	writeJSON(w, http.StatusOK, user)
}

func (a *Admin) updateUserStatus(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/users/"), "/status")
	var request model.UpdateUserStatusRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if !a.mayManageRoles(r, id, nil) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only super_admin can modify privileged users"})
		return
	}
	if request.Status == "disabled" && a.lastSuperAdmin(id) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot disable the last super_admin"})
		return
	}
	if err := a.userManager.SetStatus(id, request.Status); err != nil {
		writeStoreError(w, err)
		return
	}
	user, err := a.store.GetUserByID(id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, "user.status.update", "user", user.ID, http.StatusOK, map[string]any{"email": user.Email, "status": user.Status})
	writeJSON(w, http.StatusOK, user)
}

func (a *Admin) listRoles(w http.ResponseWriter) { writeJSON(w, http.StatusOK, a.userManagerRoles()) }

func (a *Admin) userManagerRoles() []model.Role {
	if provider, ok := a.userManager.(interface{ ListRoles() []model.Role }); ok {
		return provider.ListRoles()
	}
	return a.store.ListRoles()
}

func (a *Admin) listPermissions(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, a.store.ListPermissions())
}

func (a *Admin) createRole(w http.ResponseWriter, r *http.Request) {
	if !a.hasPermission(r, "*") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only super_admin can create roles"})
		return
	}
	var request model.CreateRoleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	role, err := a.userManager.CreateRole(request.Name, request.Description, request.Permissions)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, "role.create", "role", role.Name, http.StatusCreated, map[string]any{"name": role.Name, "permissions": role.Permissions})
	writeJSON(w, http.StatusCreated, role)
}

func (a *Admin) updateRolePermissions(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/v1/roles/"), "/permissions")
	var request struct {
		Permissions []string `json:"permissions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if !a.hasPermission(r, "*") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only super_admin can change role permissions"})
		return
	}
	if strings.EqualFold(name, "super_admin") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "super_admin permissions are immutable"})
		return
	}
	provider, ok := a.userManager.(interface{ UpdateRolePermissions(string, []string) error })
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "role management unavailable"})
		return
	}
	if err := provider.UpdateRolePermissions(name, request.Permissions); err != nil {
		writeStoreError(w, err)
		return
	}
	role, err := a.store.GetRoleByName(name)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	a.recordAudit(r, "role.permissions.update", "role", role.Name, http.StatusOK, map[string]any{"name": role.Name, "permissions": role.Permissions})
	writeJSON(w, http.StatusOK, role)
}

func (a *Admin) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	query, err := parseAuditLogQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	page, err := a.store.ListAuditLogs(query)
	if err != nil {
		a.logger.Error("list audit logs failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list audit logs failed"})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseAuditLogQuery(r *http.Request) (model.AuditLogQuery, error) {
	values := r.URL.Query()
	query := model.AuditLogQuery{Action: values.Get("action"), ResourceType: values.Get("resource_type"), ActorID: values.Get("actor_id"), RequestID: values.Get("request_id")}
	var err error
	if query.Page, err = positiveQueryInt(values.Get("page"), 1); err != nil {
		return query, fmt.Errorf("invalid page")
	}
	if query.PageSize, err = positiveQueryInt(values.Get("page_size"), 20); err != nil {
		return query, fmt.Errorf("invalid page_size")
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	if raw := values.Get("from"); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid from; use RFC3339")
		}
		query.From = &value
	}
	if raw := values.Get("to"); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid to; use RFC3339")
		}
		query.To = &value
	}
	if query.From != nil && query.To != nil && query.From.After(*query.To) {
		return query, fmt.Errorf("from must be before to")
	}
	return query, nil
}

func positiveQueryInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, errors.New("must be positive")
	}
	return value, nil
}

func (a *Admin) recordAudit(r *http.Request, action, resourceType, resourceID string, statusCode int, details map[string]any) {
	if a.auditor == nil {
		return
	}
	actor, ok := r.Context().Value(auditActorContextKey{}).(audit.Actor)
	if !ok {
		actor, _ = a.requestActor(r)
	}
	a.auditor.Record(r.Context(), actor, r, action, resourceType, resourceID, statusCode, details)
}

func (a *Admin) importOpenAPI(w http.ResponseWriter, r *http.Request) {
	var request model.OpenAPIImportRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	var document map[string]any
	if len(request.Document) > 0 {
		if err := json.Unmarshal(request.Document, &document); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "document must be a valid OpenAPI JSON object"})
			return
		}
	} else if strings.TrimSpace(request.DocumentYAML) != "" {
		if err := yaml.Unmarshal([]byte(request.DocumentYAML), &document); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "document_yaml must be a valid OpenAPI YAML object"})
			return
		}
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "document or document_yaml is required"})
		return
	}
	version := stringValue(document["openapi"])
	if !strings.HasPrefix(version, "3.") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only OpenAPI 3.x documents are supported"})
		return
	}
	request.UpstreamURL = strings.TrimSpace(request.UpstreamURL)
	if request.UpstreamURL == "" {
		request.UpstreamURL = openAPIServerURL(document)
	}
	if request.UpstreamURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upstream_url is required when the OpenAPI document has no server URL"})
		return
	}
	if request.PathPrefix != "" && (!strings.HasPrefix(request.PathPrefix, "/") || strings.Contains(request.PathPrefix, "//")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path_prefix must start with / and must not contain //"})
		return
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "document must contain a non-empty paths object"})
		return
	}
	created := make([]model.API, 0)
	errorsList := make([]map[string]string, 0)
	for path, rawPath := range paths {
		pathItem, ok := rawPath.(map[string]any)
		if !ok {
			errorsList = append(errorsList, map[string]string{"path": path, "error": "path item must be an object"})
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"} {
			rawOperation, exists := pathItem[method]
			if !exists {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				errorsList = append(errorsList, map[string]string{"path": path, "method": method, "error": "operation must be an object"})
				continue
			}
			api, err := openAPIOperation(joinOpenAPIPath(request.PathPrefix, path), method, operation, document, request)
			if err != nil {
				errorsList = append(errorsList, map[string]string{"path": path, "method": method, "error": err.Error()})
				continue
			}
			validationRequest := model.CreateAPIRequest{Name: api.Name, Method: api.Method, Path: api.Path, AuthMode: api.AuthMode, AuthConfig: api.AuthConfig, UpstreamURL: api.UpstreamURL, ResponseStatus: api.ResponseStatus, RequestSchema: api.RequestSchema, ResponseSchema: api.ResponseSchema}
			if err := validateAPIRequest(validationRequest, a.productionMode); err != nil {
				errorsList = append(errorsList, map[string]string{"path": path, "method": method, "error": err.Error()})
				continue
			}
			if err := a.checkRouteConflict("", api.Method, api.Path); err != nil {
				errorsList = append(errorsList, map[string]string{"path": path, "method": method, "error": err.Error()})
				continue
			}
			if err := a.store.CreateAPI(api); err != nil {
				errorsList = append(errorsList, map[string]string{"path": path, "method": method, "error": "API already exists or could not be saved"})
				continue
			}
			created = append(created, api)
			a.recordAudit(r, "api.import", "api", api.ID, http.StatusCreated, map[string]any{"name": api.Name, "method": api.Method, "path": api.Path, "source": "openapi"})
		}
	}
	status := http.StatusCreated
	if len(created) == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"created": created, "errors": errorsList, "created_count": len(created), "error_count": len(errorsList)})
}

func openAPIOperation(path, method string, operation, document map[string]any, request model.OpenAPIImportRequest) (model.API, error) {
	name := stringValue(operation["summary"])
	if name == "" {
		name = stringValue(operation["operationId"])
	}
	if name == "" {
		name = strings.ToUpper(method) + " " + path
	}
	requestSchema, err := openAPIContentSchema(operation, "requestBody", document)
	if err != nil {
		return model.API{}, err
	}
	responseStatus, responseSchema, err := openAPIResponse(operation, document)
	if err != nil {
		return model.API{}, err
	}
	authMode := strings.ToLower(strings.TrimSpace(request.AuthMode))
	if authMode == "" {
		authMode = openAPIAuthMode(operation, document)
	}
	return model.API{ID: newID("api"), Name: name, Description: stringValue(operation["description"]), Method: strings.ToUpper(method), Path: path,
		AuthMode: authMode, AuthConfig: request.AuthConfig, UpstreamURL: request.UpstreamURL, ResponseStatus: responseStatus,
		RequestSchema: requestSchema, ResponseSchema: responseSchema, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, nil
}

func openAPIServerURL(document map[string]any) string {
	servers, _ := document["servers"].([]any)
	if len(servers) == 0 {
		return ""
	}
	server, _ := servers[0].(map[string]any)
	url := stringValue(server["url"])
	if strings.Contains(url, "{") {
		return ""
	}
	return url
}

func joinOpenAPIPath(prefix, path string) string {
	if prefix == "" || prefix == "/" {
		return path
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(path, "/")
}

func stringValue(value any) string { text, _ := value.(string); return strings.TrimSpace(text) }

func openAPIAuthMode(operation, document map[string]any) string { return "api_key" }

func openAPIContentSchema(operation map[string]any, key string, document map[string]any) (json.RawMessage, error) {
	container, _ := operation[key].(map[string]any)
	content, _ := container["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	rawSchema, ok := media["schema"]
	if !ok {
		return nil, nil
	}
	return openAPISchema(rawSchema, document)
}

func openAPIResponse(operation, document map[string]any) (int, json.RawMessage, error) {
	responses, _ := operation["responses"].(map[string]any)
	keys := make([]string, 0, len(responses))
	for key := range responses {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		status, err := strconv.Atoi(key)
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		response, err := resolveOpenAPIValue(responses[key], document, map[string]bool{})
		if err != nil {
			return 0, nil, err
		}
		responseMap, _ := response.(map[string]any)
		content, _ := responseMap["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		if raw, ok := media["schema"]; ok {
			schema, err := openAPISchema(raw, document)
			return status, schema, err
		}
		return status, nil, nil
	}
	return http.StatusOK, nil, nil
}

func openAPISchema(raw any, document map[string]any) (json.RawMessage, error) {
	resolved, err := resolveOpenAPIValue(raw, document, map[string]bool{})
	if err != nil {
		return nil, err
	}
	normalizeOpenAPINullable(resolved)
	encoded, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI schema: %w", err)
	}
	return encoded, nil
}

func resolveOpenAPIValue(value any, document map[string]any, refs map[string]bool) (any, error) {
	switch item := value.(type) {
	case map[string]any:
		if reference := stringValue(item["$ref"]); reference != "" {
			if !strings.HasPrefix(reference, "#/components/") {
				return nil, fmt.Errorf("unsupported external OpenAPI reference %q", reference)
			}
			if refs[reference] {
				return nil, fmt.Errorf("cyclic OpenAPI reference %q", reference)
			}
			refs[reference] = true
			defer delete(refs, reference)
			parts := strings.Split(strings.TrimPrefix(reference, "#/"), "/")
			var target any = document
			for _, part := range parts {
				current, ok := target.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("OpenAPI reference not found %q", reference)
				}
				target, ok = current[part]
				if !ok {
					return nil, fmt.Errorf("OpenAPI reference not found %q", reference)
				}
			}
			resolved, err := resolveOpenAPIValue(target, document, refs)
			if err != nil {
				return nil, err
			}
			base, ok := resolved.(map[string]any)
			if !ok {
				return resolved, nil
			}
			merged := make(map[string]any, len(base)+len(item)-1)
			for key, entry := range base {
				merged[key] = entry
			}
			for key, entry := range item {
				if key != "$ref" {
					merged[key] = entry
				}
			}
			return resolveOpenAPIValue(merged, document, refs)
		}
		result := make(map[string]any, len(item))
		for key, entry := range item {
			next, err := resolveOpenAPIValue(entry, document, refs)
			if err != nil {
				return nil, err
			}
			result[key] = next
		}
		return result, nil
	case []any:
		result := make([]any, len(item))
		for i, entry := range item {
			next, err := resolveOpenAPIValue(entry, document, refs)
			if err != nil {
				return nil, err
			}
			result[i] = next
		}
		return result, nil
	default:
		return value, nil
	}
}

func normalizeOpenAPINullable(value any) {
	switch item := value.(type) {
	case map[string]any:
		if nullable, _ := item["nullable"].(bool); nullable {
			if typeName, ok := item["type"].(string); ok {
				item["type"] = []any{typeName, "null"}
			}
			delete(item, "nullable")
		}
		for _, entry := range item {
			normalizeOpenAPINullable(entry)
		}
	case []any:
		for _, entry := range item {
			normalizeOpenAPINullable(entry)
		}
	}
}

func (a *Admin) exportOpenAPI(w http.ResponseWriter, _ *http.Request) {
	paths := map[string]map[string]any{}
	apis, err := a.listAPIsChecked()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "export APIs unavailable"})
		return
	}
	sort.Slice(apis, func(i, j int) bool { return apis[i].Path < apis[j].Path })
	for _, api := range apis {
		method := strings.ToLower(api.Method)
		if paths[api.Path] == nil {
			paths[api.Path] = map[string]any{}
		}
		operation := map[string]any{"operationId": api.ID, "summary": api.Name, "description": api.Description, "responses": map[string]any{fmt.Sprint(responseStatus(api)): map[string]any{"description": "Success"}}}
		if len(api.RequestSchema) > 0 {
			operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": json.RawMessage(api.RequestSchema)}}}
		}
		if len(api.ResponseSchema) > 0 {
			operation["responses"] = map[string]any{fmt.Sprint(responseStatus(api)): map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": json.RawMessage(api.ResponseSchema)}}}}
		}
		if api.AuthMode == "api_key" {
			operation["security"] = []map[string][]string{{"ApiKeyAuth": {}}}
		}
		paths[api.Path][method] = operation
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]string{"title": "API Manager", "version": "v1"},
		"paths":   paths,
		"components": map[string]any{"securitySchemes": map[string]any{
			"ApiKeyAuth": map[string]any{"type": "apiKey", "in": "header", "name": "X-API-Key"},
		}},
	})
}

func responseStatus(api model.API) int {
	if api.ResponseStatus >= 200 && api.ResponseStatus <= 599 {
		return api.ResponseStatus
	}
	return http.StatusOK
}

func apiFromRequest(id string, request model.CreateAPIRequest, createdAt, updatedAt time.Time) model.API {
	return model.API{PublicVisible: request.PublicVisible, PublicTitle: request.PublicTitle, PublicSummary: request.PublicSummary, PublicCategory: request.PublicCategory, ID: id, Name: request.Name, Description: request.Description, Method: strings.ToUpper(request.Method), Path: request.Path, AuthMode: defaultAuthMode(request.AuthMode), AuthConfig: request.AuthConfig, RateLimitPerMinute: request.RateLimitPerMinute, DailyQuota: request.DailyQuota, MonthlyQuota: request.MonthlyQuota, ResponseStatus: request.ResponseStatus, ResponseBody: request.ResponseBody, RequestSchema: request.RequestSchema, ResponseSchema: request.ResponseSchema, ParametersSchema: request.ParametersSchema, Plugin: request.Plugin, UpstreamAuthRef: request.UpstreamAuthRef, UpstreamURL: request.UpstreamURL, UpstreamPath: request.UpstreamPath, StripPath: request.StripPath, UpstreamTimeoutMS: request.UpstreamTimeoutMS, UpstreamRetries: request.UpstreamRetries, CircuitThreshold: request.CircuitThreshold, CircuitResetSecs: request.CircuitResetSecs, CreatedAt: createdAt, UpdatedAt: updatedAt}
}

var allowedAPIMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

func normalizeAPIMethod(method string) string { return strings.ToUpper(strings.TrimSpace(method)) }

func validateAPIRequest(request model.CreateAPIRequest, productionMode bool) error {
	if len(request.PublicTitle) > 120 || len(request.PublicSummary) > 600 || len(request.PublicCategory) > 48 {
		return errors.New("public documentation fields exceed their size limits")
	}
	if request.PublicVisible && strings.TrimSpace(request.PublicTitle) == "" {
		return errors.New("a public title is required before showing an API in the public catalog")
	}

	if err := upstream.ValidatePath(request.Path, request.UpstreamPath); err != nil {
		return err
	}
	if request.UpstreamAuthRef != "" && request.UpstreamURL == "" {
		return errors.New("upstream_auth_ref requires upstream_url")
	}
	if strings.TrimSpace(request.Name) == "" {
		return errors.New("name is required")
	}
	if !allowedAPIMethods[normalizeAPIMethod(request.Method)] {
		return errors.New("method must be exactly one of GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS")
	}
	if !strings.HasPrefix(request.Path, "/") || strings.Contains(request.Path, "//") {
		return errors.New("path must start with / and must not contain //")
	}
	if request.AuthMode != "" && request.AuthMode != "api_key" && request.AuthMode != "none" {
		return errors.New("auth_mode must be api_key or none")
	}
	if len(request.AuthConfig) != 0 {
		return errors.New("KEY authentication does not accept auth_config")
	}

	if request.ResponseStatus != 0 && (request.ResponseStatus < 100 || request.ResponseStatus > 599) {
		return errors.New("response_status must be between 100 and 599")
	}
	if request.RateLimitPerMinute < 0 || request.DailyQuota < 0 || request.MonthlyQuota < 0 {
		return errors.New("rate limits and quotas must not be negative")
	}
	if request.UpstreamURL != "" {
		parsed, err := url.ParseRequestURI(request.UpstreamURL)
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("upstream_url must be a valid http or https URL")
		}
		if productionMode && parsed.Scheme != "https" {
			return errors.New("production upstream_url must use https")
		}
	}
	if err := apiSchema.ValidateSchema(request.RequestSchema); err != nil {
		return fmt.Errorf("invalid request_schema: %w", err)
	}
	if err := apiSchema.ValidateSchema(request.ResponseSchema); err != nil {
		return fmt.Errorf("invalid response_schema: %w", err)
	}
	if err := apiSchema.ValidateSchema(request.ParametersSchema); err != nil {
		return fmt.Errorf("invalid parameters_schema: %w", err)
	}
	if request.UpstreamTimeoutMS < 0 || request.UpstreamTimeoutMS > 120000 {
		return errors.New("upstream_timeout_ms must be between 0 and 120000")
	}
	if request.UpstreamRetries < 0 || request.UpstreamRetries > 5 {
		return errors.New("upstream_retries must be between 0 and 5")
	}
	if request.CircuitThreshold < 0 || request.CircuitThreshold > 100 {
		return errors.New("circuit_breaker_threshold must be between 0 and 100")
	}
	if request.CircuitResetSecs < 0 || request.CircuitResetSecs > 3600 {
		return errors.New("circuit_breaker_reset_seconds must be between 0 and 3600")
	}
	if request.Plugin == "" && request.UpstreamURL == "" && request.ResponseBody == "" && request.ResponseStatus == 0 {
		return errors.New("one of plugin, upstream_url, response_body, or response_status must be configured")
	}
	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func writePluginError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, plugin.ErrInvalidPackage), errors.Is(err, plugin.ErrInvalidLibraryPackage):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, plugin.ErrLibraryPackageExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "library package already exists"})
	case errors.Is(err, store.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "resource conflicts with existing state"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "plugin operation failed"})
	}
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
	case errors.Is(err, store.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "resource conflicts with existing state"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "storage operation failed"})
	}
}

func newID(prefix string) string { return fmt.Sprintf("%s_%s", prefix, randomSecret(8)) }

func randomSecret(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		panic("cryptographic random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// Ambiguous parameter patterns would make authentication depend on creation order.
func (a *Admin) checkRouteConflict(excludeID, method, path string) error {
	if !strings.Contains(path, "{") {
		return nil
	}
	proposed := strings.Split(strings.Trim(path, "/"), "/")
	apis, err := a.listAPIsChecked()
	if err != nil {
		return errors.New("API routes unavailable")
	}
	for _, existing := range apis {
		if existing.ID == excludeID || !strings.EqualFold(existing.Method, method) || !strings.Contains(existing.Path, "{") {
			continue
		}
		parts := strings.Split(strings.Trim(existing.Path, "/"), "/")
		if len(parts) != len(proposed) {
			continue
		}
		overlap := true
		for i := range proposed {
			leftParam := strings.HasPrefix(proposed[i], "{") && strings.HasSuffix(proposed[i], "}")
			rightParam := strings.HasPrefix(parts[i], "{") && strings.HasSuffix(parts[i], "}")
			if !leftParam && !rightParam && proposed[i] != parts[i] {
				overlap = false
				break
			}
		}
		if overlap {
			return errors.New("ambiguous parameter route conflicts with existing API")
		}
	}
	return nil
}

func validateStoredAPI(api model.API, productionMode bool) error {
	if api.AuthMode != "api_key" && api.AuthMode != "none" {
		return errors.New("stored routes must use KEY authentication or no authentication")
	}
	payload, err := json.Marshal(api)
	if err != nil {
		return err
	}
	var request model.CreateAPIRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return err
	}
	return validateAPIRequest(request, productionMode)
}

func defaultAuthMode(mode string) string {
	if mode == "none" {
		return "none"
	}
	return "api_key"
}
