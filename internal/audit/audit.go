// Package audit records security-relevant management actions without retaining secrets.
package audit

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"api-manager/internal/httpx"
	"api-manager/internal/model"
)

type Store interface {
	CreateAuditLog(model.AuditLog) error
	ListAuditLogs(model.AuditLogQuery) (model.AuditLogPage, error)
}

type Actor struct {
	ID    string
	Type  string
	Email string
}

type Service struct {
	store  Store
	logger *slog.Logger
}

func New(store Store, logger *slog.Logger) *Service { return &Service{store: store, logger: logger} }

func (s *Service) Record(ctx context.Context, actor Actor, r *http.Request, action, resourceType, resourceID string, statusCode int, details map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	if actor.Type == "" {
		actor.Type = "system"
	}
	log := model.AuditLog{
		ActorID: actor.ID, ActorType: actor.Type, ActorEmail: actor.Email,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		StatusCode: statusCode, Details: redact(details), CreatedAt: time.Now().UTC(),
	}
	if r != nil {
		log.RequestID = httpx.RequestIDFromContext(r.Context())
		log.Method, log.Path, log.UserAgent = r.Method, r.URL.Path, r.UserAgent()
		log.RemoteAddr = remoteIP(r.RemoteAddr)
	}
	if err := s.store.CreateAuditLog(log); err != nil && s.logger != nil {
		s.logger.Error("write audit log failed", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

func (s *Service) List(query model.AuditLogQuery) (model.AuditLogPage, error) {
	if s == nil || s.store == nil {
		return model.AuditLogPage{}, nil
	}
	return s.store.ListAuditLogs(query)
}

func remoteIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		return host
	}
	return addr
}

func redact(details map[string]any) map[string]any {
	clean := make(map[string]any, len(details))
	for key, value := range details {
		if sensitiveKey(key) {
			clean[key] = "[REDACTED]"
			continue
		}
		clean[key] = redactValue(value)
	}
	return clean
}

func redactValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		return redact(item)
	case []any:
		result := make([]any, len(item))
		for i, entry := range item {
			result[i] = redactValue(entry)
		}
		return result
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token") || strings.Contains(key, "api_key") || strings.Contains(key, "apikey") || strings.Contains(key, "authorization") || strings.Contains(key, "credential") || strings.Contains(key, "hash")
}
