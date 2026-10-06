package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ClientInfo struct{ IP, PeerIP, Source string }
type clientInfoKey struct{}

func Client(r *http.Request) ClientInfo {
	if info, ok := r.Context().Value(clientInfoKey{}).(ClientInfo); ok {
		return info
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return ClientInfo{Source: "unknown"}
	}
	return ClientInfo{IP: ip.Unmap().String(), PeerIP: ip.Unmap().String(), Source: "peer"}
}
func ClientIdentity(proxies []netip.Prefix, next http.Handler) http.Handler {
	trusted := func(ip netip.Addr) bool {
		for _, p := range proxies {
			if p.Contains(ip.Unmap()) {
				return true
			}
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := Client(r)
		peer, err := netip.ParseAddr(info.PeerIP)
		if err == nil && trusted(peer) && len(r.Header.Values("X-Forwarded-For")) == 1 {
			chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
			if len(chain) <= 16 {
				last := peer
				valid := true
				for i := len(chain) - 1; i >= 0 && trusted(last); i-- {
					ip, e := netip.ParseAddr(strings.TrimSpace(chain[i]))
					if e != nil {
						valid = false
						break
					}
					last = ip.Unmap()
				}
				if valid {
					info.IP = last.String()
					info.Source = "trusted_proxy"
				}
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientInfoKey{}, info)))
	})
}
