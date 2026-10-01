package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"api-manager/internal/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (m *Memory) UpdateUserProfile(id string, change model.UserProfileUpdate) (model.User, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[id]
	if !ok {
		return model.User{}, false, ErrNotFound
	}
	if u.Username != change.ExpectedUsername || u.Email != change.ExpectedEmail || u.PasswordHash != change.ExpectedPasswordHash {
		return model.User{}, false, ErrConflict
	}
	for otherID, other := range m.users {
		if otherID != id && (strings.EqualFold(other.Username, change.Username) || (change.Email != "" && strings.EqualFold(other.Email, change.Email))) {
			return model.User{}, false, ErrConflict
		}
	}
	changed := u.Username != change.Username || u.Email != change.Email || u.PasswordHash != change.PasswordHash
	if changed {
		u.Username, u.Email, u.PasswordHash, u.UpdatedAt = change.Username, change.Email, change.PasswordHash, time.Now().UTC()
		m.users[id] = u
		for key, reset := range m.resets {
			if reset.UserID == id {
				delete(m.resets, key)
			}
		}
		for key, session := range m.sessions {
			if session.UserID == id {
				delete(m.sessions, key)
			}
		}
	}
	u.Roles = append([]string(nil), m.userRoles[id]...)
	return u, changed, nil
}

func (p *Postgres) UpdateUserProfile(id string, change model.UserProfileUpdate) (model.User, bool, error) {
	ctx, cancel := dbContext()
	defer cancel()
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return model.User{}, false, err
	}
	defer tx.Rollback(ctx)
	var u model.User
	err = tx.QueryRow(ctx, `SELECT id,username,COALESCE(email,''),password_hash,role,status,created_at,updated_at FROM users WHERE id=$1 FOR UPDATE`, id).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.Role, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, false, ErrNotFound
	}
	if err != nil {
		return model.User{}, false, err
	}
	if u.Username != change.ExpectedUsername || u.Email != change.ExpectedEmail || u.PasswordHash != change.ExpectedPasswordHash {
		return model.User{}, false, ErrConflict
	}
	changed := u.Username != change.Username || u.Email != change.Email || u.PasswordHash != change.PasswordHash
	if changed {
		err = tx.QueryRow(ctx, `UPDATE users SET username=$2,email=NULLIF($3,''),password_hash=$4,updated_at=NOW() WHERE id=$1 RETURNING updated_at`, id, change.Username, change.Email, change.PasswordHash).Scan(&u.UpdatedAt)
		if err != nil {
			var pgError *pgconn.PgError
			if errors.As(err, &pgError) && pgError.Code == "23505" {
				return model.User{}, false, ErrConflict
			}
			return model.User{}, false, fmt.Errorf("update user profile: %w", err)
		}
		if _, err = tx.Exec(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, id); err != nil {
			return model.User{}, false, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM password_resets WHERE user_id=$1`, id); err != nil {
			return model.User{}, false, err
		}
		u.Username, u.Email, u.PasswordHash = change.Username, change.Email, change.PasswordHash
	}
	rows, err := tx.Query(ctx, `SELECT r.name FROM roles r JOIN user_roles ur ON ur.role_id=r.id WHERE ur.user_id=$1 ORDER BY r.name`, id)
	if err != nil {
		return model.User{}, false, err
	}
	u.Roles = []string{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return model.User{}, false, err
		}
		u.Roles = append(u.Roles, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return model.User{}, false, err
	}
	if len(u.Roles) > 0 {
		u.Role = u.Roles[0]
	}
	if err = tx.Commit(ctx); err != nil {
		return model.User{}, false, err
	}
	return u, changed, nil
}
