package store

import (
	"context"
	"time"
)

func (p *Postgres) UpdatePersonalBasics(ctx context.Context, id, nickname, timeZone string) error {
	result, e := p.pool.Exec(ctx, `UPDATE users SET nickname=$2,time_zone=$3,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id, nickname, timeZone)
	if e == nil && result.RowsAffected() != 1 {
		return ErrNotFound
	}
	return e
}
func (p *Postgres) PersonalTimeZone(id string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	var zone string
	e := p.pool.QueryRow(ctx, `SELECT time_zone FROM users WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&zone)
	return zone, e
}
