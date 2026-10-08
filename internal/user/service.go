package user

import (
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"api-manager/internal/audit"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

const passwordHashCost = 12

type Store interface {
	CreateUser(model.User) error
	GetUserByUsername(string) (model.User, error)
	GetUserByEmail(string) (model.User, error)
	GetUserByID(string) (model.User, error)
	ListUsers() []model.User
	CountUsers() int
	UpdateUserStatus(string, string) error
	UpdateUserProfile(string, model.UserProfileUpdate) (model.User, bool, error)
	EnsureRBAC() error
	ListPermissions() []model.Permission
	CreateRole(model.Role) error
	UpdateRolePermissions(string, []string) error
	ListRoles() []model.Role
	GetRoleByName(string) (model.Role, error)
	AssignUserRoles(string, []string) error
	ListUserRoles(string) []string
	GetUserPermissions(string) []string
}

type Service struct {
	profileRequestGuard func(*http.Request, string, string) error
	profileGuard        func(string, string, model.UpdateUserProfileRequest) error
	bootstrapHash       string
	store               Store
	ttl                 time.Duration
	recovery            *recovery
	recoveryMu          sync.RWMutex
}
type sessionStore interface {
	CreateSession(model.Session) error
	GetSession(string) (model.Session, error)
	DeleteSession(string) error
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9]{3,20}$`)
var dummyPasswordHash = func() []byte {
	hash, _ := bcrypt.GenerateFromPassword([]byte("dummy-password-check"), passwordHashCost)
	return hash
}()

func NewService(store Store) *Service { return &Service{store: store, ttl: 12 * time.Hour} }

func (s *Service) EnsureDefaults() error { return s.store.EnsureRBAC() }
func (s *Service) Count() int            { return s.store.CountUsers() }

func (s *Service) CountChecked() (int, error) {
	if checked, ok := s.store.(interface{ CountUsersChecked() (int, error) }); ok {
		return checked.CountUsersChecked()
	}
	return s.store.CountUsers(), nil
}

func (s *Service) RecordAudit(actor audit.Actor, r *http.Request, action, resourceType, resourceID string, statusCode int, details map[string]any) {
	auditStore, ok := s.store.(audit.Store)
	if !ok {
		return
	}
	audit.New(auditStore, nil).Record(r.Context(), actor, r, action, resourceType, resourceID, statusCode, details)
}

func (s *Service) Create(username, password, role string) (model.User, error) {
	return s.CreateWithRoles(username, password, []string{role})
}

func (s *Service) CreateWithRoles(username, password string, roles []string) (model.User, error) {
	return s.CreateWithContact(username, "", password, roles)
}
func (s *Service) CreateWithContact(username, email, password string, roles []string) (model.User, error) {
	u, err := s.PrepareUser(username, email, password, roles)
	if err != nil {
		return u, err
	}
	return u, s.store.CreateUser(u)
}
func (s *Service) CreateVerifiedUser(username, email, nickname, password string, roles []string, identity *model.Identity) (model.User, error) {
	u, err := s.PrepareUser(username, email, password, roles)
	if err != nil {
		return u, err
	}
	u.EmailVerified = true
	if nickname != "" {
		u.Nickname = nickname
	}
	if identity != nil {
		creator, ok := s.store.(interface {
			CreateIdentityUser(model.User, *model.Identity) error
		})
		if !ok {
			return u, errors.New("identity storage unavailable")
		}
		return u, creator.CreateIdentityUser(u, identity)
	}
	return u, s.store.CreateUser(u)
}
func (s *Service) PrepareUser(username, email, password string, roles []string) (model.User, error) {
	username, email = normalizeUsername(username), normalizeEmail(email)
	if !validUsername(username) || (email != "" && !validEmail(email)) || !validPassword(password) {
		return model.User{}, errors.New("用户名须为 3–20 位字母或数字，密码须为 8–24 个字符")
	}
	roles = normalizeRoles(roles)
	if len(roles) == 0 {
		roles = []string{"member"}
	}
	for _, role := range roles {
		if _, err := s.store.GetRoleByName(role); err != nil {
			return model.User{}, fmt.Errorf("unknown role %q", role)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return model.User{}, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC()
	uid := ids.NewUUID()
	u := model.User{UID: uid, ID: uid, Nickname: username, Username: username, Email: email, PasswordHash: string(hash), Role: roles[0], Roles: roles, Status: "active", CreatedAt: now, UpdatedAt: now}
	return u, nil
}

func (s *Service) Can(userID, permission string) bool {
	for _, granted := range s.store.GetUserPermissions(userID) {
		if granted == "*" || granted == permission {
			return true
		}
	}
	return false
}

func (s *Service) Profile(user model.User) map[string]any {
	names := map[string]string{}
	for _, r := range s.store.ListRoles() {
		names[r.Name] = r.DisplayName
		if names[r.Name] == "" {
			names[r.Name] = r.Name
		}
	}
	permissions := map[string]string{}
	for _, p := range s.store.ListPermissions() {
		permissions[p.Code] = p.Description
	}
	zone := ""
	if st, ok := s.store.(interface{ PersonalTimeZone(string) (string, error) }); ok {
		zone, _ = st.PersonalTimeZone(user.ID)
	}
	return map[string]any{"personal_time_zone": zone, "user": user, "permissions": s.store.GetUserPermissions(user.ID), "level_names": names, "permission_names": permissions}
}

func (s *Service) ListUsers() []model.User                 { return s.store.ListUsers() }
func (s *Service) ListRoles() []model.Role                 { return s.store.ListRoles() }
func (s *Service) ListPermissions() []model.Permission     { return s.store.ListPermissions() }
func (s *Service) GetRole(name string) (model.Role, error) { return s.store.GetRoleByName(name) }
func (s *Service) UpdateRolePermissions(name string, codes []string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return errors.New("仅支持管理员、普通用户、接口开发者三种角色")
	}
	if name == "super_admin" {
		return errors.New("super_admin permissions are immutable")
	}
	normalized := normalizePermissions(codes)
	if !store.RolePermissionsAllowed(name, normalized) {
		return errors.New("该角色不能授予所选管理权限")
	}
	known := make(map[string]struct{})
	for _, permission := range s.store.ListPermissions() {
		known[permission.Code] = struct{}{}
	}
	for _, code := range normalized {
		if _, ok := known[code]; !ok {
			return fmt.Errorf("unknown permission %q", code)
		}
	}
	return s.store.UpdateRolePermissions(name, normalized)
}
func normalizePermissions(values []string) []string {
	set := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && value != "*" {
			if _, ok := set[value]; !ok {
				set[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func (s *Service) CreateRole(name, description string, permissions []string) (model.Role, error) {
	role := model.Role{DisplayName: map[string]string{"super_admin": "管理员", "member": "普通用户", "api_developer": "接口开发者"}[strings.ToLower(strings.TrimSpace(name))], ID: ids.NewUUID(), Name: strings.ToLower(strings.TrimSpace(name)), Description: strings.TrimSpace(description), Permissions: normalizeRoles(permissions)}
	if !store.SupportedRole(role.Name) || !store.RolePermissionsAllowed(role.Name, role.Permissions) {
		return model.Role{}, errors.New("仅支持管理员、普通用户、接口开发者三种角色")
	}
	if err := s.store.CreateRole(role); err != nil {
		return model.Role{}, err
	}
	return s.store.GetRoleByName(role.Name)
}

func (s *Service) AssignRoles(userID string, roles []string) error {
	roles = normalizeRoles(roles)
	if len(roles) == 0 {
		return errors.New("at least one role is required")
	}
	for _, role := range roles {
		if _, err := s.store.GetRoleByName(role); err != nil {
			return fmt.Errorf("unknown role %q", role)
		}
	}
	return s.store.AssignUserRoles(userID, roles)
}

func (s *Service) SetStatus(userID, status string) error {
	if status != "active" && status != "disabled" {
		return errors.New("status must be active or disabled")
	}
	return s.store.UpdateUserStatus(userID, status)
}

func normalizeRoles(values []string) []string {
	set := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !set[value] {
			set[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (s *Service) SetSessionTTL(ttl time.Duration) { s.ttl = ttl }

func (s *Service) EnsureInitialAdmin(username, password string) error {
	count, err := s.CountChecked()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	if password == "" {
		return errors.New("initial administrator helper requires an explicit password")
	}
	_, err = s.Create(username, password, "super_admin")
	return err
}

func (s *Service) Authenticate(username, password string) (model.User, string, error) {
	return s.authenticate(username, password, nil)
}
func (s *Service) lookupLogin(identifier string) (model.User, error) {
	value := strings.ToLower(strings.TrimSpace(identifier))
	if validEmail(value) {
		result, err := s.store.GetUserByEmail(value)
		if !errors.Is(err, store.ErrNotFound) {
			return result, err
		}
	}
	return s.store.GetUserByUsername(value)
}
func (s *Service) authenticate(username, password string, r *http.Request) (model.User, string, error) {
	if len(password) > 72 {
		return model.User{}, "", ErrInvalidCredentials
	}
	user, lookupErr := s.lookupLogin(username)
	hash := []byte(user.PasswordHash)
	if lookupErr != nil || len(hash) == 0 {
		hash = dummyPasswordHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if lookupErr != nil || user.Status != "active" || passwordErr != nil {
		return model.User{}, "", ErrInvalidCredentials
	}
	return s.StartSession(user, r)
}
func (s *Service) VerifyPassword(username, password string) (model.User, error) {
	if len(password) > 72 {
		return model.User{}, ErrInvalidCredentials
	}
	user, lookupErr := s.lookupLogin(username)
	hash := []byte(user.PasswordHash)
	if lookupErr != nil || len(hash) == 0 {
		hash = dummyPasswordHash
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || lookupErr != nil || user.Status != "active" {
		return model.User{}, ErrInvalidCredentials
	}
	return user, nil
}
func (s *Service) StartSession(user model.User, r *http.Request) (model.User, string, error) {
	sessions, ok := s.store.(sessionStore)
	if !ok {
		return model.User{}, "", errors.New("session storage unavailable")
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return model.User{}, "", err
	}
	token := "us_" + hex.EncodeToString(random)
	created := time.Now().UTC()
	info := httpx.ClientInfo{Source: "unknown"}
	agent := ""
	if r != nil {
		info = httpx.Client(r)
		agent = cleanAgent(r.UserAgent())
	}
	binding := ""
	var err error
	if r != nil {
		binding, err = auth.AssignDevice(r)
		if err != nil {
			return model.User{}, "", err
		}
	}
	if err := sessions.CreateSession(model.Session{DeviceBindingHash: binding, ID: ids.NewUUID(), CreatedAt: created, LastSeenAt: created, LoginIP: info.IP, LastIP: info.IP, PeerIP: info.PeerIP, IPSource: info.Source, UserAgent: agent, Device: deviceLabel(agent), Hash: auth.HashAPIKey(token), UserID: user.ID, ExpiresAt: time.Now().Add(s.ttl), AuthenticatedEmail: user.Email, AuthRevision: user.AuthRevision, AuthenticatedUsername: user.Username, AuthenticatedPasswordHash: user.PasswordHash}); err != nil {
		if errors.Is(err, store.ErrConflict) || errors.Is(err, store.ErrNotFound) {
			return model.User{}, "", ErrInvalidCredentials
		}
		return model.User{}, "", err
	}
	return user, token, nil
}
func (s *Service) ValidateSession(token string) (model.User, error) {
	if len(token) != 67 || !strings.HasPrefix(token, "us_") {
		return model.User{}, ErrInvalidCredentials
	}
	sessions, ok := s.store.(sessionStore)
	if !ok {
		return model.User{}, ErrInvalidCredentials
	}
	session, err := sessions.GetSession(auth.HashAPIKey(token))
	if err != nil || !time.Now().Before(session.ExpiresAt) {
		return model.User{}, ErrInvalidCredentials
	}
	user, err := s.store.GetUserByID(session.UserID)
	if err != nil || user.Status != "active" {
		return model.User{}, ErrInvalidCredentials
	}
	return user, nil
}
func (s *Service) Logout(token string) error {
	sessions, ok := s.store.(sessionStore)
	if !ok {
		return errors.New("session storage unavailable")
	}
	return sessions.DeleteSession(auth.HashAPIKey(token))
}

func (s *Service) SetProfileGuard(guard func(string, string, model.UpdateUserProfileRequest) error) {
	s.profileGuard = guard
}

func (s *Service) SetProfileRequestGuard(g func(*http.Request, string, string) error) {
	s.profileRequestGuard = g
}
func (s *Service) VerifyProfileRequest(r *http.Request, token string) error {
	if s.profileRequestGuard != nil {
		return s.profileRequestGuard(r, "sensitive", token)
	}
	return nil
}
