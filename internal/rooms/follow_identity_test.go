package rooms

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/noopolis/moltnet/pkg/protocol"
)

func TestFollowRelayedAuthorAndOriginRemainUntrusted(t *testing.T) {
	service, config := followTestService(t)
	remote := protocol.Message{ID: "served", Origin: protocol.MessageOrigin{NetworkID: "peer", MessageID: "claimed", ReceivedVia: "forged"}, Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "public"}, From: protocol.Actor{Type: "agent", ID: "alice", NetworkID: "peer", FQID: "molt://peer/agents/alice", CredentialBound: true}, CreatedAt: time.Now().UTC()}
	if _, err := service.ImportFollowMessage(t.Context(), config, "origin", remote); err != nil {
		t.Fatal(err)
	}
	other := remote
	other.ID = "origin-alice"
	other.From.NetworkID = "origin"
	other.From.FQID = "molt://origin/agents/alice"
	if _, err := service.ImportFollowMessage(t.Context(), config, "origin", other); err != nil {
		t.Fatal(err)
	}
	page, err := service.ListRoomMessages("source.public", "", 100)
	if err != nil || len(page.Messages) != 2 {
		t.Fatalf("history: %+v %v", page, err)
	}
	m := page.Messages[0]
	if m.From.NetworkID == "origin" || m.From.NetworkID == "peer" || m.From.NetworkID == "local" || m.From.CredentialBound {
		t.Fatalf("claimed identity trusted: %+v", m.From)
	}
	if m.From.FQID == page.Messages[1].From.FQID {
		t.Fatal("relayed and origin alice conflated")
	}
	if m.Origin.NetworkID != m.From.NetworkID || m.Origin.MessageID != "served" || m.Origin.ReceivedVia != "follow:source" {
		t.Fatalf("claimed origin trusted: %+v", m.Origin)
	}
	var raw map[string]any
	data, _ := json.Marshal(m.Origin)
	_ = json.Unmarshal(data, &raw)
	if raw["claimed_network_id"] != "peer" || raw["claimed_message_id"] != "claimed" || raw["claimed_discovery_network_id"] != "origin" || m.Origin.ClaimedAuthor == nil || *m.Origin.ClaimedAuthor != remote.From {
		t.Fatalf("claims lost: %s", data)
	}
	// Discovery lies (including the local network ID) cannot change identity.
	if added, err := service.ImportFollowMessage(t.Context(), config, "local", remote); err != nil || added {
		t.Fatalf("discovery affects identity: %v %v", added, err)
	}
}
