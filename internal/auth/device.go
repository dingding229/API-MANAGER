package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

const DeviceCookie = "api_manager_device"

type newDeviceKey struct{}

func AssignDevice(r *http.Request) (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	value := hex.EncodeToString(b)
	if r != nil {
		*r = *r.WithContext(context.WithValue(r.Context(), newDeviceKey{}, value))
	}
	return HashAPIKey(value), nil
}
func SetDeviceCookie(w http.ResponseWriter, r *http.Request, production bool) {
	value, _ := r.Context().Value(newDeviceKey{}).(string)
	if value == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: DeviceCookie, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
func DeviceMatches(r *http.Request, hash string) bool {
	cookies := r.CookiesNamed(DeviceCookie)
	if hash == "" || len(cookies) != 1 || len(cookies[0].Value) != 64 {
		return false
	}
	actual := HashAPIKey(cookies[0].Value)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(hash)) == 1
}
func NewDeviceToken(r *http.Request) string {
	value, _ := r.Context().Value(newDeviceKey{}).(string)
	return value
}
