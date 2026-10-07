package user

import (
	"api-manager/internal/model"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

func (s *Service) TestingScope(u model.User) ([]string, error) {
	developer, admin := u.Role == "api_developer", u.Role == "super_admin"
	for _, role := range u.Roles {
		developer = developer || role == "api_developer"
		admin = admin || role == "super_admin"
	}
	if !developer || admin {
		return nil, nil
	}
	var apis []model.API
	var e error
	if st, ok := s.store.(interface{ ListAPIsChecked() ([]model.API, error) }); ok {
		apis, e = st.ListAPIsChecked()
	} else if st, ok := s.store.(interface{ ListAPIs() []model.API }); ok {
		apis = st.ListAPIs()
	} else {
		return nil, errors.New("API scope unavailable")
	}
	if e != nil {
		return nil, e
	}
	result := []string{}
	for _, a := range apis {
		if a.OwnerUserID == u.ID && a.PublicVisible && a.Enabled && a.PublishedAt != nil {
			digest := sha256.Sum256([]byte(a.Path))
			result = append(result, hex.EncodeToString(digest[:8]))
		}
	}
	return result, nil
}
