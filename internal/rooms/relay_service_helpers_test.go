package rooms

import (
	"encoding/json"
	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/events"
	"github.com/noopolis/moltnet/internal/pairings"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newRelayTestService(t *testing.T, networkID string, networkName string) (*Service, *httptest.Server) {
	return newRelayTestServiceWithBearer(t, networkID, networkName, "")
}

func newRelayTestServiceWithBearer(t *testing.T, networkID string, networkName string, expectedBearer string) (*Service, *httptest.Server) {
	t.Helper()

	memory := store.NewMemoryStore()
	service := NewService(ServiceConfig{
		AllowHumanIngress: true,
		NetworkID:         networkID,
		NetworkName:       networkName,
		Version:           "test",
		Store:             memory,
		Messages:          memory,
		Broker:            events.NewBroker(),
		PairingClient:     pairings.NewClient(),
	})

	t.Cleanup(func() { _ = service.Close() })

	server := httptest.NewServer(newRelayTestHandler(service, expectedBearer))
	return service, server
}

func newRelayTestHandler(service *Service, expectedBearer string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/v1/network" {
			writeRelayTestJSON(response, compatibleRemoteNetwork(service.networkID))
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/messages" {
			http.NotFound(response, request)
			return
		}
		if expectedBearer != "" && request.Header.Get("Authorization") != "Bearer "+expectedBearer {
			http.Error(response, "authorization required", http.StatusUnauthorized)
			return
		}

		var payload protocol.SendMessageRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		ctx := request.Context()
		if expectedBearer != "" {
			policy, err := authn.NewPolicy(authn.Config{
				Mode: authn.ModeBearer,
				Tokens: []authn.TokenConfig{{
					ID:     "pair-relay",
					Value:  expectedBearer,
					Scopes: []authn.Scope{authn.ScopePair},
				}},
			})
			if err != nil {
				http.Error(response, err.Error(), http.StatusInternalServerError)
				return
			}
			claims, err := policy.AuthenticateRequest(request, authn.ScopePair)
			if err != nil {
				http.Error(response, err.Error(), http.StatusUnauthorized)
				return
			}
			ctx = authn.WithClaims(ctx, claims)
		}

		accepted, err := service.SendMessageContext(ctx, payload)
		if err != nil {
			http.Error(response, err.Error(), http.StatusBadGateway)
			return
		}

		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(response).Encode(accepted)
	})
}

func writeRelayTestJSON(response http.ResponseWriter, payload any) {
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(payload); err != nil {
		http.Error(response, err.Error(), http.StatusInternalServerError)
	}
}

func waitForRoomMessage(t *testing.T, service *Service, roomID string) protocol.MessagePage {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page, err := service.ListRoomMessages(roomID, "", 10)
		if err == nil && len(page.Messages) > 0 {
			return page
		}
		time.Sleep(20 * time.Millisecond)
	}

	page, err := service.ListRoomMessages(roomID, "", 10)
	t.Fatalf("timed out waiting for room relay, page=%#v err=%v", page, err)
	return protocol.MessagePage{}
}

func waitForDMMessage(t *testing.T, service *Service, dmID string) protocol.MessagePage {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page, err := service.ListDMMessages(dmID, "", 10)
		if err == nil && len(page.Messages) > 0 {
			return page
		}
		time.Sleep(20 * time.Millisecond)
	}

	page, err := service.ListDMMessages(dmID, "", 10)
	t.Fatalf("timed out waiting for dm relay, page=%#v err=%v", page, err)
	return protocol.MessagePage{}
}
