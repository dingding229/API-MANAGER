package auth

import "context"

type passkeyConfirmationKey struct{}

// WithPasskeyConfirmation is set only after the server has consumed a valid,
// session-, operation- and body-bound assertion. JSON cannot set this context.
func WithPasskeyConfirmation(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, passkeyConfirmationKey{}, userID)
}
func PasskeyConfirmed(ctx context.Context, userID string) bool {
	id, _ := ctx.Value(passkeyConfirmationKey{}).(string)
	return userID != "" && id == userID
}
