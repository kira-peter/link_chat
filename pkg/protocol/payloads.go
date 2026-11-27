package protocol

// Operation names carried in ReqPayload.Op and ResPayload-related contexts.
const (
	OpAuthRegister = "auth.register"
	OpAuthLogin    = "auth.login"
	OpAuthLogout   = "auth.logout"
	OpAuthResume   = "auth.resume"

	// Password recovery is a three-step wizard: ask for the question, answer
	// it, then set a new password. Splitting it keeps each failure precise
	// (design.md decision 3).
	OpRecoverQuestion = "auth.recover.question"
	OpRecoverVerify   = "auth.recover.verify"
	OpRecoverReset    = "auth.recover.reset"

	OpChannelList          = "channel.list"
	OpChannelCreate        = "channel.create"
	OpChannelCreatePrivate = "channel.create_private"
	OpChannelJoin          = "channel.join"
	OpChannelLeave         = "channel.leave"
	OpChannelDelete        = "channel.delete"

	OpDMOpen   = "dm.open"
	OpUserList = "user.list"

	OpMessageSend    = "message.send"
	OpMessageEdit    = "message.edit"
	OpMessageDelete  = "message.delete"
	OpMessageHistory = "message.history"

	OpPresencePing = "presence.ping"
)

// Event names carried in EventPayload.Name.
const (
	EvMessage        = "message"
	EvMessageEdit    = "message.edited"
	EvMessageDel     = "message.deleted"
	EvPresence       = "presence"
	EvChannelCreated = "channel.created"
	EvChannelGone    = "channel.gone"
	EvShutdown       = "server.shutdown"
	// EvSessionReplaced tells a connection it lost the account to a newer
	// login. It is sent before the connection is closed, and carries where the
	// new session came from (spec login-session / 被顶掉的提示).
	EvSessionReplaced = "session.replaced"
	// Sanction events (spec chat-protocol / 处分相关的事件). account.banned is
	// written before the connection is closed.
	EvAccountBanned  = "account.banned"
	EvAccountMuted   = "account.muted"
	EvAccountUnmuted = "account.unmuted"
)

// SanctionInfo describes a ban or mute to the person it applies to. ExpiresAt
// is a millisecond timestamp; 0 means permanent. Reason is empty for a mute
// error, which only needs to say until when.
type SanctionInfo struct {
	Reason    string `json:"reason,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
}

// Conversation kinds share one ID space for message routing.
const (
	KindChannel = "channel"
	KindDM      = "dm"
)

const (
	ChannelPublic        = "public"
	ChannelPrivate       = "private"
	ChannelTopicMaxRunes = 120
)

// ---------- auth ----------

type RegisterReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// SecurityQuestion and SecurityAnswer enable password recovery. They are
	// optional at the type level so an older client can still register, but the
	// server rejects a registration that omits them.
	SecurityQuestion string `json:"security_question,omitempty"`
	SecurityAnswer   string `json:"security_answer,omitempty"`
}

type LoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Device is an optional human label for this client instance, shown to
	// whoever gets replaced so they can tell which machine took over.
	Device string `json:"device,omitempty"`
}

// AuthRes is returned by register and login.
type AuthRes struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	User      User   `json:"user"`
	Device    string `json:"device,omitempty"`
}

// ResumeReq re-authenticates an existing connection with a stored token, which
// is how a reconnecting client avoids asking for a password again.
type ResumeReq struct {
	Token string `json:"token"`
	// Device is an optional label for this client instance, shown to whoever
	// gets displaced by the resume.
	Device string `json:"device,omitempty"`
}

// ---------- password recovery ----------

// RecoverQuestionReq starts the wizard.
type RecoverQuestionReq struct {
	Username string `json:"username"`
}

// RecoverQuestionRes carries the account's question. An account that cannot be
// recovered is reported as an error, not as an empty question, so a caller
// cannot distinguish "no such user" from "no question set"
// (design.md decision 4).
type RecoverQuestionRes struct {
	Username string `json:"username"`
	Question string `json:"question"`
}

// RecoverVerifyReq answers the question.
type RecoverVerifyReq struct {
	Username string `json:"username"`
	Answer   string `json:"answer"`
}

// RecoverVerifyRes hands back a short-lived, single-use token for step three.
type RecoverVerifyRes struct {
	ResetToken string `json:"reset_token"`
	ExpiresAt  int64  `json:"expires_at"`
}

// RecoverResetReq sets the new password.
type RecoverResetReq struct {
	ResetToken  string `json:"reset_token"`
	NewPassword string `json:"new_password"`
}

// ---------- users ----------

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Online   bool   `json:"online"`
	// Nickname, Avatar and Color are the public profile (spec user-profile).
	// The server always fills them, with defaults when never set.
	Nickname string `json:"nickname,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Color    string `json:"color,omitempty"`
	// Bio, Gender and Age are shown in the plaza and on the profile. Gender
	// is "male", "female" or "" (not shown); Age 0 is not shown.
	Bio    string `json:"bio,omitempty"`
	Gender string `json:"gender,omitempty"`
	Age    int    `json:"age,omitempty"`
}

// DisplayName is the nickname, or the username when there is none.
func (u User) DisplayName() string {
	if u.Nickname != "" {
		return u.Nickname
	}
	return u.Username
}

type UserListRes struct {
	Users []User `json:"users"`
}

// ---------- conversations ----------

type Conversation struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Name          string `json:"name"`
	Topic         string `json:"topic,omitempty"`
	Visibility    string `json:"visibility,omitempty"`
	CreatedBy     string `json:"created_by,omitempty"`
	MemberCount   int    `json:"member_count"`
	LastMessageAt int64  `json:"last_message_at"`
	LastMessage   string `json:"last_message,omitempty"`
	Joined        bool   `json:"joined"`
}

type ChannelListRes struct {
	Conversations []Conversation `json:"conversations"`
}

type ChannelCreateReq struct {
	Name       string `json:"name"`
	Topic      string `json:"topic,omitempty"`
	Visibility string `json:"visibility,omitempty"`
}

type ConversationRef struct {
	ConversationID string `json:"conversation_id"`
}

type DMOpenReq struct {
	Username string `json:"username"`
}

// ---------- messages ----------

type Message struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	Seq            int64  `json:"seq"`
	AuthorID       string `json:"author_id"`
	AuthorName     string `json:"author_name"`
	AuthorAvatar   string `json:"author_avatar,omitempty"`
	AuthorColor    string `json:"author_color,omitempty"`
	Body           string `json:"body"`
	CreatedAt      int64  `json:"created_at"`
	EditedAt       int64  `json:"edited_at,omitempty"`
	Deleted        bool   `json:"deleted,omitempty"`
	// ClientMsgID is echoed from SendMessageReq so the sender can replace its
	// optimistic local copy without guessing which message is which.
	ClientMsgID string `json:"client_msg_id,omitempty"`
}

type SendMessageReq struct {
	ConversationID string `json:"conversation_id"`
	ClientMsgID    string `json:"client_msg_id,omitempty"`
	Body           string `json:"body"`
}

type EditMessageReq struct {
	MessageID string `json:"message_id"`
	Body      string `json:"body"`
}

type DeleteMessageReq struct {
	MessageID string `json:"message_id"`
}

// HistoryReq asks for messages earlier than Before (or the newest page when
// Before is zero). Using seq rather than a timestamp keeps paging exact.
type HistoryReq struct {
	ConversationID string `json:"conversation_id"`
	BeforeSeq      int64  `json:"before_seq,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

type HistoryRes struct {
	Messages []Message `json:"messages"`
	HasMore  bool      `json:"has_more"`
}

// ---------- events ----------

type MessageEvent struct {
	Message Message `json:"message"`
}

type PresenceEvent struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Online   bool   `json:"online"`
}

type ChannelGoneEvent struct {
	ConversationID string `json:"conversation_id"`
}

type ShutdownEvent struct {
	Reason string `json:"reason"`
}

// SessionReplacedEvent is pushed to the connection that lost its account.
//
// Location is the raw source address of the winning login, optionally decorated
// with a coarse geographic hint when the server can resolve one. It is only ever
// shown to the account's own owner (design.md decision 4).
type SessionReplacedEvent struct {
	Username string `json:"username"`
	Location string `json:"location"`
	At       int64  `json:"at"`
	Device   string `json:"device,omitempty"`
}

// PingReq / PongRes back the heartbeat.
type PingReq struct {
	Nonce int64 `json:"nonce,omitempty"`
	// Omitted by older clients. An empty value explicitly clears the view.
	ViewedChannel *string `json:"viewed_channel,omitempty"`
}

type PongRes struct {
	Nonce int64 `json:"nonce,omitempty"`
	Now   int64 `json:"now"`
}
