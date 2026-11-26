package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	sqlite "modernc.org/sqlite"
)

// DBFile is the database file inside the data directory. WAL mode adds
// DBFile-wal and DBFile-shm next to it.
const DBFile = "linkchat.db"

// Pragmas travel in the DSN because they are per-connection state: a PRAGMA
// run once after opening reaches only one connection of the pool.
const dsnParams = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(1)" +
	"&_pragma=synchronous(NORMAL)" +
	// Writers take the write lock at BEGIN, so a read-then-write transaction
	// never fails to upgrade halfway through.
	"&_txlock=immediate"

// messages.pk is an explicit INTEGER PRIMARY KEY because the full-text index
// refers to rows by rowid, and VACUUM may renumber an implicit rowid.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id                   TEXT PRIMARY KEY,
		username             TEXT NOT NULL,
		username_key         TEXT NOT NULL UNIQUE,
		password_hash        TEXT NOT NULL DEFAULT '',
		created_at           INTEGER NOT NULL DEFAULT 0,
		security_question    TEXT NOT NULL DEFAULT '',
		security_answer_hash TEXT NOT NULL DEFAULT '',
		perms                TEXT NOT NULL DEFAULT '',
		super                INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS tokens (
		hash           TEXT PRIMARY KEY,
		user_id        TEXT NOT NULL,
		created_at     INTEGER NOT NULL DEFAULT 0,
		expires_at     INTEGER NOT NULL DEFAULT 0,
		purpose        TEXT NOT NULL DEFAULT '',
		remote         TEXT NOT NULL DEFAULT '',
		device         TEXT NOT NULL DEFAULT '',
		last_seen_at   INTEGER NOT NULL DEFAULT 0,
		client_version TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS tokens_user ON tokens(user_id)`,
	`CREATE TABLE IF NOT EXISTS conversations (
		id         TEXT PRIMARY KEY,
		kind       TEXT NOT NULL,
		name       TEXT NOT NULL DEFAULT '',
		name_key   TEXT NOT NULL DEFAULT '',
		topic      TEXT NOT NULL DEFAULT '',
		created_by TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		member_ids TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS conversations_channel_name ON conversations(name_key) WHERE kind = 'channel'`,
	`CREATE TABLE IF NOT EXISTS members (
		conversation_id TEXT NOT NULL,
		user_id         TEXT NOT NULL,
		joined_at       INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (conversation_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
		pk              INTEGER PRIMARY KEY,
		id              TEXT NOT NULL UNIQUE,
		conversation_id TEXT NOT NULL,
		seq             INTEGER NOT NULL,
		author_id       TEXT NOT NULL DEFAULT '',
		body            TEXT NOT NULL DEFAULT '',
		client_msg_id   TEXT NOT NULL DEFAULT '',
		created_at      INTEGER NOT NULL DEFAULT 0,
		edited_at       INTEGER NOT NULL DEFAULT 0,
		deleted         INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS messages_conv_seq ON messages(conversation_id, seq)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS messages_idem ON messages(conversation_id, author_id, client_msg_id) WHERE client_msg_id <> ''`,
	`CREATE INDEX IF NOT EXISTS messages_time ON messages(created_at DESC, id DESC)`,
	`CREATE INDEX IF NOT EXISTS messages_author_time ON messages(author_id, created_at DESC)`,
	`CREATE TABLE IF NOT EXISTS conversation_seq (
		conversation_id TEXT PRIMARY KEY,
		next_seq        INTEGER NOT NULL DEFAULT 1
	)`,
	// Trigram index over message bodies. Searches query it with LIKE, which
	// keeps the substring semantics; MATCH would miss two-character Chinese
	// keywords entirely (design.md decision 5).
	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		body, content='messages', content_rowid='pk', tokenize='trigram'
	)`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, body) VALUES (new.pk, new.body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, body) VALUES ('delete', old.pk, old.body);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE OF body ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, body) VALUES ('delete', old.pk, old.body);
		INSERT INTO messages_fts(rowid, body) VALUES (new.pk, new.body);
	END`,
	// settings_owner marks users who saved settings at least once, which an
	// empty merge also counts as.
	`CREATE TABLE IF NOT EXISTS settings_owner (user_id TEXT PRIMARY KEY)`,
	`CREATE TABLE IF NOT EXISTS user_settings (
		user_id TEXT NOT NULL,
		key     TEXT NOT NULL,
		value   TEXT NOT NULL,
		PRIMARY KEY (user_id, key)
	)`,
	`CREATE TABLE IF NOT EXISTS sanctions (
		id            TEXT PRIMARY KEY,
		user_id       TEXT NOT NULL,
		kind          TEXT NOT NULL,
		reason        TEXT NOT NULL DEFAULT '',
		created_by    TEXT NOT NULL DEFAULT '',
		created_at    INTEGER NOT NULL DEFAULT 0,
		expires_at    INTEGER NOT NULL DEFAULT 0,
		revoked_by    TEXT NOT NULL DEFAULT '',
		revoked_at    INTEGER NOT NULL DEFAULT 0,
		superseded_by TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS sanctions_user ON sanctions(user_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS sanctions_expiry ON sanctions(expires_at)
		WHERE revoked_at = 0 AND superseded_by = ''`,
	`CREATE TABLE IF NOT EXISTS audit (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		at         INTEGER NOT NULL,
		actor_id   TEXT NOT NULL DEFAULT '',
		actor_name TEXT NOT NULL DEFAULT '',
		action     TEXT NOT NULL DEFAULT '',
		target     TEXT NOT NULL DEFAULT '',
		detail     TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS audit_at ON audit(at DESC, id DESC)`,
	`CREATE INDEX IF NOT EXISTS audit_actor ON audit(actor_id, at DESC)`,

	`CREATE TABLE IF NOT EXISTS user_profiles (
		user_id    TEXT PRIMARY KEY,
		nickname   TEXT NOT NULL DEFAULT '',
		bio        TEXT NOT NULL DEFAULT '',
		avatar     TEXT NOT NULL DEFAULT '',
		color      TEXT NOT NULL DEFAULT '',
		gender     TEXT NOT NULL DEFAULT '',
		age        INTEGER NOT NULL DEFAULT 0,
		updated_at INTEGER NOT NULL DEFAULT 0
	)`,
	// One row per pair, smaller ID first, so a friendship cannot be one-sided.
	`CREATE TABLE IF NOT EXISTS friendships (
		user_a     TEXT NOT NULL,
		user_b     TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_a, user_b)
	)`,
	`CREATE INDEX IF NOT EXISTS friendships_b ON friendships(user_b)`,
	`CREATE TABLE IF NOT EXISTS friend_requests (
		id         TEXT PRIMARY KEY,
		from_id    TEXT NOT NULL,
		to_id      TEXT NOT NULL,
		note       TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL DEFAULT 0,
		UNIQUE (from_id, to_id)
	)`,
	`CREATE INDEX IF NOT EXISTS friend_requests_to ON friend_requests(to_id, created_at DESC)`,
	`CREATE TABLE IF NOT EXISTS blocks (
		blocker_id TEXT NOT NULL,
		blocked_id TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (blocker_id, blocked_id)
	)`,
	`CREATE TABLE IF NOT EXISTS moments (
		id         TEXT PRIMARY KEY,
		author_id  TEXT NOT NULL,
		body       TEXT NOT NULL,
		visibility TEXT NOT NULL,
		created_at INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS moments_author_time ON moments(author_id, created_at DESC, id DESC)`,
	`CREATE INDEX IF NOT EXISTS moments_time ON moments(created_at DESC, id DESC)`,
	`CREATE TABLE IF NOT EXISTS moment_likes (
		moment_id  TEXT NOT NULL,
		user_id    TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (moment_id, user_id)
	)`,
	`CREATE TABLE IF NOT EXISTS moment_comments (
		id            TEXT PRIMARY KEY,
		moment_id     TEXT NOT NULL,
		author_id     TEXT NOT NULL,
		reply_to      TEXT NOT NULL DEFAULT '',
		reply_to_user TEXT NOT NULL DEFAULT '',
		body          TEXT NOT NULL,
		created_at    INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS moment_comments_moment ON moment_comments(moment_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS moment_notifications (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    TEXT NOT NULL,
		kind       TEXT NOT NULL,
		actor_id   TEXT NOT NULL,
		moment_id  TEXT NOT NULL,
		comment_id TEXT NOT NULL DEFAULT '',
		body       TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		read       INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS moment_notifications_user ON moment_notifications(user_id, id DESC)`,
	`CREATE INDEX IF NOT EXISTS moment_notifications_moment ON moment_notifications(moment_id)`,
	`CREATE TABLE IF NOT EXISTS friend_removals (
		user_id    TEXT NOT NULL,
		by_id      TEXT NOT NULL,
		created_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, by_id)
	)`,
	`CREATE TABLE IF NOT EXISTS moment_seen (
		user_id TEXT PRIMARY KEY,
		seen_at INTEGER NOT NULL
	)`,
}

// sqlStore is the SQLite implementation of Storage. Concurrency comes from the
// database: readers run in parallel under WAL, writers queue on SQLite's write
// lock (design.md decision 2).
type sqlStore struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open opens (or creates) the store in dir.
func Open(dir string) (Storage, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("storage: create data dir: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, DBFile)+dsnParams)
	if err != nil {
		return nil, fmt.Errorf("storage: open database: %w", err)
	}
	db.SetMaxOpenConns(8)
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("storage: create schema: %w", err)
		}
	}
	// Columns added after a table first shipped. A data directory made before
	// them lacks them; adding one that exists fails harmlessly.
	for _, stmt := range []string{
		`ALTER TABLE user_profiles ADD COLUMN gender TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE user_profiles ADD COLUMN age INTEGER NOT NULL DEFAULT 0`,
	} {
		_, _ = db.Exec(stmt)
	}
	return &sqlStore{db: db}, nil
}

func (s *sqlStore) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.db.Close() })
	return s.closeErr
}

// withTx runs fn in one write transaction and commits it, rolling back when fn
// fails. Errors come back already mapped.
func (s *sqlStore) withTx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return mapErr(err)
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return mapErr(err)
	}
	return mapErr(tx.Commit())
}

// SQLite result codes used by mapErr. Extended codes carry the primary code in
// their low byte.
const (
	sqliteBusy              = 5
	sqliteLocked            = 6
	sqliteReadOnly          = 8
	sqliteIOErr             = 10
	sqliteFull              = 13
	sqliteCantOpen          = 14
	sqliteConstraintPK      = 1555
	sqliteConstraintUnique  = 2067
	errDatabaseClosedSuffix = "database is closed"
)

// mapErr turns database errors into the sentinels the server maps onto
// protocol codes, so no SQL detail crosses the interface
// (spec server-runtime / 存储错误被统一映射).
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) || errors.Is(err, ErrUnavailable) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch code := se.Code(); code {
		case sqliteConstraintPK, sqliteConstraintUnique:
			return ErrConflict
		default:
			switch code & 0xff {
			case sqliteBusy, sqliteLocked, sqliteReadOnly, sqliteIOErr, sqliteFull, sqliteCantOpen:
				return fmt.Errorf("%w: %v", ErrUnavailable, err)
			}
		}
		return err
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) || strings.HasSuffix(err.Error(), errDatabaseClosedSuffix) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return err
}

// rowScanner is the part of *sql.Row and *sql.Rows the scan helpers need.
type rowScanner interface {
	Scan(dest ...any) error
}

// encodeList stores a small string list as JSON; an empty list is "".
func encodeList(list []string) string {
	if len(list) == 0 {
		return ""
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func decodeList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(s), &out) != nil || len(out) == 0 {
		return nil
	}
	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nameKey is the case-insensitive key used for username and channel uniqueness.
func nameKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// newID returns a random, non-enumerable identifier. Random rather than
// sequential so IDs do not disclose ordering or volume
// (spec message-delivery / 消息标识与幂等).
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("storage: entropy source failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
