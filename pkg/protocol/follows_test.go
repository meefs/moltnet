package protocol

import "testing"

func TestFollowDefaultsAndMapping(t *testing.T) {
	f := Follow{ID: "news", RemoteBaseURL: "https://example.com"}.WithDefaults()
	if f.BackfillLimit() != 200 || f.Visibility != "private" || f.PollIntervalSeconds != 30 || f.ReconnectMinSeconds != 1 || f.ReconnectMaxSeconds != 60 {
		t.Fatalf("defaults = %#v", f)
	}
	if f.LocalRoomID("general") != "news.general" {
		t.Fatal("incorrect default mapping")
	}
	zero := 0
	f.Backfill = &zero
	f.RoomMapping = map[string]string{"general": "archive"}
	if f.BackfillLimit() != 0 || f.LocalRoomID("general") != "archive" {
		t.Fatal("explicit options ignored")
	}
}

func TestFollowNamespaceCollisions(t *testing.T) {
	base := Follow{ID: "news", RemoteBaseURL: "https://example.com"}
	for _, other := range []Follow{
		{ID: "news.remote", RemoteBaseURL: "https://other.example"},
		{ID: "other", RemoteBaseURL: "https://other.example", Rooms: []string{"general"}, RoomMapping: map[string]string{"general": "news.general"}},
	} {
		if err := ValidateFollows([]Follow{base, other}, nil); err == nil {
			t.Fatalf("accepted colliding follow %#v", other)
		}
	}
	if err := ValidateFollows([]Follow{base}, []string{"local"}); err != nil {
		t.Fatal(err)
	}
}
