package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/noopolis/moltnet/internal/app"
	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

type clockRoundTripper func(*http.Request) (*http.Response, error)

func (f clockRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClockPreflightCombinations(t *testing.T) {
	clock := &bridgeconfig.ClockConfig{Version: bridgeconfig.TaskClockVersion, Origin: "2025-10-01T08:00:00Z", AnchorEpochMS: 1791619200000}
	differentOrigin := *clock
	differentOrigin.Origin = "2025-10-01T09:00:00Z"
	differentAnchor := *clock
	differentAnchor.AnchorEpochMS++
	sameOffset := differentOrigin
	sameOffset.AnchorEpochMS += 3600000
	equivalent := *clock
	equivalent.Origin = "2025-10-01T10:00:00+02:00"
	for _, test := range []struct {
		name           string
		server, bridge *bridgeconfig.ClockConfig
		mismatch       bool
	}{
		{"neither", nil, nil, false},
		{"server_only", clock, nil, true},
		{"bridge_only", nil, clock, true},
		{"both_matching", clock, clock, false},
		{"different_origin", clock, &differentOrigin, true},
		{"different_anchor", clock, &differentAnchor, true},
		{"same_offset_different_identity", clock, &sameOffset, true},
		{"equivalent_origin", clock, &equivalent, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, err := app.New(app.Config{NetworkID: "local", Clock: test.server})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			prior := defaultPreflightClient
			defer func() { defaultPreflightClient = prior }()
			defaultPreflightClient = &http.Client{Transport: clockRoundTripper(func(request *http.Request) (*http.Response, error) {
				// A caller-supplied identity must never become the server identity.
				request.Header.Set(bridgeconfig.TaskClockHeader, "forged")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				return response.Result(), nil
			})}
			config := bridgeconfig.Config{Clock: test.bridge, Moltnet: bridgeconfig.MoltnetConfig{BaseURL: "http://moltnet.invalid", NetworkID: "local"}}
			err = Preflight(context.Background(), config)
			if test.mismatch {
				if err == nil || !strings.Contains(err.Error(), "clock mismatch") {
					t.Fatalf("expected clock mismatch, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			request, err := NewPreflightRequest(config)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := test.bridge.Identity()
			if request.CacheKey().ClockIdentity != want {
				t.Fatal("cached preflight can bypass clock identity")
			}
		})
	}
}
