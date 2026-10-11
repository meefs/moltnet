package transport

import (
	"net/http"

	"github.com/noopolis/moltnet/pkg/bridgeconfig"
)

func withClockIdentity(next http.Handler, identity string) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if identity != "" {
			response.Header().Set(bridgeconfig.TaskClockHeader, identity)
		}
		next.ServeHTTP(response, request)
	})
}
