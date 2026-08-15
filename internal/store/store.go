// Package store keeps a realtime mirror of gate-channel membership in
// SQLite, fed by Telegram chat_member update events (see bot.go's
// handleChatMember). It exists so the gate can answer "is this user in the
// channel" from a local table instead of an API call on every message.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite only allows one writer at a time; the driver serializes writes
	// through a single connection so concurrent upserts don't hit SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS members (
		user_id    INTEGER PRIMARY KEY,
		status     TEXT NOT NULL,
		updated_at INTEGER NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create members table: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Upsert records the latest known chat_member status for userID, as reported
// by a Telegram chat_member update.
func (s *Store) Upsert(userID int64, status string, updatedAt int64) error {
	_, err := s.db.Exec(`INSERT INTO members (user_id, status, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET status = excluded.status, updated_at = excluded.updated_at
		WHERE excluded.updated_at >= members.updated_at`, userID, status, updatedAt)
	return err
}

// Status returns the last known membership status for userID and whether any
// row exists at all. No row means the bot has never seen a chat_member event
// for this user (eg. they joined before the bot started listening).
func (s *Store) Status(userID int64) (status string, known bool, err error) {
	err = s.db.QueryRow(`SELECT status FROM members WHERE user_id = ?`, userID).Scan(&status)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return status, true, nil
}
