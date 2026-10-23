package transport

import (
	"net/http"

	authn "github.com/noopolis/moltnet/internal/auth"
)

// These predicates are shared by route gates and console discovery.
func requestAllowsScopes(policy *authn.Policy, request *http.Request, scopes []authn.Scope) bool {
	if policy == nil || !policy.Enabled() {
		return true
	}
	claims, ok := authn.ClaimsFromContext(request.Context())
	return ok && claims.AllowsAny(scopes)
}

func canReadPairings(policy *authn.Policy, request *http.Request) bool {
	claims, _ := authn.ClaimsFromContext(request.Context())
	return requestAllowsScopes(policy, request, readScopes) && !claims.AgentToken()
}

var attachScopes = []authn.Scope{authn.ScopeAttach, authn.ScopeAdmin}

func canAttachAgent(policy *authn.Policy, request *http.Request) bool {
	if policy == nil || !policy.Enabled() {
		return true
	}
	_, authenticated := authn.ClaimsFromContext(request.Context())
	if !authenticated && policy.AgentRegistration() == authn.AgentRegistrationOpen {
		return true
	}
	return requestAllowsScopes(policy, request, attachScopes)
}
