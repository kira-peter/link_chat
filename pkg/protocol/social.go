package protocol

import (
	"hash/fnv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// Friends, profiles and moments (specs friends, user-profile, moments).

const (
	OpFriendRequest = "friend.request"
	OpFriendRespond = "friend.respond"
	OpFriendList    = "friend.list"
	OpFriendRemove  = "friend.remove"
	OpFriendBlock   = "friend.block"
	OpFriendUnblock = "friend.unblock"

	OpProfileGet    = "profile.get"
	OpProfileUpdate = "profile.update"

	OpMomentPost          = "moment.post"
	OpMomentTimeline      = "moment.timeline"
	OpMomentDelete        = "moment.delete"
	OpMomentLike          = "moment.like"
	OpMomentComment       = "moment.comment"
	OpMomentComments      = "moment.comments"
	OpMomentCommentDelete = "moment.comment.delete"
	OpMomentNotifications = "moment.notifications"
	OpMomentNotifRead     = "moment.notifications.read"
	OpMomentUnread        = "moment.unread"
)

const (
	EvFriendRequested = "friend.requested"
	EvFriendAdded     = "friend.added"
	EvFriendRemoved   = "friend.removed"
	EvProfileUpdated  = "profile.updated"
	EvMomentNew       = "moment.new"
	EvMomentUpdated   = "moment.updated"
	EvMomentDeleted   = "moment.deleted"
	EvMomentNotify    = "moment.notify"
)

// Limits on profile and moment text, in characters.
const (
	MaxNicknameLen     = 24
	MaxBioLen          = 60
	MaxFriendNoteLen   = 100
	MaxMomentLen       = 500
	MaxCommentLen      = 200
	TimelinePageSize   = 20
	ProfileMomentsPage = 20
)

// Moment visibility.
const (
	VisibilityFriends = "friends"
	VisibilityPrivate = "private"
)

// Notification kinds.
const (
	NotifyLike    = "like"
	NotifyComment = "comment"
	NotifyReply   = "reply"
)

// AvatarColors are the colours an avatar may take. They are the named message
// colours, which read on both themes.
var AvatarColors = []string{"cyan", "blue", "green", "amber", "rose", "violet"}

// ValidAvatarColor reports whether c is one of AvatarColors.
func ValidAvatarColor(c string) bool {
	for _, x := range AvatarColors {
		if x == c {
			return true
		}
	}
	return false
}

// avatarWidth measures with the same rules the terminal client uses.
var avatarWidth = &runewidth.Condition{StrictEmojiNeutral: true}

// ValidAvatar reports whether s is one grapheme, at most two cells wide, and
// neither blank nor a control character (spec user-profile / 主页资料字段).
func ValidAvatar(s string) bool {
	if s == "" || uniseg.GraphemeClusterCount(s) != 1 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	w := avatarWidth.StringWidth(s)
	return w >= 1 && w <= 2
}

// DefaultAvatar is the avatar of an account that never set one: the first
// character of the username, upper-cased, in a colour fixed by the username.
func DefaultAvatar(username string) (avatar, color string) {
	for _, r := range username {
		avatar = strings.ToUpper(string(r))
		break
	}
	if avatar == "" {
		avatar = "?"
	}
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(username)))
	return avatar, AvatarColors[h.Sum32()%uint32(len(AvatarColors))]
}

// ValidProfileText reports whether s fits max characters and holds no line
// break or control character.
func ValidProfileText(s string, max int) bool {
	if len([]rune(s)) > max {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ---------- friends ----------

type FriendRequestReq struct {
	Username string `json:"username"`
	Note     string `json:"note,omitempty"`
}

// FriendRequestRes says what the request did: "sent", or "added" when the
// other side had already asked and the two are now friends.
type FriendRequestRes struct {
	Result string `json:"result"`
	Friend *User  `json:"friend,omitempty"`
}

const (
	FriendRequestSent  = "sent"
	FriendRequestAdded = "added"
)

type FriendRespondReq struct {
	RequestID string `json:"request_id"`
	Accept    bool   `json:"accept"`
}

type FriendUserReq struct {
	Username string `json:"username"`
}

// FriendRequest is a pending request as its recipient sees it.
type FriendRequest struct {
	ID        string `json:"id"`
	From      User   `json:"from"`
	Note      string `json:"note,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type FriendListRes struct {
	Friends  []User          `json:"friends"`
	Incoming []FriendRequest `json:"incoming"`
	Blocked  []User          `json:"blocked"`
	// RemovedBy are former friends who removed you; they stay listed with
	// that status until you remove them too.
	RemovedBy []User `json:"removed_by"`
	// Plaza are people who chose to be listed for anyone to add.
	Plaza []User `json:"plaza"`
}

// FriendRemovedEvent tells a client a friendship ended. ByThem is true when
// the other side did it.
type FriendRemovedEvent struct {
	UserID string `json:"user_id"`
	ByThem bool   `json:"by_them,omitempty"`
}

// ---------- profiles ----------

type ProfileGetReq struct {
	Username string        `json:"username"`
	Cursor   *MomentCursor `json:"cursor,omitempty"`
}

// Profile is a user's page.
type Profile struct {
	User      User   `json:"user"`
	Bio       string `json:"bio,omitempty"`
	CreatedAt int64  `json:"created_at"`
	IsSelf    bool   `json:"is_self"`
	IsFriend  bool   `json:"is_friend"`
	// RequestSent is true when the viewer has a pending request to this user.
	RequestSent bool          `json:"request_sent,omitempty"`
	Moments     []Moment      `json:"moments"`
	Next        *MomentCursor `json:"next,omitempty"`
}

// ProfileUpdateReq changes the fields that are present; nil keeps a field.
type ProfileUpdateReq struct {
	Nickname *string `json:"nickname,omitempty"`
	Bio      *string `json:"bio,omitempty"`
	Avatar   *string `json:"avatar,omitempty"`
	Color    *string `json:"color,omitempty"`
	Gender   *string `json:"gender,omitempty"`
	Age      *int    `json:"age,omitempty"`
}

// Genders a profile may show; "" keeps it private.
var Genders = []string{"", "male", "female"}

// ValidGender reports whether g is one of Genders.
func ValidGender(g string) bool {
	for _, x := range Genders {
		if x == g {
			return true
		}
	}
	return false
}

// MaxAge bounds the age a profile may show; 0 keeps it private.
const MaxAge = 120

// EvPlazaChanged tells everyone online that the plaza changed, so open
// friends pages refetch it.
const EvPlazaChanged = "plaza.changed"

// ProfileUpdatedEvent carries the new public profile.
type ProfileUpdatedEvent struct {
	User User   `json:"user"`
	Bio  string `json:"bio,omitempty"`
}

// ---------- moments ----------

// MomentCursor names the last moment of the previous page.
type MomentCursor struct {
	At int64  `json:"at"`
	ID string `json:"id"`
}

type MomentPostReq struct {
	Body       string `json:"body"`
	Visibility string `json:"visibility,omitempty"`
}

type MomentTimelineReq struct {
	Cursor *MomentCursor `json:"cursor,omitempty"`
}

type MomentTimelineRes struct {
	Moments []Moment      `json:"moments"`
	Next    *MomentCursor `json:"next,omitempty"`
}

type MomentRef struct {
	MomentID string `json:"moment_id"`
}

type MomentLikeReq struct {
	MomentID string `json:"moment_id"`
	Like     bool   `json:"like"`
}

type MomentCommentReq struct {
	MomentID string `json:"moment_id"`
	Body     string `json:"body"`
	ReplyTo  string `json:"reply_to,omitempty"`
}

type MomentCommentsReq struct {
	MomentID string        `json:"moment_id"`
	Before   *MomentCursor `json:"before,omitempty"`
}

type MomentCommentsRes struct {
	Comments []Comment     `json:"comments"`
	Next     *MomentCursor `json:"next,omitempty"`
}

type CommentRef struct {
	CommentID string `json:"comment_id"`
}

// Moment is one post with bounded previews of its visible interactions.
type Moment struct {
	ID             string        `json:"id"`
	Author         User          `json:"author"`
	Body           string        `json:"body"`
	Visibility     string        `json:"visibility"`
	CreatedAt      int64         `json:"created_at"`
	Likes          []User        `json:"likes"`
	Liked          bool          `json:"liked"`
	MoreLikes      int           `json:"more_likes,omitempty"`
	CommentsBefore *MomentCursor `json:"comments_before,omitempty"`
	Comments       []Comment     `json:"comments"`
}

// Comment is one comment. ReplyTo is empty when it replies to no one, or to
// someone the viewer may not see.
type Comment struct {
	ID        string `json:"id"`
	MomentID  string `json:"moment_id"`
	Author    User   `json:"author"`
	ReplyTo   *User  `json:"reply_to,omitempty"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

type MomentDeletedEvent struct {
	MomentID string `json:"moment_id"`
}

// Notification is one interaction with the user's moments or comments.
type Notification struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Actor     User   `json:"actor"`
	MomentID  string `json:"moment_id"`
	Body      string `json:"body,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
	CreatedAt int64  `json:"created_at"`
	Read      bool   `json:"read"`
}

type NotificationsRes struct {
	Notifications []Notification `json:"notifications"`
}

// UnreadRes is what the moments entry badge needs.
type UnreadRes struct {
	Moments       int `json:"moments"`
	Notifications int `json:"notifications"`
	Requests      int `json:"requests"`
}
