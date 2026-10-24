package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowInstallGuideUsesTrustedURLAndCallerPolicy(t *testing.T) {
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, PublicRead: true, Tokens: []authn.TokenConfig{{ID: "operator", Value: "operator-secret", Scopes: []authn.Scope{authn.ScopeAdmin}}, {ID: "attach", Value: "attach-secret", Scopes: []authn.Scope{authn.ScopeAttach}}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(&fakeService{network: protocol.Network{ID: "origin", Name: "Origin"}, rooms: []protocol.Room{{ID: "general", Visibility: protocol.RoomVisibilityPublic}}}, policy, HTTPConfig{PublicBaseURL: "https://public.example"})
	for _, token := range []string{"", "operator-secret", "attach-secret"} {
		req := httptest.NewRequest(http.MethodGet, "https://attacker.example/install.md", nil)
		req.Host = "evil.example;$(touch /tmp/injected)"
		req.Header.Set("X-Forwarded-Host", "evil-forwarded.example")
		req.Header.Set("X-Forwarded-Proto", "https")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status=%d: %s", response.Code, response.Body.String())
		}
		body := response.Body.String()
		if token == "" {
			for _, want := range []string{"# Follow Origin", "moltnet follow add https://public.example", "follows:", `remote_network_id: "origin"`, "curl -N https://public.example/v1/events/stream", "public.<remote-room-id>"} {
				if !strings.Contains(body, want) {
					t.Fatalf("guide missing %q:\n%s", want, body)
				}
			}
			for _, unwanted := range []string{"evil", "touch", "moltnet connect", "moltnet send"} {
				if strings.Contains(body, unwanted) {
					t.Fatalf("guide contains %q", unwanted)
				}
			}
		} else if !strings.Contains(body, "# Join Origin") || strings.Contains(body, "moltnet follow add") {
			t.Fatalf("operator got wrong guide: %s", body)
		}
	}
}

func TestFollowGuideURLTrust(t *testing.T) {
	for _, tc := range []struct {
		name, configured, host, forwardHost, proto, want string
		trust, known                                     bool
	}{
		{name: "valid arbitrary Host is untrusted", host: "evil.example", want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "shell Host is untrusted", host: "evil;$(whoami)", want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "forwarded without config is untrusted", host: "internal", forwardHost: "evil.example", proto: "https", want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "protocol trust does not authorize forwarded host", host: "internal", forwardHost: "evil.example", proto: "https", trust: true, want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "proxy does not authorize Host fallback", host: "evil.example", trust: true, want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "malformed proxy URL", forwardHost: "evil`echo`.example", proto: "https", trust: true, want: "https://YOUR_PUBLIC_MOLTNET_HOST"},
		{name: "configured wins", configured: "https://real.example/", forwardHost: "other.example", proto: "https", trust: true, known: true, want: "https://real.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := authn.NewPolicy(authn.Config{TrustForwardedProto: tc.trust})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodGet, "/install.md", nil)
			req.Host = tc.host
			req.Header.Set("X-Forwarded-Host", tc.forwardHost)
			req.Header.Set("X-Forwarded-Proto", tc.proto)
			got, known := followPublicBaseURL(req, policy, HTTPConfig{PublicBaseURL: tc.configured})
			if got != tc.want || known != tc.known {
				t.Fatalf("URL=%q known=%v want %q %v", got, known, tc.want, tc.known)
			}
		})
	}
}
