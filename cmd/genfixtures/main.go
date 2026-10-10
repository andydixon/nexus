// Command genfixtures writes the Go-generated compatibility fixtures that the
// Rust port must read unchanged. Run from the repo root:
//
//	go run ./cmd/genfixtures rust/core/tests/fixtures
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/andydixon/link/internal/exportimport"
	"github.com/andydixon/link/internal/models"
	"github.com/andydixon/link/internal/recording"
	"github.com/andydixon/link/internal/secrets"
	"github.com/andydixon/link/internal/storage"
)

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	out := os.Args[1]
	must(os.RemoveAll(out))
	must(os.MkdirAll(out, 0o755))

	store, err := storage.NewStore(filepath.Join(out, "link.db"))
	must(err)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var profiles []models.Profile
	for i, proto := range []models.Protocol{models.ProtocolSSH, models.ProtocolTelnet, models.ProtocolRAW, models.ProtocolGitHub, models.ProtocolVNC, models.ProtocolIRC} {
		p := models.NewProfile(proto)
		p.ID = "00000000-0000-0000-0000-00000000000" + string(rune('1'+i))
		p.DisplayName = "Fixture " + string(proto)
		p.Host = "host-" + string(proto) + ".example"
		p.Tags = []string{"fixture", string(proto)}
		p.Folder = "Fixtures"
		p.Notes = "notes ✓"
		p.CreatedAt, p.UpdatedAt = ts, ts
		switch proto {
		case models.ProtocolSSH:
			p.Username = "alice"
			p.Favourite = true
			p.LastConnected = &ts
			p.SSH.PrivateKeyPath = "/home/alice/.ssh/id_ed25519"
			p.SSH.JumpHosts = []models.JumpHost{{Host: "jump.example", Port: 2222, Username: "bob", AuthMethod: "publickey", SecretMode: models.SecretModeSaved}}
			p.SSH.PortForwards = []models.PortForward{{ID: "pf1", Direction: models.PortForwardLocal, BindHost: "127.0.0.1", BindPort: 8080, DestHost: "localhost", DestPort: 80, Enabled: true, Label: "web"}}
			p.SSH.EnvVars = map[string]string{"LANG": "en_GB.UTF-8"}
			p.SSH.LoginScripts = []models.LoginScriptStep{{Type: "wait", Value: "$ ", Timeout: 5}, {Type: "send", Value: "uptime"}}
			p.SSH.ProxyType = "socks5"
			p.SSH.ProxyHost = "proxy.example"
			p.SSH.ProxyPort = 1080
			p.SSH.AutoReconnect = true
		case models.ProtocolRAW:
			p.RAW.PayloadPresets = []models.PayloadPreset{{ID: "pp1", Name: "hello", Payload: "aGVsbG8=", Format: "text"}}
		case models.ProtocolIRC:
			p.IRC.AutoJoinRooms = []string{"#rust", "#go"}
			p.IRC.UseTLS = true
		}
		must(store.CreateProfile(&p))
		profiles = append(profiles, p)
	}
	must(store.SaveKnownHost(&models.KnownHost{ID: "kh1", Host: "host-ssh.example", Port: 22, KeyType: "ssh-ed25519", Fingerprint: "SHA256:abc", PublicKey: "AAAA", FirstSeen: ts, LastSeen: ts, Trusted: true}))
	must(store.CreateSnippet(&models.Snippet{ID: "sn1", Name: "uptime", Command: "uptime", Description: "load", Folder: "ops", Tags: []string{"x"}, CreatedAt: ts, UpdatedAt: ts}))
	must(store.SaveIRCLastRooms(profiles[5].ID, []string{"#rust", "#nexus"}))
	prefs := models.DefaultAppPreferences()
	prefs.TerminalTheme = "dracula"
	prefs.SidebarWidth = 320
	must(store.SavePreferences(&prefs))
	must(store.Close())

	vault := secrets.NewSecretStore(filepath.Join(out, "vault"))
	must(vault.Initialise("correct horse battery staple"))
	must(vault.SetSecret("profile:00000000-0000-0000-0000-000000000001:password", "s3cret-ssh"))
	must(vault.SetSecret("profile:00000000-0000-0000-0000-000000000001:totp", "JBSWY3DPEHPK3PXP"))

	must(exportimport.EncryptAndExport(profiles, "export-pass", filepath.Join(out, "profiles.link")))

	rec, err := recording.NewRecorder(filepath.Join(out, "recordings"), "rec1", "Fixture — host", 80, 24)
	must(err)
	rec.WriteOutput([]byte("hello \x1b[1mworld\x1b[0m\r\n"))
	rec.WriteInput([]byte("ls\r"))
	must(rec.Close())
	log.Println("fixtures written to", out)
}
