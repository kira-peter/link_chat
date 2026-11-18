package protocol

import (
	"encoding/json"
	"testing"
)

// TestErrorCodesAreDistinct guards the rule that a new code never reuses an
// existing string (spec chat-protocol / 错误表示).
func TestErrorCodesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for code := range errorsToClient {
		if seen[code] {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = true
	}
	if !IsKnownCode(CodeClientOutdated) || CodeClientOutdated != "client_outdated" {
		t.Fatalf("client_outdated is not published")
	}
}

func TestClientOutdatedRoundTrip(t *testing.T) {
	f, err := NewErrorData("hello", NewErrorCode(CodeClientOutdated), ClientOutdatedData{MinVersion: "1.2.0", ClientVersion: "1.1.9"})
	if err != nil {
		t.Fatal(err)
	}
	var p ResPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		t.Fatal(err)
	}
	var d ClientOutdatedData
	if err := json.Unmarshal(p.Data, &d); err != nil {
		t.Fatal(err)
	}
	if f.Type != TypeError || p.Code != CodeClientOutdated || d.MinVersion != "1.2.0" || d.ClientVersion != "1.1.9" {
		t.Fatalf("got %+v %+v", p, d)
	}
	// Without a reported version the field is omitted rather than "".
	b, _ := json.Marshal(ClientOutdatedData{MinVersion: "1.2.0"})
	if string(b) != `{"min_version":"1.2.0"}` {
		t.Fatalf("got %s", b)
	}
}
