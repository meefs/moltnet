package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestConsoleViewerPermissionsMatchRoutes(t *testing.T) {
	for _, registration := range []string{authn.AgentRegistrationDisabled, authn.AgentRegistrationToken, authn.AgentRegistrationOpen} {
		t.Run(registration, func(t *testing.T) {
			policy, err := authn.NewPolicy(authn.Config{
				Mode: authn.ModeBearer, PublicRead: true, AgentRegistration: registration,
				Tokens: []authn.TokenConfig{
					{ID: "observer", Value: "observe-secret", Scopes: []authn.Scope{authn.ScopeObserve}},
					{ID: "operator", Value: "operator-secret", Scopes: []authn.Scope{authn.ScopeAdmin, authn.ScopeWrite}},
					{ID: "attach", Value: "attach-secret", Scopes: []authn.Scope{authn.ScopeAttach}},
					{ID: "writer", Value: "write-secret", Scopes: []authn.Scope{authn.ScopeWrite}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			handler := NewHTTPHandler(&fakeService{
				network:          protocol.Network{ID: "public", Capabilities: protocol.NetworkCapabilities{HumanIngress: true, DirectMessages: true, Pairings: true}},
				agentTokenClaims: map[string]authn.Claims{"agent-secret": authn.NewAgentTokenClaims("reader", "agent-token:reader")},
			}, policy)
			for _, tc := range []struct {
				name, token, kind, id        string
				pairings, dms, connect, send bool
			}{
				{name: "anonymous", kind: "anonymous", connect: registration == authn.AgentRegistrationOpen},
				{name: "observer", token: "observe-secret", kind: "token", id: "observer", pairings: true, dms: true},
				{name: "operator", token: "operator-secret", kind: "token", id: "operator", pairings: true, dms: true, connect: true, send: true},
				{name: "attach only", token: "attach-secret", kind: "token", id: "attach", connect: true},
				{name: "write only", token: "write-secret", kind: "token", id: "writer", send: true},
				{name: "agent", token: "agent-secret", kind: "agent", id: "reader", dms: true, connect: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					get := func(path string) *httptest.ResponseRecorder {
						req := httptest.NewRequest(http.MethodGet, path, nil)
						if tc.token != "" {
							req.Header.Set("Authorization", "Bearer "+tc.token)
						}
						rec := httptest.NewRecorder()
						handler.ServeHTTP(rec, req)
						return rec
					}
					rec := get("/v1/network")
					if rec.Code != http.StatusOK {
						t.Fatalf("network status %d: %s", rec.Code, rec.Body.String())
					}
					var network protocol.Network
					if err := json.Unmarshal(rec.Body.Bytes(), &network); err != nil {
						t.Fatal(err)
					}
					want := protocol.NetworkConsole{CanFollow: !(tc.connect || registration == authn.AgentRegistrationOpen), Viewer: protocol.ConsoleViewer{Kind: tc.kind, ID: tc.id}, CanReadPairings: tc.pairings, CanReadDMs: tc.dms, CanConnectAgent: tc.connect || registration == authn.AgentRegistrationOpen, CanSendHuman: tc.send}
					if network.Console == nil || *network.Console != want {
						t.Fatalf("console = %+v, want %+v", network.Console, want)
					}
					for path, allowed := range map[string]bool{"/v1/pairings": tc.pairings, "/v1/dms": tc.dms} {
						rec := get(path)
						if (rec.Code == http.StatusOK) != allowed {
							t.Fatalf("%s status=%d, permission=%v", path, rec.Code, allowed)
						}
					}
					// A successful auth gate reaches the websocket upgrade check (400), not 401/403.
					rec = get("/v1/attach")
					if (rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden) != tc.connect {
						t.Fatalf("attach status=%d, permission=%v", rec.Code, tc.connect)
					}
				})
			}
		})
	}
}

func TestConsoleAnonymousMirrorAndLegacyPermissions(t *testing.T) {
	policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, PublicRead: true, Tokens: []authn.TokenConfig{{ID: "operator", Value: "secret", Scopes: []authn.Scope{authn.ScopeAdmin}}}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/network", nil)
	network := networkForRequest(policy, request, protocol.Network{})
	want := protocol.NetworkConsole{CanFollow: true, Viewer: protocol.ConsoleViewer{Kind: "anonymous"}}
	if *network.Console != want {
		t.Fatalf("mirror console=%+v, want %+v", network.Console, want)
	}
	network = networkForRequest(nil, request, protocol.Network{Capabilities: protocol.NetworkCapabilities{HumanIngress: true}})
	if !network.Console.CanReadPairings || !network.Console.CanReadDMs || !network.Console.CanConnectAgent || !network.Console.CanSendHuman {
		t.Fatalf("legacy console=%+v", network.Console)
	}
}

func TestConsoleFollowRequiresPublicReadAndNoConnect(t *testing.T) {
	for _, publicRead := range []bool{false, true} {
		policy, err := authn.NewPolicy(authn.Config{Mode: authn.ModeBearer, PublicRead: publicRead, Tokens: []authn.TokenConfig{{ID: "operator", Value: "secret", Scopes: []authn.Scope{authn.ScopeAdmin}}}})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/network", nil)
		network := networkForRequest(policy, req, protocol.Network{})
		if network.Console.CanFollow != publicRead || network.Console.CanConnectAgent {
			t.Fatalf("public=%v console=%+v", publicRead, network.Console)
		}
	}
}
