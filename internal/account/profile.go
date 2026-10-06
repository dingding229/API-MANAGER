package account

import (
	"api-manager/internal/model"
	"api-manager/internal/user"
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Service) profileGuard(actorID, targetID string, request model.UpdateUserProfileRequest) error {
	if s.accounts == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actor, err := s.store.GetUserByID(actorID)
	if err != nil {
		return user.ErrProfileForbidden
	}
	if actorID != targetID {
		if _, err := s.users.VerifyPassword(actor.Username, request.CurrentPassword); err != nil {
			return user.ErrCurrentPassword
		}
	}
	if err := s.checkMFA(ctx, actorID, request.TOTPCode); err != nil {
		return fmt.Errorf("%w: 双重验证失败", user.ErrProfileForbidden)
	}
	target, err := s.store.GetUserByID(targetID)
	if err != nil {
		return err
	}
	if request.Email != nil && strings.ToLower(strings.TrimSpace(*request.Email)) != target.Email {
		v, err := s.verifyCode(ctx, request.VerificationID, "change-email", request.VerificationCode, actorID)
		if err != nil || v.Subject != strings.ToLower(strings.TrimSpace(*request.Email)) {
			return fmt.Errorf("%w: 修改邮箱须验证新邮箱", user.ErrInvalidProfile)
		}
	}
	return nil
}
