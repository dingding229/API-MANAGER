package model

import "time"

type APITestTicket struct {
	Hash, SessionHash, APIID, Digest, ClientIP, UserAgent string
	ExpiresAt                                             time.Time
}
