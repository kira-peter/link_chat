package protocol

import "fmt"

// Error codes. These strings are part of the contract: clients branch on them
// and specs name them. Never repurpose one for a different meaning — add a new
// code instead (see spec chat-protocol / 错误表示).
const (
	CodeMalformed            = "malformed_frame"
	CodeProtocolViolation    = "protocol_violation"
	CodeVersionUnsupported   = "protocol_version_unsupported"
	CodeDuplicateRequestID   = "duplicate_request_id"
	CodeFrameTooLarge        = "frame_too_large"
	CodeRateLimited          = "rate_limited"
	CodeUnauthorized         = "unauthorized"
	CodeForbidden            = "forbidden"
	CodeInvalidArgument      = "invalid_argument"
	CodeInvalidUsername      = "invalid_username"
	CodeInvalidChannelName   = "invalid_channel_name"
	CodeUsernameTaken        = "username_taken"
	CodeWeakPassword         = "weak_password"
	CodeInvalidCredentials   = "invalid_credentials"
	CodeTokenExpired         = "token_expired"
	CodeChannelExists        = "channel_exists"
	CodeChannelNotFound      = "channel_not_found"
	CodeConversationNotFound = "conversation_not_found"
	CodeMessageTooLong       = "message_too_long"
	CodeMessageNotFound      = "message_not_found"
	CodeUserNotFound         = "user_not_found"
	CodeStorageUnavailable   = "storage_unavailable"
	CodeInternal             = "internal"
	CodeShuttingDown         = "server_shutting_down"
	CodeSecurityQuestionReq  = "security_question_required"
	CodeSecurityAnswerWrong  = "security_answer_incorrect"
	CodeResetTokenInvalid    = "reset_token_invalid"
	CodeResetTokenExpired    = "reset_token_expired"
	CodeCannotRecover        = "cannot_recover"
)

// Error is the structured failure carried by a TypeError frame.
type Error struct {
	Code         string
	Message      string
	RetryAfterMS int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Errf builds an error with a formatted message.
func Errf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// errorsToClient is the fixed, human-readable text for each code. Keeping the
// wording here (rather than at each call site) means the same failure always
// reads the same way, and no call site accidentally leaks internal detail.
var errorsToClient = map[string]string{
	CodeMalformed:            "frame is not valid JSON",
	CodeProtocolViolation:    "frame arrived out of protocol order",
	CodeVersionUnsupported:   "unsupported protocol version",
	CodeDuplicateRequestID:   "a request with this id is still in flight",
	CodeFrameTooLarge:        "frame exceeds the maximum size",
	CodeRateLimited:          "too many requests, slow down",
	CodeUnauthorized:         "authentication required",
	CodeForbidden:            "not permitted",
	CodeInvalidArgument:      "invalid argument",
	CodeInvalidUsername:      "username is not allowed",
	CodeInvalidChannelName:   "channel name is not allowed",
	CodeUsernameTaken:        "username is already taken",
	CodeWeakPassword:         "password is too weak",
	CodeInvalidCredentials:   "username or password is incorrect",
	CodeTokenExpired:         "session token has expired",
	CodeChannelExists:        "channel already exists",
	CodeChannelNotFound:      "channel does not exist",
	CodeConversationNotFound: "conversation does not exist",
	CodeMessageTooLong:       "message exceeds the maximum length",
	CodeMessageNotFound:      "message does not exist",
	CodeUserNotFound:         "user does not exist",
	CodeStorageUnavailable:   "storage is temporarily unavailable",
	CodeInternal:             "internal server error",
	CodeShuttingDown:         "server is shutting down",
	CodeSecurityQuestionReq:  "a security question and answer are required",
	CodeSecurityAnswerWrong:  "that answer is not correct",
	CodeResetTokenInvalid:    "this reset link is no longer valid",
	CodeResetTokenExpired:    "this reset link has expired",
	CodeCannotRecover:        "this account cannot be recovered",
}

// DefaultMessage returns the standard client-facing text for a code.
func DefaultMessage(code string) string {
	if m, ok := errorsToClient[code]; ok {
		return m
	}
	return errorsToClient[CodeInternal]
}

// NewErrorCode builds an error using the standard message for its code.
func NewErrorCode(code string) *Error {
	return &Error{Code: code, Message: DefaultMessage(code)}
}

// IsKnownCode reports whether code is part of the published set.
func IsKnownCode(code string) bool {
	_, ok := errorsToClient[code]
	return ok
}
