// Package storage defines the persistence contract for the chat server.
//
// The interface is deliberately domain-shaped rather than file-shaped: callers
// ask for "the page of messages before seq N", not "read this file". That is
// what lets the JSON backend be replaced without touching the server (see
// design.md decision 4).
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
}

// IsReset reports whether this token is a password-recovery token.
func (t Token) IsReset() bool { return t.Purpose == PurposeReset }

// Conversation is a channel or a direct message. Both live in one ID space so
// message routing never needs a special case for private chats.
type Conversation struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Topic     string `json:"topic,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
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
// Every method must be safe for concurrent use; the JSON implementation achieves
// that with a single writer goroutine (design.md decision 4).
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

	// Tokens
	SaveToken(t Token) error
	TokenByHash(hash string) (Token, error)
	DeleteToken(hash string) error
	DeleteTokensForUser(userID string) error
	// DeleteSessionTokensForUser revokes every session token of a user while
	// leaving reset tokens alone.
	DeleteSessionTokensForUser(userID string) error
	// ListTokensForUser returns a user's tokens, newest first. Used to describe
	// the session being displaced.
	ListTokensForUser(userID string) ([]Token, error)

	// Conversations
	CreateConversation(c Conversation) error
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

	// Close flushes pending writes and releases resources.
	Close() error
}

// Now is the clock used for timestamps, injectable for tests.
var Now = func() int64 { return time.Now().UnixMilli() }
