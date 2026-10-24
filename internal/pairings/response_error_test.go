package pairings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestHTTPPreservesPeerResponseStatus(t *testing.T) {
	for _, code := range []int{403, 404, 408, 409, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
			defer server.Close()
			_, err := NewClient().RelayMessage(context.Background(), protocol.Pairing{RemoteBaseURL: server.URL}, protocol.SendMessageRequest{})
			var response *ResponseError
			if !errors.As(err, &response) || response.StatusCode() != code {
				t.Fatalf("lost status %d: %v", code, err)
			}
		})
	}
}
