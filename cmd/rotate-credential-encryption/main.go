// rotate-credential-encryption rewraps recoverable API keys atomically.
// Run with the gateway stopped, after a verified database backup.
package main

import (
	"api-manager/internal/auth"
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"os"
)

func main() {
	oldKey := os.Getenv("OLD_CREDENTIAL_ENCRYPTION_KEY")
	newKey := os.Getenv("CREDENTIAL_ENCRYPTION_KEY")
	dsn := os.Getenv("POSTGRES_DSN")
	if len(oldKey) < 16 || len(newKey) < 32 || oldKey == newKey || dsn == "" {
		log.Fatal("set POSTGRES_DSN, OLD_CREDENTIAL_ENCRYPTION_KEY, and a distinct CREDENTIAL_ENCRYPTION_KEY")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal("credential migration failed; inspect database connectivity and backup")
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Fatal("credential migration failed; inspect database connectivity and backup")
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT id, key_hash, encrypted_key FROM api_credentials WHERE encrypted_key <> '' FOR UPDATE")
	if err != nil {
		log.Fatal("credential migration failed; inspect database connectivity and backup")
	}
	type item struct{ id, hash, encrypted string }
	items := []item{}
	for rows.Next() {
		var row item
		if err := rows.Scan(&row.id, &row.hash, &row.encrypted); err != nil {
			rows.Close()
			log.Fatal("credential migration failed; inspect database connectivity and backup")
		}
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		log.Fatal("credential migration failed; inspect database connectivity and backup")
	}
	rows.Close()
	for _, row := range items {
		plaintext, err := auth.DecryptSecret(oldKey, row.encrypted)
		if err != nil || auth.HashAPIKey(plaintext) != row.hash {
			log.Fatal(errors.New("old key cannot decrypt all credentials; transaction rolled back"))
		}
		sealed, err := auth.EncryptSecret(newKey, plaintext)
		if err != nil {
			log.Fatal("credential migration failed; inspect database connectivity and backup")
		}
		if _, err := tx.Exec(ctx, "UPDATE api_credentials SET encrypted_key=$2 WHERE id=$1", row.id, sealed); err != nil {
			log.Fatal("credential migration failed; inspect database connectivity and backup")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		log.Fatal("credential migration failed; inspect database connectivity and backup")
	}
	fmt.Printf("Rewrapped %d credentials. Remove the old secret from your environment.\n", len(items))
}
