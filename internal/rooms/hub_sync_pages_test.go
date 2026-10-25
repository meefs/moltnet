package rooms_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// The home's fanout to beta is refused (the outbox drops every row, as an
// expired outbox would), so only catch-up can deliver. The history is far
// larger than one 1 MiB transport response: pages split by encoded size and
// each stays inside the pairing transports' limit (HTTP and relay both cap
// responses at 1 MiB).
func TestHubCatchUpPagesLargeHistoryWithinTransportLimits(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	real := h.beta.handler.Load().(http.Handler)
	h.beta.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			http.Error(response, "refused", http.StatusForbidden)
			return
		}
		real.ServeHTTP(response, request)
	})))
	var largest, pages atomic.Int64
	homeReal := h.home.handler.Load().(http.Handler)
	h.home.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/hub/sync" {
			homeReal.ServeHTTP(response, request)
			return
		}
		recorder := httptest.NewRecorder()
		homeReal.ServeHTTP(recorder, request)
		pages.Add(1)
		if size := int64(recorder.Body.Len()); size > largest.Load() {
			largest.Store(size)
		}
		for key, values := range recorder.Header() {
			response.Header()[key] = values
		}
		response.WriteHeader(recorder.Code)
		_, _ = response.Write(recorder.Body.Bytes())
	})))
	const count = 8
	var want []string
	for index := 0; index < count; index++ {
		text := fmt.Sprintf("%d:%s", index, strings.Repeat("x", 150<<10))
		h.home.post(t, text)
		want = append(want, text)
	}
	waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Rejected == count })
	pages.Store(0)
	if err := h.beta.catchUp(t, h.global); err != nil {
		t.Fatalf("catch-up of a large history: %v", err)
	}
	if got := h.beta.texts(t); !equalStrings(got, want) {
		t.Fatalf("beta holds %d of %d messages", len(got), count)
	}
	if cursor := h.beta.cursor(t, h.global); cursor.Seq != count {
		t.Fatalf("cursor = %#v", cursor)
	}
	if largest.Load() > 1<<20 || pages.Load() < 3 {
		t.Fatalf("largest sync response %d bytes over %d pages", largest.Load(), pages.Load())
	}
}

// A control log longer than one page comes over several control-only pages
// before any entry, and the member keeps pulling until it has all of it.
// Live fanout is refused (the outbox drops the rows), so one catch-up pass
// alone must bring the roster and messages.
func TestHubCatchUpPagesALongControlLog(t *testing.T) {
	defer rooms.SetSyncPageBytesForTest(8 << 10)()
	h := joinedTopology(t, memoryBackend)
	real := h.beta.handler.Load().(http.Handler)
	h.beta.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			http.Error(response, "refused", http.StatusForbidden)
			return
		}
		real.ServeHTTP(response, request)
	})))
	h.home.post(t, "before the churn")
	roles := []string{protocol.RoomRoleReader, protocol.RoomRoleWriter}
	for index := 0; index < 300; index++ {
		if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
			GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "alpha", Role: roles[index%2]}); err != nil {
			t.Fatal(err)
		}
	}
	h.home.post(t, "after the churn")
	waitFor(t, func() bool { return h.home.delivery(t, "link-beta").Rejected == 2 })
	var largest, controlPages atomic.Int64
	homeReal := h.home.handler.Load().(http.Handler)
	h.home.handler.Store(http.Handler(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		recorder := httptest.NewRecorder()
		homeReal.ServeHTTP(recorder, request)
		if strings.HasPrefix(request.URL.Path, "/v1/hub/") {
			if int64(recorder.Body.Len()) > largest.Load() {
				largest.Store(int64(recorder.Body.Len()))
			}
			if strings.Contains(recorder.Body.String(), `"entries":[]`) && strings.Contains(recorder.Body.String(), `"more":true`) {
				controlPages.Add(1)
			}
		}
		for key, values := range recorder.Header() {
			response.Header()[key] = values
		}
		response.WriteHeader(recorder.Code)
		_, _ = response.Write(recorder.Body.Bytes())
	})))
	// One catch-up pass, as the sync loop runs it, brings it all.
	if err := h.beta.service.SyncHubRoomOnceForTest(h.global); err != nil {
		t.Fatal(err)
	}
	if got := h.beta.texts(t); !equalStrings(got, []string{"before the churn", "after the churn"}) {
		t.Fatalf("beta after one pass = %q", got)
	}
	view, err := h.beta.service.HubRoomsContext(contextBackground())
	if err != nil || view[0].Epoch != 303 {
		t.Fatalf("beta's epoch = %#v, %v", view, err)
	}
	if largest.Load() > 16<<10 || controlPages.Load() < 10 {
		t.Fatalf("largest hub response %d bytes over an 8 KiB budget; %d control-only sync pages", largest.Load(), controlPages.Load())
	}
}

// The control log endpoint pages too, and a member pulls every page.
func TestHubControlLogPagesAndJoinPullsThemAll(t *testing.T) {
	defer rooms.SetSyncPageBytesForTest(16 << 10)()
	h := newHubTopology(t, memoryBackend)
	h.add(t, h.alpha)
	for index := 0; index < 80; index++ {
		if _, err := h.home.service.AppendHubControlContext(contextBackground(), rooms.HubControlRequest{
			GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "alpha", Role: protocol.RoomRoleWriter}); err != nil {
			t.Fatal(err)
		}
	}
	h.add(t, h.beta)
	status, body := hubHTTP(t, http.MethodGet, h.home.url+"/v1/hub/control?after=0&room="+urlQuery(h.global), "beta-link-secret", nil)
	if status != http.StatusOK || !strings.Contains(body, `"more":true`) || len(body) > 24<<10 {
		t.Fatalf("first control page: %d, %d bytes", status, len(body))
	}
	h.join(t, h.beta)
	view, err := h.beta.service.HubRoomsContext(contextBackground())
	if err != nil || view[0].Epoch != 83 {
		t.Fatalf("beta joined at %#v, %v", view, err)
	}
}
