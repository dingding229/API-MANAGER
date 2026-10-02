package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"api-manager/internal/model"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Memory struct {
	siteSettings  model.SiteSettingsRecord
	bootstrapHash string
	bootstrapUsed bool
	mu            sync.RWMutex
	apis          map[string]model.API
	credentials   map[string]model.Credential
	users         map[string]model.User
	sessions      map[string]model.Session
	resets        map[string]model.PasswordReset
	releases      map[string][]model.Release
	permissions   map[string]model.Permission
	roles         map[string]model.Role
	userRoles     map[string][]string
	plugins       map[string]model.Plugin
	pluginData    map[string]model.PluginData
	auditLogs     []model.AuditLog
	nextAuditID   int64
}

func NewMemory() *Memory {
	memory := &Memory{apis: make(map[string]model.API), credentials: make(map[string]model.Credential), users: make(map[string]model.User), sessions: make(map[string]model.Session), resets: make(map[string]model.PasswordReset), releases: make(map[string][]model.Release), permissions: make(map[string]model.Permission), roles: make(map[string]model.Role), userRoles: make(map[string][]string), plugins: make(map[string]model.Plugin), pluginData: make(map[string]model.PluginData), auditLogs: make([]model.AuditLog, 0)}
	memory.seedRBAC()
	return memory
}

func (m *Memory) seedRBAC() {
	for _, permission := range DefaultPermissions() {
		if _, exists := m.permissions[permission.Code]; !exists {
			m.permissions[permission.Code] = permission
		}
	}
	for _, role := range DefaultRoles() {
		if existing, exists := m.roles[role.Name]; exists {
			existing.Description = role.Description
			m.roles[role.Name] = existing
		} else {
			m.roles[role.Name] = role
		}
	}
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close()                     {}

func (m *Memory) CreateAPI(api model.API) error {
	methods, err := model.NormalizeMethods(api.Method, api.Methods)
	if err != nil {
		return err
	}
	api.Methods, api.Method = methods, methods[0]
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apis[api.ID]; exists {
		return ErrConflict
	}
	for _, existing := range m.apis {
		if model.RoutesConflict(existing, api) {
			return ErrConflict
		}
	}
	m.apis[api.ID] = api
	return nil
}

func (m *Memory) UpdateAPI(api model.API) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.updateAPI(api)
}
func (m *Memory) UpdateAndRelease(api model.API) (model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.updateAPI(api); err != nil {
		return model.Release{}, err
	}
	return m.createRelease(m.apis[api.ID]), nil
}
func (m *Memory) updateAPI(api model.API) error {
	methods, err := model.NormalizeMethods(api.Method, api.Methods)
	if err != nil {
		return err
	}
	api.Methods, api.Method = methods, methods[0]
	if _, exists := m.apis[api.ID]; !exists {
		return ErrNotFound
	}
	for id, existing := range m.apis {
		if id != api.ID && model.RoutesConflict(existing, api) {
			return ErrConflict
		}
	}
	m.apis[api.ID] = api
	return nil
}

func (m *Memory) GetAPI(id string) (model.API, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	api, exists := m.apis[id]
	if !exists {
		return model.API{}, ErrNotFound
	}
	return api, nil
}

func (m *Memory) ListAPIs() []model.API {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]model.API, 0, len(m.apis))
	for _, api := range m.apis {
		result = append(result, api)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}

func (m *Memory) DeleteAPI(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.apis[id]; !exists {
		return ErrNotFound
	}
	delete(m.apis, id)
	return nil
}

func (m *Memory) CreateCredential(credential model.Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.credentials[credential.ID]; exists {
		return ErrConflict
	}
	m.credentials[credential.ID] = credential
	return nil
}

func (m *Memory) UpdateCredential(credential model.Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.credentials[credential.ID]; !exists {
		return ErrNotFound
	}
	m.credentials[credential.ID] = credential
	return nil
}

func (m *Memory) RotateCredential(id, prefix, hash, encryptedKey string) (model.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	credential, exists := m.credentials[id]
	if !exists {
		return model.Credential{}, ErrNotFound
	}
	if credential.Revoked {
		return model.Credential{}, ErrConflict
	}
	credential.Prefix, credential.Hash, credential.EncryptedKey = prefix, hash, encryptedKey
	credential.KeyAvailable = credential.EncryptedKey != ""
	m.credentials[id] = credential
	return credential, nil
}

func (m *Memory) GetCredential(id string) (model.Credential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	credential, exists := m.credentials[id]
	if !exists {
		return model.Credential{}, ErrNotFound
	}
	credential.KeyAvailable = credential.EncryptedKey != ""
	return credential, nil
}

func (m *Memory) ListCredentials() []model.Credential {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]model.Credential, 0, len(m.credentials))
	for _, credential := range m.credentials {
		credential.KeyAvailable = credential.EncryptedKey != ""
		result = append(result, credential)
	}
	return result
}

func (m *Memory) FindCredentialByHash(hash string) (model.Credential, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, credential := range m.credentials {
		if credential.Hash == hash {
			return credential, true
		}
	}
	return model.Credential{}, false
}

func (m *Memory) CreateUser(user model.User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if existing.Username == user.Username || (user.Email != "" && existing.Email == user.Email) {
			return ErrConflict
		}
	}
	roles := user.Roles
	if len(roles) == 0 && user.Role != "" {
		roles = []string{user.Role}
	}
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}
	for _, role := range roles {
		if _, ok := m.roles[role]; !ok {
			return ErrNotFound
		}
	}
	user.Roles = dedupe(roles)
	user.Role = user.Roles[0]
	m.users[user.ID] = user
	m.userRoles[user.ID] = append([]string(nil), user.Roles...)
	return nil
}

func (m *Memory) GetUserByUsername(username string) (model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.Username == username {
			u.Roles = append([]string(nil), m.userRoles[u.ID]...)
			return u, nil
		}
	}
	return model.User{}, ErrNotFound
}
func (m *Memory) GetUserByEmail(email string) (model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if email != "" && u.Email == email {
			u.Roles = append([]string(nil), m.userRoles[u.ID]...)
			return u, nil
		}
	}
	return model.User{}, ErrNotFound
}

func (m *Memory) ListUsers() []model.User {
	m.mu.RLock()
	defer m.mu.RUnlock()
	users := make([]model.User, 0, len(m.users))
	for _, user := range m.users {
		user.Roles = append([]string(nil), m.userRoles[user.ID]...)
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	return users
}

func (m *Memory) CountUsers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users)
}

func (m *Memory) CreateRelease(api model.API) (model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createRelease(api), nil
}
func (m *Memory) createRelease(api model.API) model.Release {
	version := len(m.releases[api.ID]) + 1
	publishedAt := time.Now().UTC()
	if api.PublishedAt != nil {
		publishedAt = *api.PublishedAt
	}
	release := model.Release{ID: int64(version), APIID: api.ID, Version: version, Snapshot: api, PublishedAt: publishedAt}
	m.releases[api.ID] = append(m.releases[api.ID], release)
	return release
}

func (m *Memory) ListReleases(apiID string) []model.Release {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.releases[apiID]
	result := make([]model.Release, len(items))
	copy(result, items)
	return result
}

func (m *Memory) GetRelease(apiID string, version int) (model.Release, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, release := range m.releases[apiID] {
		if release.Version == version {
			return release, nil
		}
	}
	return model.Release{}, ErrNotFound
}

func (m *Memory) GetUserByID(id string) (model.User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	user, ok := m.users[id]
	if !ok {
		return model.User{}, ErrNotFound
	}
	user.Roles = append([]string(nil), m.userRoles[id]...)
	return user, nil
}

func (m *Memory) UpdateUserStatus(id, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[id]
	if !ok {
		return ErrNotFound
	}
	if status == "disabled" && m.lastActiveAdmin(id) {
		return ErrConflict
	}
	user.Status = status
	user.UpdatedAt = time.Now().UTC()
	m.users[id] = user
	if status == "disabled" {
		for hash, reset := range m.resets {
			if reset.UserID == id {
				delete(m.resets, hash)
			}
		}
		for hash, session := range m.sessions {
			if session.UserID == id {
				delete(m.sessions, hash)
			}
		}
	}
	return nil
}

func (m *Memory) ListPermissions() []model.Permission {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]model.Permission, 0, len(m.permissions))
	for _, permission := range m.permissions {
		items = append(items, permission)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	return items
}

func (m *Memory) CreateRole(role model.Role) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[role.Name]; ok {
		return ErrConflict
	}
	for _, code := range role.Permissions {
		if _, ok := m.permissions[code]; !ok {
			m.permissions[code] = model.Permission{ID: code, Code: code}
		}
	}
	role.Permissions = dedupe(role.Permissions)
	m.roles[role.Name] = role
	return nil
}

func (m *Memory) UpdateRolePermissions(name string, permissions []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	role, ok := m.roles[name]
	if !ok {
		return ErrNotFound
	}
	for _, code := range permissions {
		if _, ok := m.permissions[code]; !ok {
			return ErrNotFound
		}
	}
	role.Permissions = dedupe(permissions)
	m.roles[name] = role
	return nil
}

func (m *Memory) ListRoles() []model.Role {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]model.Role, 0, len(m.roles))
	for _, role := range m.roles {
		role.Permissions = append([]string(nil), role.Permissions...)
		items = append(items, role)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (m *Memory) GetRoleByName(name string) (model.Role, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	role, ok := m.roles[name]
	if !ok {
		return model.Role{}, ErrNotFound
	}
	role.Permissions = append([]string(nil), role.Permissions...)
	return role, nil
}

func (m *Memory) AssignUserRoles(userID string, roles []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[userID]; !ok {
		return ErrNotFound
	}
	if !containsSuperAdmin(roles) && m.lastActiveAdmin(userID) {
		return ErrConflict
	}
	for _, role := range roles {
		if _, ok := m.roles[role]; !ok {
			return ErrNotFound
		}
	}
	roles = dedupe(roles)
	m.userRoles[userID] = roles
	user := m.users[userID]
	user.Roles = append([]string(nil), roles...)
	if len(roles) > 0 {
		user.Role = roles[0]
	}
	user.UpdatedAt = time.Now().UTC()
	m.users[userID] = user
	return nil
}

func (m *Memory) ListUserRoles(userID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]string(nil), m.userRoles[userID]...)
}

func (m *Memory) GetUserPermissions(userID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	set := map[string]bool{}
	for _, roleName := range m.userRoles[userID] {
		for _, permission := range m.roles[roleName].Permissions {
			set[permission] = true
		}
	}
	permissions := make([]string, 0, len(set))
	for permission := range set {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}

func dedupe(values []string) []string {
	set := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !set[value] {
			set[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (m *Memory) EnsureRBAC() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seedRBAC()
	return nil
}

func (m *Memory) CreateAuditLog(log model.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextAuditID++
	log.ID = m.nextAuditID
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	log.Details = cloneAuditDetails(log.Details)
	m.auditLogs = append(m.auditLogs, log)
	return nil
}

func (m *Memory) ListAuditLogs(query model.AuditLogQuery) (model.AuditLogPage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	query = normalizeAuditQuery(query)
	items := make([]model.AuditLog, 0, len(m.auditLogs))
	for i := len(m.auditLogs) - 1; i >= 0; i-- {
		log := m.auditLogs[i]
		if !matchesAuditQuery(log, query) {
			continue
		}
		log.Details = cloneAuditDetails(log.Details)
		items = append(items, log)
	}
	total := int64(len(items))
	start := (query.Page - 1) * query.PageSize
	if start >= len(items) {
		items = []model.AuditLog{}
	} else {
		end := start + query.PageSize
		if end > len(items) {
			end = len(items)
		}
		items = items[start:end]
	}
	return model.AuditLogPage{Items: items, Page: query.Page, PageSize: query.PageSize, Total: total}, nil
}

func normalizeAuditQuery(query model.AuditLogQuery) model.AuditLogQuery {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	return query
}

func matchesAuditQuery(log model.AuditLog, query model.AuditLogQuery) bool {
	if query.Action != "" && log.Action != query.Action {
		return false
	}
	if query.ResourceType != "" && log.ResourceType != query.ResourceType {
		return false
	}
	if query.ActorID != "" && log.ActorID != query.ActorID {
		return false
	}
	if query.RequestID != "" && log.RequestID != query.RequestID {
		return false
	}
	if query.From != nil && log.CreatedAt.Before(*query.From) {
		return false
	}
	if query.To != nil && log.CreatedAt.After(*query.To) {
		return false
	}
	return true
}

func cloneAuditDetails(details map[string]any) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	copied := make(map[string]any, len(details))
	for key, value := range details {
		copied[key] = value
	}
	return copied
}

func pluginDataKey(pluginName, namespace, key string) string {
	return pluginName + "\x00" + namespace + "\x00" + key
}
func (m *Memory) PutPluginData(item model.PluginData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item.UpdatedAt = time.Now().UTC()
	m.pluginData[pluginDataKey(item.PluginName, item.Namespace, item.Key)] = item
	return nil
}
func (m *Memory) GetPluginData(pluginName, namespace, key string) (model.PluginData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, ok := m.pluginData[pluginDataKey(pluginName, namespace, key)]
	if !ok {
		return model.PluginData{}, ErrNotFound
	}
	return item, nil
}

func (m *Memory) CreatePlugin(item model.Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.plugins[item.ID]; exists {
		return ErrConflict
	}
	for _, existing := range m.plugins {
		if existing.Name == item.Name && existing.Version == item.Version {
			return ErrConflict
		}
	}
	m.plugins[item.ID] = clonePlugin(item)
	return nil
}

func (m *Memory) GetPlugin(id string) (model.Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	item, exists := m.plugins[id]
	if !exists {
		return model.Plugin{}, ErrNotFound
	}
	return clonePlugin(item), nil
}

func (m *Memory) ListPlugins() []model.Plugin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]model.Plugin, 0, len(m.plugins))
	for _, item := range m.plugins {
		items = append(items, clonePlugin(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func (m *Memory) SetPluginEnabled(id string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, exists := m.plugins[id]
	if !exists {
		return ErrNotFound
	}
	if enabled {
		for otherID, other := range m.plugins {
			if otherID != id && other.Name == item.Name && other.Enabled {
				other.Enabled = false
				other.UpdatedAt = time.Now().UTC()
				m.plugins[otherID] = other
			}
		}
	}
	item.Enabled = enabled
	item.UpdatedAt = time.Now().UTC()
	m.plugins[id] = item
	return nil
}

func (m *Memory) DeletePlugin(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.plugins[id]; !exists {
		return ErrNotFound
	}
	delete(m.plugins, id)
	return nil
}

func clonePlugin(item model.Plugin) model.Plugin {
	item.Manifest = append([]byte(nil), item.Manifest...)
	return item
}

func (m *Memory) lastActiveAdmin(id string) bool {
	target, ok := m.users[id]
	if !ok || target.Status != "active" || !containsSuperAdmin(m.userRoles[id]) {
		return false
	}
	count := 0
	for uid, u := range m.users {
		if u.Status == "active" && containsSuperAdmin(m.userRoles[uid]) {
			count++
		}
	}
	return count <= 1
}
