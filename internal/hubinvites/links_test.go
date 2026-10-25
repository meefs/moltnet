package hubinvites

import (
	"testing"
	"time"
)

func TestLinksPersistPerRoomAndReplacePerNetwork(t *testing.T) {
	for name, open := range map[string]func(t *testing.T) (*Store, func() *Store){
		"memory": func(*testing.T) (*Store, func() *Store) { s := NewMemory(); return s, func() *Store { return s } },
		"file": func(t *testing.T) (*Store, func() *Store) {
			dir := t.TempDir()
			return Open(dir), func() *Store { return Open(dir) }
		},
	} {
		t.Run(name, func(t *testing.T) {
			store, reopen := open(t)
			for _, link := range []Link{{PairingID: "room-a", GlobalRoomID: "ga", NetworkID: "alpha"}, {PairingID: "room-b", GlobalRoomID: "gb", NetworkID: "alpha"},
				{PairingID: "friend", GlobalRoomID: "ga", NetworkID: "alpha"}} {
				if err := store.SetLink(link); err != nil {
					t.Fatal(err)
				}
			}
			links, err := reopen().Links("ga")
			if err != nil || len(links) != 1 || links[0].PairingID != "friend" {
				t.Fatalf("room ga links = %#v, %v (a re-link must replace the network's earlier link)", links, err)
			}
			if links, _ := reopen().Links("gb"); len(links) != 1 || links[0].PairingID != "room-b" {
				t.Fatalf("room gb links = %#v", links)
			}
		})
	}
}

func TestPendingJoinUpdateKeepsTerminalStatus(t *testing.T) {
	store := Open(t.TempDir())
	exp := time.Now().Add(time.Hour).UTC().Round(time.Second)
	if err := store.AddJoin(PendingJoin{PairingID: "p", GlobalRoomID: "g", Exp: exp}); err != nil {
		t.Fatal(err)
	}
	joins, _ := store.Joins()
	if len(joins) != 1 || !joins[0].Pending() || !joins[0].Exp.Equal(exp) {
		t.Fatalf("joins = %#v", joins)
	}
	joins[0].Status, joins[0].LastError = JoinExpired, "invite expired"
	if err := store.UpdateJoin(joins[0]); err != nil {
		t.Fatal(err)
	}
	if joins, _ := store.Joins(); len(joins) != 1 || joins[0].Pending() || joins[0].Status != JoinExpired {
		t.Fatalf("joins after update = %#v", joins)
	}
}
