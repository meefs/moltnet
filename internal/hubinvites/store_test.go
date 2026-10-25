package hubinvites

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGrantsAndJoinsPersistAndExpire(t *testing.T) {
	dir := t.TempDir()
	store := Open(dir)
	now := time.Now()
	if err := store.AddGrant(Grant{PairingID: "p1", GlobalRoomID: "g", Role: "writer", Exp: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddGrant(Grant{PairingID: "p2", GlobalRoomID: "g", Role: "reader", Exp: now.Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	reopened := Open(dir)
	if grant, ok, err := reopened.Grant("p1", "g", now); err != nil || !ok || grant.Role != "writer" {
		t.Fatalf("grant = %#v, %v, %v", grant, ok, err)
	}
	if _, ok, _ := reopened.Grant("p2", "g", now); ok {
		t.Fatal("expired grant redeemable")
	}
	if err := reopened.RemoveGrant("p1", "g"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Open(dir).Grant("p1", "g", now); ok {
		t.Fatal("removed grant redeemable")
	}
	if err := store.AddJoin(PendingJoin{PairingID: "p", GlobalRoomID: "g", LocalRoomID: "r"}); err != nil {
		t.Fatal(err)
	}
	if joins, err := Open(dir).Joins(); err != nil || len(joins) != 1 || joins[0].LocalRoomID != "r" {
		t.Fatalf("joins = %#v, %v", joins, err)
	}
	if err := store.RemoveJoin("g"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, fileName)); err != nil || info.Mode().Perm() != fileMode {
		t.Fatalf("file mode = %v, %v", info, err)
	}
}

func TestDamagedInvitesFileFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"v":1,"grants":[{"pairing_id":""}]}`), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(dir).Grant("p", "g", time.Now()); err == nil {
		t.Fatal("damaged file read as empty")
	}
}
