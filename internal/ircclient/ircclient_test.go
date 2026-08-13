package ircclient

import (
	"strings"
	"testing"
)

func TestRememberRoomMembershipTracksConfirmedChannels(t *testing.T) {
	var updates []string
	client := &Client{cfg: Config{
		OnRoomsChanged: func(rooms []string) {
			updates = append(updates, strings.Join(rooms, ","))
		},
	}}

	client.rememberRoomMembership("#One", true)
	client.rememberRoomMembership("#two", true)
	client.rememberRoomMembership("#one", true)
	client.rememberRoomMembership("#missing", false)
	client.rememberRoomMembership("#ONE", false)

	if got, want := strings.Join(updates, "|"), "#One|#One,#two|#two"; got != want {
		t.Fatalf("room updates = %q, want %q", got, want)
	}
	if got := strings.Join(client.roomNames, ","); got != "#two" {
		t.Fatalf("remembered rooms = %q, want %q", got, "#two")
	}
}
