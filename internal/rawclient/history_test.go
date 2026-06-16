package rawclient

import (
	"bytes"
	"testing"

	"github.com/andydixon/nexus/internal/models"
)

func TestHistoryIsCappedByEntryCount(t *testing.T) {
	client := NewClient(&models.Profile{})

	for i := 0; i < maxHistoryEntries+25; i++ {
		client.appendHistory(HistoryEntry{Direction: "recv", Data: []byte{byte(i)}})
	}

	history := client.GetHistory()
	if len(history) != maxHistoryEntries {
		t.Fatalf("history length = %d, want %d", len(history), maxHistoryEntries)
	}
}

func TestHistoryIsCappedByBytes(t *testing.T) {
	client := NewClient(&models.Profile{})
	chunk := bytes.Repeat([]byte("a"), 1024)

	for i := 0; i < (maxHistoryBytes/len(chunk))+10; i++ {
		client.appendHistory(HistoryEntry{Direction: "recv", Data: chunk})
	}

	total := 0
	for _, entry := range client.GetHistory() {
		total += len(entry.Data)
	}
	if total > maxHistoryBytes {
		t.Fatalf("history bytes = %d, want <= %d", total, maxHistoryBytes)
	}
}

func TestGetHistoryReturnsDeepCopy(t *testing.T) {
	client := NewClient(&models.Profile{})
	client.appendHistory(HistoryEntry{Direction: "recv", Data: []byte("secret")})

	history := client.GetHistory()
	history[0].Data[0] = 'X'

	again := client.GetHistory()
	if string(again[0].Data) != "secret" {
		t.Fatalf("history was mutated through returned slice: %q", string(again[0].Data))
	}
}
