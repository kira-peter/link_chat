// Package storage defines the persistence contract for the chat server.
//
// The interface is deliberately domain-shaped rather than file-shaped: callers
// ask for "the page of messages before seq N", not "read this file". That is
// what let the JSON backend be replaced by SQLite without touching the server.
package storage

import (
	"errors"
	"time"
)

// Sentinel errors. The server maps these onto protocol error codes; it never
// sees a file path or an os.PathError.
var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrUnavailable = errors.New("storage unavailable")
)

// User is a persisted account. PasswordHash is a PHC-format Argon2id string;
// the plaintext never reaches this layer.
type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	UsernameKey  string `json:"username_key"` // lowercased, used for uniqueness
	PasswordHash string `json:"password_hash"`
	CreatedAt    int64  `json:"created_at"`

	// SecurityQuestion is stored in clear text because it has to be shown back
	// to the user during recovery. SecurityAnswerHash is Argon2id over the
	// normalised answer and never leaves the server
	// (spec account-auth / 安全问题存储).
	//
	// Both are empty on accounts created before recovery existed.
	SecurityQuestion   string `json:"security_question,omitempty"`
	SecurityAnswerHash string `json:"security_answer_hash,omitempty"`

	// Perms are the account's admin-console permissions; Super marks the
	// super administrator, who holds all of them. Both are absent on ordinary
	// accounts and on every account written before the console existed, which
	// therefore read as having no permissions (spec admin-console / 管理权限位).
	Perms []string `json:"perms,omitempty"`
	Super bool     `json:"super,omitempty"`
}

// CanRecover reports whether the account has the material needed for recovery.
func (u User) CanRecover() bool {
	return u.SecurityQuestion != "" && u.SecurityAnswerHash != ""
}

// Token purposes. A reset token only authorises setting a new password; a
// session token only authorises chat traffic. Keeping both in one collection
// avoids a second persistence path for a nearly identical record
// (design.md decision 1).
const (
	PurposeSession = "session"
	PurposeReset   = "reset"
	// PurposeAdmin authorises the admin console only. It is never accepted on
	// the chat connection, and chat session tokens are never accepted by the
	// console (spec admin-console / 后台会话与聊天会话相互独立).
	PurposeAdmin = "admin"
)

// Token is a credential. Only the hash of the token is stored, so a stolen data
// file does not yield usable sessions.
type Token struct {
	Hash      string `json:"hash"`
	UserID    string `json:"user_id"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	// Purpose is empty on tokens written before recovery existed; empty is
	// treated as PurposeSession.
	Purpose string `json:"purpose,omitempty"`

	// Remote and Device describe where a session was issued, so a displaced
	// login can be told where the new one came from
	// (spec login-session / 被顶掉的提示).
	Remote     string `json:"remote,omitempty"`
	Device     string `json:"device,omitempty"`
	LastSeenAt int64  `json:"last_seen_at,omitempty"`
	// ClientVersion is the x.y.z the client reported when the session was
	// issued; empty for older clients and older tokens.
	ClientVersion string `json:"client_version,omitempty"`
}

// IsReset reports whether this token is a password-recovery token.
func (t Token) IsReset() bool { return t.Purpose == PurposeReset }

// EffectivePurpose is the token's purpose with the legacy empty value read as
// a session.
func (t Token) EffectivePurpose() string {
	if t.Purpose == "" {
		return PurposeSession
	}
	return t.Purpose
}

// Sanction kinds.
const (
	SanctionBan  = "ban"
	SanctionMute = "mute"
)

// Sanction is one ban or mute. Records are never deleted: revoking one stamps
// RevokedBy/RevokedAt, and a newer sanction of the same kind stamps
// SupersededBy on the one it replaces (spec account-moderation / 处分记录).
type Sanction struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	// ExpiresAt is a millisecond timestamp; 0 means permanent.
	ExpiresAt    int64  `json:"expires_at"`
	RevokedBy    string `json:"revoked_by,omitempty"`
	RevokedAt    int64  `json:"revoked_at,omitempty"`
	SupersededBy string `json:"superseded_by,omitempty"`
}

// ActiveAt reports whether the sanction is in force at now. Expiry needs no
// timer: a lapsed sanction simply stops matching (design.md decision 4).
func (s Sanction) ActiveAt(now int64) bool {
	return s.RevokedAt == 0 && s.SupersededBy == "" && (s.ExpiresAt == 0 || now < s.ExpiresAt)
}

// AuditEntry is one administrative action. The log is append-only; the
// storage interface offers no way to change or remove an entry
// (spec account-moderation / 审计日志).
type AuditEntry struct {
	At        int64             `json:"at"`
	ActorID   string            `json:"actor_id"`
	ActorName string            `json:"actor_name"`
	Action    string            `json:"action"`
	Target    string            `json:"target,omitempty"`
	Detail    map[string]string `json:"detail,omitempty"`
}

// AuditFilter narrows ListAudit. Zero fields do not filter. Before pages
// backwards: only entries strictly older than it are returned.
type AuditFilter struct {
	ActorID string
	Action  string
	From    int64
	To      int64
	Before  int64
	Limit   int
}

// Counts summarises the store for the admin overview.
type Counts struct {
	Users    int   `json:"users"`
	Channels int   `json:"channels"`
	DMs      int   `json:"dms"`
	Messages int64 `json:"messages"`
}

// SearchFilter is a cross-conversation message search. Zero fields do not
// filter; Keyword matches case-insensitively as a substring of the body.
type SearchFilter struct {
	Keyword        string
	AuthorID       string
	ConversationID string
	From           int64
	To             int64
	Limit          int
}

// SearchCursor resumes a search. The zero value starts from the newest match;
// otherwise it names the last message of the previous batch, and the next
// batch continues strictly older than it in (CreatedAt, ID) order.
type SearchCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

// SearchResult is one batch. Next is nil when the search is exhausted.
type SearchResult struct {
	Messages []Message
	Next     *SearchCursor
}

// SearchMaxResults caps one batch (spec admin-console / 跨会话搜索).
const SearchMaxResults = 100

// Conversation is a channel or a direct message. Both live in one ID space so
// message routing never needs a special case for private chats.
type Conversation struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Topic      string `json:"topic,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	CreatedBy  string `json:"created_by,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	// MemberIDs is only meaningful for direct messages, where membership is
	// fixed at creation. Channels track members in their own collection.
	MemberIDs []string `json:"member_ids,omitempty"`
}

// Member records that a user belongs to a channel.
type Member struct {
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
	JoinedAt       int64  `json:"joined_at"`
}

// Message is one chat message. Seq is per-conversation and strictly increasing.
// Deleted messages keep their row so seq stays gapless for paging.
type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Seq            int64  `json:"seq"`
	AuthorID       string `json:"author_id"`
	Body           string `json:"body"`
	ClientMsgID    string `json:"client_msg_id,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	EditedAt       int64  `json:"edited_at,omitempty"`
	Deleted        bool   `json:"deleted,omitempty"`
}

// Storage is the whole persistence surface of the server.
//
// Every method must be safe for concurrent use; the SQLite implementation gets
// that from transactions and the database's single-writer lock.
type Storage interface {
	// Users
	CreateUser(u User) error
	UserByUsernameKey(key string) (User, error)
	UserByID(id string) (User, error)
	ListUsers() ([]User, error)
	UpdateUserPasswordHash(userID, hash string) error
	// UpdateUserCredentials updates both the password hash and the recovery
	// material in one write, so a password reset cannot leave a half-updated
	// account behind.
	UpdateUserCredentials(userID, passwordHash, question, answerHash string) error

	// SetUserPerms replaces the user's admin permissions.
	SetUserPerms(userID string, perms []string) error
	// SetUserSuper marks or unmarks the super administrator.
	SetUserSuper(userID string, super bool) error

	// Tokens
	SaveToken(t Token) error
	TokenByHash(hash string) (Token, error)
	DeleteToken(hash string) error
	DeleteTokensForUser(userID string) error
	// DeleteSessionTokensForUser revokes every chat session token of a user
	// while leaving reset and admin tokens alone.
	DeleteSessionTokensForUser(userID string) error
	// DeleteTokensForUserPurpose revokes a user's tokens of one purpose.
	DeleteTokensForUserPurpose(userID, purpose string) error
	// ListTokensForUser returns a user's tokens, newest first. Used to describe
	// the session being displaced.
	ListTokensForUser(userID string) ([]Token, error)

	// Conversations
	// CreateConversation adds a channel and its creator membership atomically.
	CreateConversation(c Conversation) error
	UpdateChannel(id, name, topic, visibility string) error
	ConversationByID(id string) (Conversation, error)
	ChannelByNameKey(key string) (Conversation, error)
	ListConversations() ([]Conversation, error)
	DeleteConversation(id string) error
	// DMBetween returns the direct message conversation shared by two users.
	DMBetween(a, b string) (Conversation, error)

	// Members
	AddMember(m Member) error
	RemoveMember(conversationID, userID string) error
	IsMember(conversationID, userID string) bool
	ListMembers(conversationID string) ([]Member, error)
	MemberCount(conversationID string) int

	// Messages
	// AppendMessage assigns Seq atomically and returns the stored row.
	AppendMessage(m Message) (Message, error)
	MessageByID(id string) (Message, error)
	// MessagesBefore returns up to limit messages with Seq < beforeSeq, ordered
	// by Seq ascending. beforeSeq <= 0 means "the newest page".
	MessagesBefore(conversationID string, beforeSeq int64, limit int) ([]Message, bool, error)
	UpdateMessage(m Message) error
	// LiveMessageCounts counts retained messages since the start of today and
	// since the start of the rolling minute, respectively.
	LiveMessageCounts(today, minute int64) (int64, int64, error)

	// Settings: one record per user, a key to value map (spec
	// user-settings-sync). The store does not know the keys; the server
	// validates them before saving.
	//
	// UserSettings returns a copy of the user's settings and whether any were
	// ever saved.
	UserSettings(userID string) (map[string]string, bool, error)
	// MergeUserSettings applies patch over the user's settings in one atomic
	// step and returns the result.
	MergeUserSettings(userID string, patch map[string]string) (map[string]string, error)

	// Sanctions
	// AddSanction stores s, assigning an ID when empty. A sanction of the same
	// kind still in force for the user is marked superseded in the same step.
	AddSanction(s Sanction) (Sanction, error)
	// RevokeSanction ends a sanction early. Revoking one that is no longer in
	// force is ErrConflict.
	RevokeSanction(id, by string, at int64) (Sanction, error)
	// SanctionsForUser returns every sanction of the user, newest first.
	SanctionsForUser(userID string) ([]Sanction, error)
	ExpiredSanctions(after, through int64) ([]Sanction, error)
	// ActiveSanction returns the sanction of kind in force at now, if any.
	ActiveSanction(userID, kind string, now int64) (Sanction, bool, error)

	// Audit
	AppendAudit(e AuditEntry) error
	// ListAudit returns matching entries, newest first.
	ListAudit(f AuditFilter) ([]AuditEntry, error)

	// Overview and search for the admin console.
	Counts() (Counts, error)
	// SearchMessages returns at most min(f.Limit, SearchMaxResults) matches,
	// newest first across all conversations.
	SearchMessages(f SearchFilter, from SearchCursor) (SearchResult, error)

	// Friends, profiles and moments (see social.go).
	Social

	// Close flushes pending writes and releases resources.
	Close() error
}

// Now is the clock used for timestamps, injectable for tests.
var Now = func() int64 { return time.Now().UnixMilli() }
