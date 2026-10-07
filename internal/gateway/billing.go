package gateway

import (
	"api-manager/internal/auth"
	"api-manager/internal/httpx"
	"api-manager/internal/ids"
	"api-manager/internal/model"
	"api-manager/internal/store"
	"context"
	"errors"
	"go.opentelemetry.io/otel/trace"
	"net/http"
	"time"
)

func (g *Gateway) billingUser(r *http.Request, testUser string) string {
	if testUser != "" {
		return testUser
	}
	if c, ok := auth.ValidateAPIKey(g.store, auth.RequestKey(r)); ok {
		return c.OwnerUserID
	}
	return ""
}
func (g *Gateway) reserveCall(r *http.Request, a model.API, id string) (model.Charge, error) {
	if id == "" {
		if a.PriceMicros > 0 {
			return model.Charge{}, auth.ErrForbidden
		}
		return model.Charge{}, nil
	}
	st, ok := g.store.(store.BillingStore)
	if !ok {
		if a.PriceMicros > 0 {
			return model.Charge{}, errors.New("billing storage unavailable")
		}
		return model.Charge{}, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	return st.BeginCharge(ctx, id, a.ID, a.PriceMicros, ids.NewUUID(), time.Now().UTC())
}
func (g *Gateway) finishCall(r *http.Request, a model.API, c model.Charge, userID string, status int, started time.Time) {
	if userID == "" {
		return
	}
	st, ok := g.store.(store.BillingStore)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	success := status >= 200 && status < 300
	if c.ID != "" {
		if err := st.FinishCharge(ctx, c, success); err != nil {
			g.logger.Error("call settlement requires reconciliation", "charge_id", c.ID, "api_id", a.ID)
		}
	}
	if c.ID == "" {
		c.ID = ids.NewUUID()
		c.UserID = userID
	}
	price := int64(0)
	if success {
		price = c.PriceMicros
	}
	credentialID := ""
	if key, ok := auth.ValidateAPIKey(g.store, auth.RequestKey(r)); ok {
		credentialID = key.ID
	}
	if err := st.SaveCallLog(ctx, model.CallLog{TraceID: func() string {
		v := trace.SpanContextFromContext(r.Context())
		if v.IsValid() {
			return v.TraceID().String()
		}
		return ""
	}(), RequestID: httpx.RequestIDFromContext(r.Context()), CredentialID: credentialID, ID: c.ID, UserID: c.UserID, APIID: a.ID, APIName: a.Name, Method: r.Method, Path: r.URL.Path, ClientIP: httpx.Client(r).IP, Status: status, PriceMicros: price, DurationMS: time.Since(started).Milliseconds(), CreatedAt: time.Now().UTC()}); err != nil {
		g.logger.Error("own call log unavailable", "charge_id", c.ID)
	}
}
