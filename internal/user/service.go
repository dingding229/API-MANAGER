package user

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"api-manager/internal/audit"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Store interface {
	CreateUser(model.User) error
	GetUserByEmail(string) (model.User, error)
	GetUserByID(string) (model.User, error)
	ListUsers() []model.User
	CountUsers() int
	UpdateUserStatus(string, string) error
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
	store  Store
	secret []byte
	ttl    time.Duration
}

func NewService(store Store, secret string, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Service{store: store, secret: []byte(secret), ttl: ttl}
}

func (s *Service) EnsureDefaults() error { return s.store.EnsureRBAC() }
func (s *Service) Enabled() bool         { return len(s.secret) > 0 }
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

func (s *Service) Create(email, password, role string) (model.User, error) {
	return s.CreateWithRoles(email, password, []string{role})
}

func (s *Service) CreateWithRoles(email, password string, roles []string) (model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !strings.Contains(email, "@") || len(password) < 8 {
		return model.User{}, errors.New("valid email and password with at least 8 characters are required")
	}
	roles = normalizeRoles(roles)
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}
	for _, role := range roles {
		if _, err := s.store.GetRoleByName(role); err != nil {
			return model.User{}, fmt.Errorf("unknown role %q", role)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, fmt.Errorf("hash password: %w", err)
	}
	now := time.Now().UTC()
	newUser := model.User{ID: ids.NewUUID(), Email: email, PasswordHash: string(hash), Role: roles[0], Roles: roles, Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateUser(newUser); err != nil {
		return model.User{}, err
	}
	return newUser, nil
}

func (s *Service) Authenticate(email, password string) (model.User, string, error) {
	if !s.Enabled() {
		return model.User{}, "", errors.New("user authentication is disabled")
	}
	user, err := s.store.GetUserByEmail(strings.ToLower(strings.TrimSpace(email)))
	if err != nil || user.Status != "active" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return model.User{}, "", ErrInvalidCredentials
	}
	now := time.Now()
	claims := jwt.MapClaims{"sub": user.ID, "email": user.Email, "roles": user.Roles, "iat": now.Unix(), "exp": now.Add(s.ttl).Unix()}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return model.User{}, "", fmt.Errorf("sign login token: %w", err)
	}
	return user, signed, nil
}

func (s *Service) ValidateToken(tokenString string) (model.User, error) {
	if !s.Enabled() {
		return model.User{}, ErrInvalidCredentials
	}
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	}, jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return model.User{}, ErrInvalidCredentials
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return model.User{}, ErrInvalidCredentials
	}
	email, _ := claims["email"].(string)
	issued, err := claims.GetIssuedAt()
	if err != nil || issued == nil {
		return model.User{}, ErrInvalidCredentials
	}
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil || expires.Time.Sub(issued.Time) > 24*time.Hour || expires.Time.Before(issued.Time) {
		return model.User{}, ErrInvalidCredentials
	}
	user, err := s.store.GetUserByEmail(email)
	if err != nil || user.Status != "active" || claims["sub"] != user.ID {
		return model.User{}, ErrInvalidCredentials
	}
	return user, nil
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
	return map[string]any{"user": user, "permissions": s.store.GetUserPermissions(user.ID)}
}

func (s *Service) ListUsers() []model.User                 { return s.store.ListUsers() }
func (s *Service) ListRoles() []model.Role                 { return s.store.ListRoles() }
func (s *Service) ListPermissions() []model.Permission     { return s.store.ListPermissions() }
func (s *Service) GetRole(name string) (model.Role, error) { return s.store.GetRoleByName(name) }
func (s *Service) UpdateRolePermissions(name string, codes []string) error {
	return s.store.UpdateRolePermissions(name, codes)
}

func (s *Service) CreateRole(name, description string, permissions []string) (model.Role, error) {
	role := model.Role{ID: ids.NewUUID(), Name: strings.ToLower(strings.TrimSpace(name)), Description: strings.TrimSpace(description), Permissions: normalizeRoles(permissions)}
	if role.Name == "" {
		return model.Role{}, errors.New("role name is required")
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
