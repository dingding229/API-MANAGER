package ratelimit

import "context"

type Health interface {
	Ping(context.Context) error
}
