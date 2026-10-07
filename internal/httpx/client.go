package httpx

import (
	"api-manager/internal/clientip"
	"net/http"
	"net/netip"
)

type ClientInfo = clientip.ClientInfo

func Client(r *http.Request) ClientInfo { return clientip.Client(r) }
func ClientIdentity(proxies []netip.Prefix, next http.Handler) http.Handler {
	return clientip.ClientIdentity(proxies, next)
}
func PublicAddress(raw string) string { return clientip.PublicAddress(raw) }
