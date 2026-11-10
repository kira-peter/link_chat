// Package protocol defines the wire contract between the link_chat server and
// its clients.
//
// Every WebSocket text message carries exactly one Frame, encoded as JSON:
//
//	{"type":"req","id":"a1","payload":{"op":"message.send","data":{...}}}
//
// The envelope is deliberately minimal. Extra fields are ignored rather than
// rejected so that a newer peer can add them without breaking an older one.
package protocol

import (
	"encoding/json"
	"errors"
)

// Version is the protocol version implemented by this package.
//
// Peers advertise it in the hello frame; the server refuses anything newer than
// it knows about. Bump it whenever a frame changes meaning — not when a frame
// gains an optional field, which older peers already ignore.
const Version = 1

// Frame types. Every frame on the wire has one of these as its "type".
const (
	// TypeHello is the first frame a client sends. Nothing else is accepted
	// before it (see spec chat-protocol / 协议版本协商).
	TypeHello = "hello"
	// TypeHelloAck answers a hello and admits the connection.
	TypeHelloAck = "hello-ack"
	// TypeReq is a client-initiated request. It always carries an ID.
	TypeReq = "req"
	// TypeRes is the server's answer to exactly one request, echoing its ID.
	TypeRes = "res"
	// TypeError is a response that failed. It is a distinct type purely so
	// clients can branch on "type" without inspecting the payload.
	TypeError = "error"
	// TypeEvent is a server-initiated push. It never carries an ID.
	TypeEvent = "event"
)

// Frame is the single unit of the wire protocol.
type Frame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// HelloPayload is carried by TypeHello and TypeHelloAck.
type HelloPayload struct {
	Version int    `json:"version"`
	Client  string `json:"client,omitempty"`
	Server  string `json:"server,omitempty"`
}

// ReqPayload is carried by TypeReq.
type ReqPayload struct {
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ResPayload is carried by TypeRes and TypeError.
type ResPayload struct {
	OK           bool            `json:"ok"`
	Code         string          `json:"code,omitempty"`
	Message      string          `json:"message,omitempty"`
	RetryAfterMS int             `json:"retry_after_ms,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

// EventPayload is carried by TypeEvent. Name identifies the event; Data holds
// its body.
type EventPayload struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data,omitempty"`
}

// ErrMalformed reports bytes that are not a decodable Frame.
var ErrMalformed = errors.New("malformed frame")

// Decode parses one frame from raw bytes.
func Decode(b []byte) (Frame, error) {
	var f Frame
	if err := json.Unmarshal(b, &f); err != nil {
		return Frame{}, ErrMalformed
	}
	if f.Type == "" {
		return Frame{}, ErrMalformed
	}
	return f, nil
}

// Encode serialises a frame. dst may be nil.
func Encode(f Frame) ([]byte, error) {
	return json.Marshal(f)
}

// NewReq builds a request frame.
func NewReq(id, op string, data any) (Frame, error) {
	raw, err := marshalOptional(data)
	if err != nil {
		return Frame{}, err
	}
	p, err := json.Marshal(ReqPayload{Op: op, Data: raw})
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: TypeReq, ID: id, Payload: p}, nil
}

// NewRes builds a successful response frame.
func NewRes(id string, data any) (Frame, error) {
	raw, err := marshalOptional(data)
	if err != nil {
		return Frame{}, err
	}
	p, err := json.Marshal(ResPayload{OK: true, Data: raw})
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: TypeRes, ID: id, Payload: p}, nil
}

// NewError builds a failed response frame tied to a request.
func NewError(id string, e *Error) (Frame, error) {
	p, err := json.Marshal(ResPayload{
		OK:           false,
		Code:         e.Code,
		Message:      e.Message,
		RetryAfterMS: e.RetryAfterMS,
	})
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: TypeError, ID: id, Payload: p}, nil
}

// NewEvent builds a server-pushed event frame. Events never carry an ID, which
// is what lets clients tell a push apart from a reply.
func NewEvent(name string, data any) (Frame, error) {
	raw, err := marshalOptional(data)
	if err != nil {
		return Frame{}, err
	}
	p, err := json.Marshal(EventPayload{Name: name, Data: raw})
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: TypeEvent, Payload: p}, nil
}

// marshalOptional encodes v, returning nil when v is nil so the field is
// omitted instead of becoming the literal "null".
func marshalOptional(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// NewResRequest builds a frame of an arbitrary type carrying a payload. It
// exists for handshake frames (hello, hello-ack), which are neither requests nor
// responses but still travel in the same envelope.
func NewResRequest(id, frameType string, data any) (Frame, error) {
	raw, err := marshalOptional(data)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Type: frameType, ID: id, Payload: raw}, nil
}

// NewErrorCodeFrame builds a failed response for a request that had no usable
// ID (a malformed frame, say). The ID is empty when it cannot be recovered.
func NewErrorCodeFrame(id, code string) (Frame, error) {
	return NewError(id, NewErrorCode(code))
}

// DecodePayload unmarshals a frame payload into dst.
func DecodePayload(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return ErrMalformed
	}
	return nil
}
