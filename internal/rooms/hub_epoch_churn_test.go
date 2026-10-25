package rooms_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/rooms"
	"github.com/noopolis/moltnet/internal/store"
	"github.com/noopolis/moltnet/pkg/protocol"
)

// Home posts racing control changes never place an older-epoch receipt
// after a newer one (members would refuse it with 409).
func TestHubHomePostsNeverRegressTheEpochUnderControlChurn(t *testing.T) {
	h := joinedTopology(t, memoryBackend)
	hubs := h.home.store.(store.HubStore)
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Operator flips beta's role; every flip bumps the epoch under hubMu.
	wg.Add(1)
	go func() {
		defer wg.Done()
		roles := []string{protocol.RoomRoleAdmin, protocol.RoomRoleWriter}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = h.home.service.AppendHubControlContext(ctx, rooms.HubControlRequest{GlobalRoomID: h.global, Kind: protocol.RoomControlSetRole, NetworkID: "beta", Role: roles[i%2]})
		}
	}()
	// Two home posters race each other and the operator.
	for p := 0; p < 2; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				_, _ = h.home.service.SendMessage(protocol.SendMessageRequest{
					Target: protocol.Target{Kind: protocol.TargetKindRoom, RoomID: "research"},
					From:   protocol.Actor{Type: "agent", ID: "lead"}, Parts: []protocol.Part{{Kind: "text", Text: fmt.Sprintf("p%d-%d", p, i)}},
				})
			}
		}(p)
	}
	deadline := time.After(4 * time.Second)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	go func() {
		select {
		case <-deadline:
		case <-done:
		}
		close(stop)
	}()
	<-done

	page, err := h.home.service.ListRoomMessages("research", "", 2000)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(page.Messages))
	for _, m := range page.Messages {
		ids = append(ids, m.ID)
	}
	receipts, err := hubs.HubReceiptsForMessagesContext(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	type placed struct{ seq, epoch int64 }
	var chain []placed
	for _, record := range receipts {
		receipt, _, err := protocol.VerifyRoomReceipt(record.Receipt, h.home.identity.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, placed{receipt.RoomSeq, receipt.MembershipEpoch})
	}
	sort.Slice(chain, func(i, j int) bool { return chain[i].seq < chain[j].seq })
	regressions := 0
	for i := 1; i < len(chain); i++ {
		if chain[i].epoch < chain[i-1].epoch {
			regressions++
			if regressions <= 3 {
				t.Logf("seq %d at epoch %d follows seq %d at epoch %d", chain[i].seq, chain[i].epoch, chain[i-1].seq, chain[i-1].epoch)
			}
		}
	}
	t.Logf("%d receipts, %d epoch regressions", len(chain), regressions)
	if regressions > 0 {
		t.Fatalf("honest home signed %d receipt(s) with an older membership epoch after a newer one; members refuse these with 409", regressions)
	}
}
