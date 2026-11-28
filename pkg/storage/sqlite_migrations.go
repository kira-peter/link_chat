package storage

import (
	"database/sql"
	"errors"
	"fmt"
)

// Entries are applied in order. user_version is the number of migrations
// committed, including the first migration from an unversioned database.
var sqliteMigrations = []func(*sql.Tx) error{bootstrapSchema, addSimulationAccounts, indexMembersByUser}

func indexMembersByUser(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS members_by_user ON members(user_id, conversation_id)`)
	return err
}

func addSimulationAccounts(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE simulation_accounts (
		user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
		run_id TEXT NOT NULL
	)`)
	return err
}

func (s *sqlStore) migrate() error {
	return s.withTx(func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
			return fmt.Errorf("storage: read SQLite schema version: %w", err)
		}
		if version < 0 || version > len(sqliteMigrations) {
			return fmt.Errorf("storage: unsupported SQLite schema version %d, supported through %d", version, len(sqliteMigrations))
		}
		if version == len(sqliteMigrations) {
			return nil
		}
		for version < len(sqliteMigrations) {
			if err := sqliteMigrations[version](tx); err != nil {
				return fmt.Errorf("storage: migrate SQLite schema to version %d: %w", version+1, err)
			}
			version++
		}
		_, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version))
		return err
	})
}

// bootstrapSchema handles both a new database and the pre-versioned SQLite
// releases, some of which predate the profile and channel columns.
func bootstrapSchema(tx *sql.Tx) error {
	for _, stmt := range schema {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
	}
	for _, column := range []struct {
		table, name, definition string
	}{
		{"user_profiles", "gender", "TEXT NOT NULL DEFAULT ''"},
		{"user_profiles", "age", "INTEGER NOT NULL DEFAULT 0"},
		{"conversations", "visibility", "TEXT NOT NULL DEFAULT 'public'"},
	} {
		var present int
		switch err := tx.QueryRow(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, column.table, column.name).Scan(&present); {
		case err == nil:
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("inspect %s.%s: %w", column.table, column.name, err)
		}
		if _, err := tx.Exec("ALTER TABLE " + column.table + " ADD COLUMN " + column.name + " " + column.definition); err != nil {
			return fmt.Errorf("add %s.%s: %w", column.table, column.name, err)
		}
	}
	// The cursor index includes the old index's key columns.
	_, err := tx.Exec(`DROP INDEX IF EXISTS moment_comments_moment`)
	return err
}
