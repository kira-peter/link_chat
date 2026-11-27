package storage

// Friends, profiles and moments (specs friends, user-profile, moments).

// Profile is a user's public profile. A user with no row reads as the zero
// value, and the server fills in defaults.
type Profile struct {
	UserID    string
	Nickname  string
	Bio       string
	Avatar    string
	Color     string
	Gender    string
	Age       int
	UpdatedAt int64
}

// FriendRequest is a pending request from one user to another.
type FriendRequest struct {
	ID        string
	FromID    string
	ToID      string
	Note      string
	CreatedAt int64
}

// Moment visibilities.
const (
	VisibilityFriends = "friends"
	VisibilityPrivate = "private"
)

// Moment is one post.
type Moment struct {
	ID         string
	AuthorID   string
	Body       string
	Visibility string
	CreatedAt  int64
}

// Like is one like of a moment.
type Like struct {
	MomentID  string
	UserID    string
	CreatedAt int64
}

// Comment is one comment. ReplyToUser is the author of the comment replied
// to, kept so the reply still names them after that comment is deleted.
type Comment struct {
	ID          string
	MomentID    string
	AuthorID    string
	ReplyTo     string
	ReplyToUser string
	Body        string
	CreatedAt   int64
}

// Notification is one interaction addressed to UserID.
type Notification struct {
	ID         int64
	UserID     string
	Kind       string
	ActorID    string
	MomentID   string
	CommentID  string
	Body       string
	MomentBody string // Populated when the notification is read.
	CreatedAt  int64
	Read       bool
}

// MomentCursor pages moments by (CreatedAt, ID), newest first. The zero
// value starts from the newest.
type MomentCursor struct {
	At int64
	ID string
}

// LikePreview contains the bounded list and the viewer's visible totals.
type LikePreview struct {
	Likes []Like
	Total int
	Liked bool
}

// CommentPage is displayed oldest first, with Next pointing to older comments.
type CommentPage struct {
	Comments []Comment
	Next     *MomentCursor
}

// MomentPage is one page of moments. Next is nil when there is no more.
type MomentPage struct {
	Moments []Moment
	Next    *MomentCursor
}

// Social is the persistence of friends, profiles and moments.
type Social interface {
	// Profiles. ProfileOf returns the zero Profile (with UserID set) when the
	// user never saved one.
	ProfileOf(userID string) (Profile, error)
	ProfilesOf(userIDs []string) (map[string]Profile, error)
	SaveProfile(p Profile) error

	// Friendships are symmetric: one call covers both directions.
	AddFriendship(a, b string, at int64) error
	RemoveFriendship(a, b string) error
	AreFriends(a, b string) (bool, error)
	FriendIDs(userID string) ([]string, error)
	// Removals remember who removed whom, so the removed side can see it.
	// AddFriendship and Block clear them between the pair.
	AddRemoval(userID, byID string, at int64) error
	DeleteRemoval(userID, byID string) error
	RemovedByIDs(userID string) ([]string, error)
	// PlazaUserIDs are the users whose plaza setting is on.
	PlazaUserIDs() ([]string, error)

	// Requests. SaveFriendRequest replaces any pending request from the same
	// sender to the same recipient, keeping its ID.
	SaveFriendRequest(r FriendRequest) (FriendRequest, error)
	FriendRequestByID(id string) (FriendRequest, error)
	FriendRequestBetween(from, to string) (FriendRequest, error)
	IncomingFriendRequests(userID string) ([]FriendRequest, error)
	// DeleteFriendRequests removes pending requests in either direction.
	DeleteFriendRequests(a, b string) error

	// Blocks are directed: blocker blocked blocked.
	Block(blocker, blocked string, at int64) error
	Unblock(blocker, blocked string) error
	// IsBlocked reports whether either user has blocked the other.
	IsBlocked(a, b string) (bool, error)
	BlockedIDs(blocker string) ([]string, error)

	// Moments.
	AddMoment(m Moment) error
	MomentByID(id string) (Moment, error)
	// DeleteMoment removes the moment with its likes, comments and
	// notifications.
	DeleteMoment(id string) error
	// MomentsBy pages the moments of any of authorIDs: their friends-visible
	// ones, plus every moment of selfID (private ones too) when selfID is
	// among the authors.
	MomentsBy(authorIDs []string, selfID string, from MomentCursor, limit int) (MomentPage, error)
	// AllMoments pages every moment, for the admin console. authorID narrows
	// it when not empty.
	AllMoments(authorID string, from MomentCursor, limit int) (MomentPage, error)
	// CountMomentsSince counts friends-visible moments of authorIDs newer than
	// since.
	CountMomentsSince(authorIDs []string, since int64) (int, error)

	SetLike(l Like, on bool) error
	ChangeLike(l Like, on bool) (bool, error)
	LikePreviews(viewerID string, momentIDs []string, limit int) (map[string]LikePreview, error)
	LikesOf(momentIDs []string) (map[string][]Like, error)

	AddComment(c Comment) error
	CommentByID(id string) (Comment, error)
	DeleteComment(id string) error
	CommentsOf(momentIDs []string) (map[string][]Comment, error)
	CommentPreviews(viewerID string, momentIDs []string, limit int) (map[string]CommentPage, error)
	MomentComments(viewerID, momentID string, before MomentCursor, limit int) (CommentPage, error)

	AddNotification(n Notification) (Notification, error)
	Notifications(userID string, limit int) ([]Notification, error)
	NotificationFor(userID string, id int64) (Notification, error)
	MarkNotificationsRead(userID string) error
	UnreadNotifications(userID string) (int, error)

	// MomentsSeenAt is when the user last read the first page of the
	// timeline; 0 when never.
	MomentsSeenAt(userID string) (int64, error)
	SetMomentsSeenAt(userID string, at int64) error
}
