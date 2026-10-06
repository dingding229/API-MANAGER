// Package audit records security-relevant management actions without retaining secrets.
package audit

import (
	"context"
	"errors"
	"log/slog"
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
	if err := s.RecordChecked(ctx, actor, r, action, resourceType, resourceID, statusCode, details); err != nil && s.logger != nil {
		s.logger.Error("write audit log failed", "action", action, "resource_type", resourceType, "resource_id", resourceID, "error", err)
	}
}

// RecordChecked is used by destructive operations that must not proceed when
// their audit intent cannot be accepted by the configured store.
func (s *Service) RecordChecked(_ context.Context, actor Actor, r *http.Request, action, resourceType, resourceID string, statusCode int, details map[string]any) error {
	if s == nil || s.store == nil {
		return errors.New("audit storage unavailable")
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
		agent := r.UserAgent()
		if len(agent) > 512 {
			agent = agent[:512]
		}
		log.Method, log.Path, log.UserAgent = r.Method, r.URL.Path, agent
		log.RemoteAddr = httpx.Client(r).IP
	}
	return s.store.CreateAuditLog(log)
}

func (s *Service) List(query model.AuditLogQuery) (model.AuditLogPage, error) {
	if s == nil || s.store == nil {
		return model.AuditLogPage{}, nil
	}
	return s.store.ListAuditLogs(query)
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
