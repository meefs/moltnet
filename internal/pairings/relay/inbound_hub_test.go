package relay

import (
	"net/http"
	"testing"
)

func TestInboundAllowsOnlyTheHubControlPull(t *testing.T) {
	allowed := []string{"/v1/hub/control?room=sha256%3Aab%2Fresearch&after=3", "/v1/hub/control"}
	for _, target := range allowed {
		if !isAllowedInboundRequest(http.MethodGet, target) {
			t.Errorf("GET %s refused", target)
		}
	}
	refused := [][2]string{
		{http.MethodPost, "/v1/hub/control"},
		{http.MethodGet, "/v1/hub/rooms"},
		{http.MethodGet, "/v1/hub/control/../rooms"},
	}
	for _, request := range refused {
		if isAllowedInboundRequest(request[0], request[1]) {
			t.Errorf("%s %s allowed", request[0], request[1])
		}
	}
}
