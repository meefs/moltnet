package rooms_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

// liveLimitTopology records the size of every live hub delivery alpha
// accepts, and makes beta refuse every live delivery (the home drops the
// rows: an outbox loss), so only catch-up can bring beta a message.
type liveLimitTopology struct {
	hubTopology
	lastLive atomic.Int64
}

func newLiveLimitTopology(t *testing.T) *liveLimitTopology {
	t.Helper()
	topology := &liveLimitTopology{hubTopology: joinedTopology(t, memoryBackend)}
	alpha := topology.alpha.handler.Load().(http.Handler)
	topology.alpha.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/v1/messages" {
			body, _ := io.ReadAll(request.Body)
			topology.lastLive.Store(int64(len(body)))
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		alpha.ServeHTTP(response, request)
	})))
	beta := topology.beta.handler.Load().(http.Handler)
	topology.beta.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			http.Error(response, "refused", http.StatusForbidden)
			return
		}
		beta.ServeHTTP(response, request)
	})))
	return topology
}

// postLive posts text from home and returns the size of the live delivery
// alpha accepted for it.
func (l *liveLimitTopology) postLive(t *testing.T, text string, delivered int) int64 {
	t.Helper()
	l.home.post(t, text)
	waitFor(t, func() bool { return len(l.alpha.texts(t)) == delivered })
	return l.lastLive.Load()
}

// textForLiveSize finds the text length whose live delivery is target
// bytes: the delivery grows linearly with the text (raw in parts, base64 in
// the envelope), so two probes fix the line.
func (l *liveLimitTopology) textForLiveSize(t *testing.T, target int64) int {
	t.Helper()
	const small, large = 100 << 10, 300 << 10
	low := l.postLive(t, "p:"+strings.Repeat("x", small), 1)
	high := l.postLive(t, "q:"+strings.Repeat("x", large), 2)
	perByte := float64(high-low) / float64(large-small)
	return small + int(float64(target-low)/perByte)
}

func (l *liveLimitTopology) withheld(t *testing.T) []protocol.HubWithheldInfo {
	t.Helper()
	var info protocol.HubRoomInfo
	if err := json.Unmarshal([]byte(pairGet(t, l.beta.url+"/v1/rooms/"+localRoom(l.beta)+"/hub", hubOperatorToken)), &info); err != nil {
		t.Fatal(err)
	}
	return info.Withheld
}

// A message whose live delivery came within a few hundred bytes of the
// transport limit (far past the old 900 KiB catch-up cutoff) was accepted live, lost from
// beta's outbox, and must arrive complete by catch-up.
func TestHubCatchUpCarriesAMessageAcceptedLiveNearTheLimit(t *testing.T) {
	l := newLiveLimitTopology(t)
	length := l.textForLiveSize(t, protocol.MaxPairingPayloadBytes-256)
	text := "r:" + strings.Repeat("x", length)
	live := l.postLive(t, text, 3)
	if live > protocol.MaxPairingPayloadBytes || live < protocol.MaxPairingPayloadBytes-512 {
		t.Fatalf("live delivery was %d bytes, not near the %d limit", live, protocol.MaxPairingPayloadBytes)
	}
	waitFor(t, func() bool { return l.home.delivery(t, "link-beta").Rejected == 3 })
	if err := l.beta.catchUp(t, l.global); err != nil {
		t.Fatal(err)
	}
	got := l.beta.texts(t)
	if len(got) != 3 || got[2] != text {
		t.Fatalf("beta caught up %d messages; the near-limit one complete: %v", len(got), len(got) == 3 && got[2] == text)
	}
	if withheld := l.withheld(t); len(withheld) != 0 {
		t.Fatalf("withheld = %#v", withheld)
	}
}

// A message live delivery accepted within a page's own few hundred bytes of
// the limit cannot fit any page. Catch-up completes past it, and beta keeps
// it as explicitly withheld: shown, never stored as delivered.
func TestHubCatchUpMarksAMessageThatFitsNoPageAsWithheld(t *testing.T) {
	l := newLiveLimitTopology(t)
	length := l.textForLiveSize(t, protocol.MaxPairingPayloadBytes-8)
	live := l.postLive(t, "r:"+strings.Repeat("x", length), 3)
	if live > protocol.MaxPairingPayloadBytes || live < protocol.MaxPairingPayloadBytes-120 {
		t.Fatalf("live delivery was %d bytes, not at the %d limit", live, protocol.MaxPairingPayloadBytes)
	}
	l.home.post(t, "after the big one")
	waitFor(t, func() bool { return l.home.delivery(t, "link-beta").Rejected == 4 })
	if err := l.beta.catchUp(t, l.global); err != nil {
		t.Fatal(err)
	}
	got := l.beta.texts(t)
	if len(got) != 3 || got[2] != "after the big one" {
		t.Fatalf("beta holds %d messages", len(got))
	}
	if cursor := l.beta.cursor(t, l.global); cursor.Seq != 4 {
		t.Fatalf("cursor = %#v", cursor)
	}
	withheld := l.withheld(t)
	if len(withheld) != 1 || withheld[0].RoomSeq != 3 || withheld[0].Reason != protocol.RoomSyncWithheldTooLarge {
		t.Fatalf("withheld = %#v", withheld)
	}
}
