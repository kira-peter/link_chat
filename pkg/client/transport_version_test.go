package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"link-chat/pkg/protocol"
	"link-chat/pkg/version"
)

// fakeHelloServer answers the first frame with reply(hello) and records the
// hello it received.
func fakeHelloServer(t *testing.T, reply func(protocol.Frame) protocol.Frame) (url string, got <-chan protocol.HelloPayload) {
	t.Helper()
	ch := make(chan protocol.HelloPayload, 1)
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		_, b, err := ws.Read(ctx)
		if err != nil {
			return
		}
		f, _ := protocol.Decode(b)
		var hp protocol.HelloPayload
		_ = json.Unmarshal(f.Payload, &hp)
		ch <- hp
		out, _ := protocol.Encode(reply(f))
		_ = ws.Write(ctx, websocket.MessageText, out)
		// Hold the socket open until the client is done with it.
		_, _, _ = ws.Read(ctx)
	}))
	t.Cleanup(hs.Close)
	return "ws" + strings.TrimPrefix(hs.URL, "http"), ch
}

func TestHandshakeReportsClientVersion(t *testing.T) {
	url, got := fakeHelloServer(t, func(f protocol.Frame) protocol.Frame {
		ack, _ := protocol.NewRes(f.ID, protocol.HelloPayload{Version: protocol.Version, Server: "link-chat/9.9.9"})
		return ack
	})
	tr := NewTransport(url)
	if err := tr.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer tr.Close()
	hp := <-got
	if hp.Client != ClientName+"/"+version.String() {
		t.Fatalf("hello client = %q", hp.Client)
	}
}

func TestHandshakeOutdated(t *testing.T) {
	url, _ := fakeHelloServer(t, func(f protocol.Frame) protocol.Frame {
		e, _ := protocol.NewErrorData(f.ID, protocol.NewErrorCode(protocol.CodeClientOutdated),
			protocol.ClientOutdatedData{MinVersion: "99.0.0", ClientVersion: version.String()})
		return e
	})
	err := NewTransport(url).Connect(context.Background())
	var oe *OutdatedError
	if !errors.As(err, &oe) {
		t.Fatalf("want *OutdatedError, got %v", err)
	}
	if oe.Min != "99.0.0" || oe.Current != version.String() {
		t.Fatalf("got %+v", oe)
	}
}

// A server speaking a newer protocol is still refused once the ack is read
// from the response envelope's data.
func TestHandshakeRejectsNewerServerProtocol(t *testing.T) {
	url, _ := fakeHelloServer(t, func(f protocol.Frame) protocol.Frame {
		ack, _ := protocol.NewRes(f.ID, protocol.HelloPayload{Version: protocol.Version + 1})
		return ack
	})
	if err := NewTransport(url).Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "protocol") {
		t.Fatalf("want protocol mismatch, got %v", err)
	}
}
