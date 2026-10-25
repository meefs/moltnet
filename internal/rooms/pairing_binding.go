package rooms

import (
	"context"
	"slices"
	"strings"

	authn "github.com/noopolis/moltnet/internal/auth"
	"github.com/noopolis/moltnet/internal/observability"
)

// pairCredentialMatchesOrigin decides whether a pair-scoped credential may
// assert originNetworkID as the origin of an inbound relayed message.
//
// Three cases, in order:
//
//  1. The credential carries a network binding from config
//     (bindPairingNetworks, internal/app/app.go) -- the peer's network id was
//     known at pairing time. This is the join side: `moltnet pair <code>`
//     learns the inviter's network id straight out of the invite. Compare and
//     be done.
//
//  2. No config binding, but this credential has already been seen asserting
//     an origin during this process's lifetime -- compare against that learned
//     value. A second, different origin from the same credential is a
//     cross-pairing impersonation attempt and is refused.
//
//  3. No config binding and never seen before -- first contact. Under strict
//     mode refuse (unchanged). Otherwise learn it and accept, so case 2 can
//     enforce consistency from here on.
//
// Case 2/3 exist because the INVITING side never learns its peer's real
// network id: `pair invite` mints the credential before the peer has ever
// spoken, so `bindPairingNetworks` has nothing to bind from and
// claims.Network() stays empty indefinitely (see
// reference/authentication.md#require_pair_network_binding). Before this,
// that meant an unbound credential could assert *any* origin network id on
// every message forever -- the hole the old code logged about but allowed.
// Trust-on-first-use closes it: whatever the peer claims first is what it is
// held to.
//
// The learned binding is process-local, alongside pairingStatuses
// (service.go), and is NOT written back to config: the server has never
// written its own config, and introducing a second writer racing the
// `pair invite`/`pair revoke` CLI commands over one file is a worse trade.
// Signed peers do not depend on it across restarts: the first signed
// message pins the peer's key with this pairing as its source, and that
// durable pin (pinnedPairingNetworks) is checked before the learned binding.
// The residual re-learn window is left only for unsigned legacy peers, whose
// messages are labeled unsigned and carry no verified identity anyway.
func (s *Service) pairCredentialMatchesOrigin(ctx context.Context, claims authn.Claims, originNetworkID string, strict bool) bool {
	if credentialNetworkID := strings.TrimSpace(claims.Network()); credentialNetworkID != "" {
		return credentialNetworkID == originNetworkID
	}

	credentialKey := pairBindingKey(claims)
	if credentialKey == "" {
		// No stable way to identify this credential across messages, so
		// there is nothing to pin a learned binding to. Fall back to the
		// pre-existing behavior rather than inventing a key.
		if strict {
			logPairBindingRefusal(ctx, originNetworkID)
			return false
		}
		return true
	}

	if strict {
		// Strict mode never learns: only a binding config established at
		// pairing time counts, and this credential has none.
		logPairBindingRefusal(ctx, originNetworkID)
		return false
	}

	// An unbound credential may not claim a network another pairing is
	// config-bound to, nor one other than the id its own peer reported in
	// authenticated discovery (persisted as the delivery destination).
	if reason := s.unboundOriginConflict(ctx, originNetworkID); reason != "" {
		observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID).
			Warn("rejecting inbound pair-scoped message: " + reason)
		return false
	}

	// A key pinned through this pairing durably records which network the
	// credential speaks for. It outlives the process-local learned binding
	// below, so a restart cannot reopen first contact for a different,
	// not-yet-pinned network id under the same credential.
	if pinned, known, err := s.pinnedPairingNetworks(ctx); err != nil {
		observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID).
			Error("refusing pair-scoped message: network key trust store unavailable", "error", err)
		return false
	} else if known {
		if !slices.Contains(pinned, originNetworkID) {
			observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID, "pinned_networks", pinned).
				Warn("rejecting inbound pair-scoped message whose origin differs from the network pinned through this pairing")
			return false
		}
		s.bindOrMatchPairNetwork(credentialKey, originNetworkID)
		return true
	}

	matched, firstContact := s.bindOrMatchPairNetwork(credentialKey, originNetworkID)
	switch {
	case firstContact:
		observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID).
			Info("binding this pair credential to the origin network it first asserted; later messages claiming a different origin will be rejected")
	case !matched:
		observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID).
			Warn("rejecting inbound pair-scoped message whose origin network id differs from the one this credential first asserted")
	}
	return matched
}

// bindOrMatchPairNetwork compares originNetworkID against this credential's
// learned binding, establishing it if there is none, as ONE atomic step
// under a single exclusive lock.
//
// Splitting this into a read followed by a later write loses the guarantee
// entirely: two concurrent first-contact messages asserting different origins
// would both find no binding, both proceed, and both be accepted, with only
// later messages constrained to whichever write happened to land. The whole
// point is that the first origin wins and every other origin loses, so the
// comparison and the establishment cannot be separated.
//
// Returns whether the origin is acceptable, and whether this call is the one
// that established the binding.
func (s *Service) bindOrMatchPairNetwork(credentialKey, originNetworkID string) (matched bool, firstContact bool) {
	s.pairingsMu.Lock()
	defer s.pairingsMu.Unlock()

	if s.learnedPairNetworks == nil {
		s.learnedPairNetworks = map[string]string{}
	}
	if learned, ok := s.learnedPairNetworks[credentialKey]; ok {
		return learned == originNetworkID, false
	}
	s.learnedPairNetworks[credentialKey] = originNetworkID
	return true, true
}

// unboundOriginConflict explains why an unbound credential may not claim
// originNetworkID, or returns "".
func (s *Service) unboundOriginConflict(ctx context.Context, originNetworkID string) string {
	pairing, ok := s.pairingForPairScopedContext(ctx)
	if !ok {
		return ""
	}
	if other, err := s.findPairingByRemoteNetwork(originNetworkID); err == nil && other.ID != pairing.ID {
		return "origin network is config-bound to a different pairing"
	}
	if s.pairingDeliveries != nil {
		if state, err := s.pairingDeliveries.PairingDeliveryStateContext(ctx, pairing.ID); err == nil &&
			state.RemoteNetworkID != "" && state.RemoteNetworkID != originNetworkID {
			return "origin network differs from the network this pairing's peer reported in discovery"
		}
	}
	return ""
}

// pinnedPairingNetworks reports the networks whose keys were pinned through
// the caller's pairing; known is false when there are none.
func (s *Service) pinnedPairingNetworks(ctx context.Context) ([]string, bool, error) {
	pairing, ok := s.pairingForPairScopedContext(ctx)
	if !ok || s.keyPins == nil {
		return nil, false, nil
	}
	networks, err := s.keyPins.PairingNetworks(pairing.ID)
	if err != nil {
		return nil, false, err
	}
	return networks, len(networks) > 0, nil
}

func logPairBindingRefusal(ctx context.Context, originNetworkID string) {
	observability.Logger(ctx, "rooms.pairing", "origin_network_id", originNetworkID).
		Warn("rejecting inbound pair-scoped message from a pair token with no bound network because auth.require_pair_network_binding is enabled")
}

// pairBindingKey identifies the credential a learned binding is pinned to.
// TokenID is the auth.tokens[] id, which pair invite/join mint equal to the
// pairing id (cmd/moltnet/pair_invite.go), so a revoked-and-reminted pairing
// under the same id produces the same key -- acceptable here because revoking
// deletes the token, and a fresh credential legitimately re-learns on its own
// first contact after the restart that reload requires. CredentialKey is the
// fallback for shapes that carry no token id.
func pairBindingKey(claims authn.Claims) string {
	if tokenID := strings.TrimSpace(claims.TokenID); tokenID != "" {
		return "token:" + tokenID
	}
	return strings.TrimSpace(claims.CredentialKey)
}
