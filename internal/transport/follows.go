package transport

import (
	"context"
	"net/http"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/pkg/protocol"
)

type followService interface {
	ListFollowsContext(context.Context) (protocol.FollowPage, error)
}

func registerFollowRoutes(mux *http.ServeMux, policy *authn.Policy, service Service) {
	mux.HandleFunc("GET /v1/follows", authorizedOperatorOrObserver(policy, service, func(response http.ResponseWriter, request *http.Request) {
		reader, ok := service.(followService)
		if !ok {
			writeJSON(response, http.StatusOK, protocol.FollowPage{Follows: []protocol.FollowStatus{}})
			return
		}
		page, err := reader.ListFollowsContext(request.Context())
		if err != nil {
			writeError(response, statusForError(err), err)
			return
		}
		writeJSON(response, http.StatusOK, page)
	}))
}
