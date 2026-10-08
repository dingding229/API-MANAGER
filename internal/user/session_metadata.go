package user

import (
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/model"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

func cleanAgent(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	if len(value) > 512 {
		value = value[:512]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
func deviceLabel(agent string) string {
	browser := "浏览器"
	switch {
	case strings.Contains(agent, "Edg/"):
		browser = "Edge"
	case strings.Contains(agent, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(agent, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(agent, "Safari/"):
		browser = "Safari"
	case strings.Contains(agent, "curl/"):
		browser = "命令行客户端"
	case agent == "":
		return "未知设备"
	}
	system := "其他设备"
	switch {
	case strings.Contains(agent, "Android"):
		system = "Android"
	case strings.Contains(agent, "iPhone") || strings.Contains(agent, "iPad"):
		system = "iOS"
	case strings.Contains(agent, "Windows"):
		system = "Windows"
	case strings.Contains(agent, "Macintosh"):
		system = "macOS"
	case strings.Contains(agent, "Linux"):
		system = "Linux"
	}
	return browser + " / " + system
}
func (s *Service) AuthenticateRequest(name, password string, r *http.Request) (model.User, string, error) {
	return s.authenticate(name, password, r)
}
func (s *Service) CurrentSession(token string) (model.Session, error) {
	if _, err := s.ValidateSession(token); err != nil {
		return model.Session{}, err
	}
	return s.store.(sessionStore).GetSession(auth.HashAPIKey(token))
}

type sessionManagerStore interface {
	ListUserSessions(string, time.Time) ([]model.Session, error)
	DeleteUserSession(string, string) (bool, error)
	TouchSession(string, time.Time, string) error
}

func (s *Service) Sessions(id string) ([]model.Session, error) {
	st, ok := s.store.(sessionManagerStore)
	if !ok {
		return nil, ErrInvalidCredentials
	}
	return st.ListUserSessions(id, time.Now())
}
func (s *Service) RevokeSession(userID, id string) (bool, error) {
	st, ok := s.store.(sessionManagerStore)
	if !ok {
		return false, ErrInvalidCredentials
	}
	return st.DeleteUserSession(userID, id)
}
func (s *Service) TouchSession(token string, r *http.Request) {
	if httpx.Client(r).IP == "" {
		return
	}
	if st, ok := s.store.(sessionManagerStore); ok {
		if err := st.TouchSession(auth.HashAPIKey(token), time.Now().UTC(), httpx.PublicAddress(httpx.Client(r).IP)); err != nil {
			slog.Warn("session activity update failed")
		}
	}
}

func NormalizedAgent(value string) string { return cleanAgent(value) }
func SameBrowserDevice(a, b string) bool {
	return deviceLabel(cleanAgent(a)) == deviceLabel(cleanAgent(b))
}
