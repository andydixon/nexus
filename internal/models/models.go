// Package models defines the core data structures used throughout
// the Nexus Terminal application.
package models

import "time"

// Protocol represents the connection protocol for a session.
type Protocol string

const (
	ProtocolSSH    Protocol = "ssh"
	ProtocolTelnet Protocol = "telnet"
	ProtocolRAW    Protocol = "raw"
	ProtocolGitHub Protocol = "github"
	ProtocolVNC    Protocol = "vnc"
	ProtocolLocal  Protocol = "local"
)

// HostKeyPolicy determines how unknown or changed SSH host keys are handled.
type HostKeyPolicy string

const (
	HostKeyStrict    HostKeyPolicy = "strict"
	HostKeyAsk       HostKeyPolicy = "ask"
	HostKeyAcceptNew HostKeyPolicy = "accept-new"
	HostKeyInsecure  HostKeyPolicy = "insecure"
)

// SecretMode controls how credentials are persisted.
type SecretMode string

const (
	SecretModeSaved        SecretMode = "saved"
	SecretModeSessionOnly  SecretMode = "session-only"
	SecretModeAskEveryTime SecretMode = "ask-every-time"
)

// LineEnding specifies the line-ending convention for Telnet and RAW connections.
type LineEnding string

const (
	LineEndingLF   LineEnding = "lf"
	LineEndingCR   LineEnding = "cr"
	LineEndingCRLF LineEnding = "crlf"
	LineEndingNone LineEnding = "none"
)

// Profile holds the full configuration for a connection profile.
type Profile struct {
	ID             string     `json:"id"`
	DisplayName    string     `json:"displayName"`
	Description    string     `json:"description"`
	Protocol       Protocol   `json:"protocol"`
	Host           string     `json:"host"`
	Port           int        `json:"port"`
	Username       string     `json:"username"`
	Tags           []string   `json:"tags"`
	Folder         string     `json:"folder"`
	Environment    string     `json:"environment"`
	Notes          string     `json:"notes"`
	Favourite      bool       `json:"favourite"`
	LastConnected  *time.Time `json:"lastConnected" ts_type:"string"`
	ConnectTimeout int        `json:"connectTimeout"` // seconds
	ReadTimeout    int        `json:"readTimeout"`    // seconds
	CreatedAt      time.Time  `json:"createdAt" ts_type:"string"`
	UpdatedAt      time.Time  `json:"updatedAt" ts_type:"string"`

	// Terminal preferences
	TerminalFontFamily  string `json:"terminalFontFamily"`
	TerminalFontSize    int    `json:"terminalFontSize"`
	TerminalScrollback  int    `json:"terminalScrollback"`
	TerminalCursorStyle string `json:"terminalCursorStyle"`

	// SSH-specific configuration
	SSH *SSHConfig `json:"ssh,omitempty"`

	// Telnet-specific configuration
	Telnet *TelnetConfig `json:"telnet,omitempty"`

	// RAW-specific configuration
	RAW *RAWConfig `json:"raw,omitempty"`

	// GitHub-specific configuration
	GitHub *GitHubConfig `json:"github,omitempty"`

	// VNC-specific configuration
	VNC *VNCConfig `json:"vnc,omitempty"`
}

// SSHConfig holds SSH-specific settings for a connection profile.
type SSHConfig struct {
	AuthMethods       []string          `json:"authMethods"` // password, publickey, keyboard-interactive
	SecretMode        SecretMode        `json:"secretMode"`
	PrivateKeyPath    string            `json:"privateKeyPath"`
	KeySecretMode     SecretMode        `json:"keySecretMode"`
	HostKeyPolicy     HostKeyPolicy     `json:"hostKeyPolicy"`
	TerminalType      string            `json:"terminalType"`
	KeepaliveInterval int               `json:"keepaliveInterval"`
	KeepaliveCountMax int               `json:"keepaliveCountMax"`
	Compression       bool              `json:"compression"`
	AgentAuth         bool              `json:"agentAuth"`         // use SSH agent for authentication
	AgentForwarding   bool              `json:"agentForwarding"`   // forward agent to remote host
	AutoReconnect     bool              `json:"autoReconnect"`     // reconnect on unexpected disconnect
	MaxReconnectTries int               `json:"maxReconnectTries"` // 0 = use default (5)
	LoginScripts      []LoginScriptStep `json:"loginScripts"`     // expect-style auto-login sequence
	TOTPSecret        string            `json:"totpSecret"`       // TOTP secret key for 2FA auto-fill (stored encrypted)
	ProxyType         string            `json:"proxyType"`        // "none", "socks5", "http"
	ProxyHost         string            `json:"proxyHost"`
	ProxyPort         int               `json:"proxyPort"`
	ProxyUsername     string            `json:"proxyUsername"`
	ProxyPassword     string            `json:"proxyPassword"` // stored in vault for production; inline for simplicity
	JumpHosts         []JumpHost        `json:"jumpHosts"`
	PortForwards      []PortForward     `json:"portForwards"`
	StartupCommand    string            `json:"startupCommand"`
	EnvVars           map[string]string `json:"envVars"`
}

// LoginScriptStep represents a single step in an expect-style auto-login sequence.
type LoginScriptStep struct {
	Type    string `json:"type"`    // "wait" or "send"
	Value   string `json:"value"`   // text to wait for / text to send
	Timeout int    `json:"timeout"` // max seconds to wait (for "wait" steps; 0 = use default 10s)
}

// JumpHost describes a proxy/bastion host used for SSH multi-hop connections.
type JumpHost struct {
	Host           string     `json:"host"`
	Port           int        `json:"port"`
	Username       string     `json:"username"`
	AuthMethod     string     `json:"authMethod"`
	SecretMode     SecretMode `json:"secretMode"`
	PrivateKeyPath string     `json:"privateKeyPath"`
}

// PortForwardDirection indicates the tunnel direction.
type PortForwardDirection string

const (
	PortForwardLocal  PortForwardDirection = "local"
	PortForwardRemote PortForwardDirection = "remote"
)

// PortForward defines a single SSH port-forwarding rule.
type PortForward struct {
	ID        string               `json:"id"`
	Direction PortForwardDirection `json:"direction"`
	BindHost  string               `json:"bindHost"`  // local address to listen on (local) or remote bind (remote)
	BindPort  int                  `json:"bindPort"`
	DestHost  string               `json:"destHost"`  // target address to forward to
	DestPort  int                  `json:"destPort"`
	Enabled   bool                 `json:"enabled"`   // whether to activate on connect
	Label     string               `json:"label"`     // optional descriptive label
}

// TelnetConfig holds Telnet-specific settings for a connection profile.
type TelnetConfig struct {
	LocalEcho         bool              `json:"localEcho"`
	LineEnding        LineEnding        `json:"lineEnding"`
	TerminalType      string            `json:"terminalType"`
	Encoding          string            `json:"encoding"`
	NegotiationDebug  bool              `json:"negotiationDebug"`
	LineMode          bool              `json:"lineMode"`
	AutoReconnect     bool              `json:"autoReconnect"`     // reconnect on unexpected disconnect
	MaxReconnectTries int               `json:"maxReconnectTries"` // 0 = use default (5)
	LoginScripts      []LoginScriptStep `json:"loginScripts"`     // expect-style auto-login sequence
}

// RAWConfig holds RAW TCP-specific settings for a connection profile.
type RAWConfig struct {
	AutoReconnect  bool            `json:"autoReconnect"`
	LineEnding     LineEnding      `json:"lineEnding"`
	Encoding       string          `json:"encoding"`
	ShowTimestamps bool            `json:"showTimestamps"`
	ShowDirection  bool            `json:"showDirection"`
	PayloadPresets []PayloadPreset `json:"payloadPresets"`
}

// PayloadPreset stores a reusable payload for RAW TCP connections.
type PayloadPreset struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Payload string `json:"payload"` // base64-encoded for binary safety
	Format  string `json:"format"`  // text, hex, escaped
}

// GitHubConfig holds GitHub API-specific settings for a connection profile.
type GitHubConfig struct {
	// BaseURL is the GitHub API base (e.g. "https://api.github.com" for
	// github.com, or "https://github.example.com/api/v3" for GHE).
	BaseURL string `json:"baseURL"`

	// SecretMode controls how the personal access token is stored.
	SecretMode SecretMode `json:"secretMode"`

	// RefreshIntervalSecs controls the auto-refresh period in seconds.
	// Defaults to 300 (5 minutes).
	RefreshIntervalSecs int `json:"refreshIntervalSecs"`
}

// VNCConfig holds VNC-specific settings for a connection profile.
type VNCConfig struct {
	// VNCHost is the VNC server address (defaults to profile.Host if empty).
	VNCHost string `json:"vncHost"`

	// VNCPort is the VNC server port (5900 + display number, default 5900).
	VNCPort int `json:"vncPort"`

	// SecretMode controls how the VNC password is stored.
	SecretMode SecretMode `json:"secretMode"`

	// ViewOnly disables sending keyboard and mouse events to the server.
	ViewOnly bool `json:"viewOnly"`

	// ScaleToFit scales the remote framebuffer to fit the panel.
	ScaleToFit bool `json:"scaleToFit"`

	// Quality controls JPEG compression quality (1-9, 0 = default).
	Quality int `json:"quality"`

	// SharedSession allows other VNC viewers to remain connected.
	SharedSession bool `json:"sharedSession"`

	// SSH tunnel settings — if enabled, VNC traffic is tunnelled through an SSH connection.
	SSHTunnel       bool       `json:"sshTunnel"`
	SSHHost         string     `json:"sshHost"`
	SSHPort         int        `json:"sshPort"`
	SSHUsername     string     `json:"sshUsername"`
	SSHAuthMethod   string     `json:"sshAuthMethod"`   // password, publickey
	SSHSecretMode   SecretMode `json:"sshSecretMode"`
	SSHPrivateKey   string     `json:"sshPrivateKeyPath"`
	SSHAgentAuth    bool       `json:"sshAgentAuth"`
}

// GitHubRepo is a single repository returned to the frontend for display.
type GitHubRepo struct {
	FullName    string `json:"fullName"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description"`
	HTMLURL     string `json:"htmlURL"`
	DefaultBranch string `json:"defaultBranch"`
	LatestCommitter string `json:"latestCommitter"`
	LatestCommitAt  string `json:"latestCommitAt"`
	Private     bool   `json:"private"`
	Fork        bool   `json:"fork"`
	Archived    bool   `json:"archived"`
	Language    string `json:"language"`
	Stars       int    `json:"stars"`
	PushedAt    string `json:"pushedAt"` // ISO 8601
	UpdatedAt   string `json:"updatedAt"`
}

// SessionState represents the lifecycle state of a connection session.
type SessionState string

const (
	SessionConnecting   SessionState = "connecting"
	SessionConnected    SessionState = "connected"
	SessionDisconnected SessionState = "disconnected"
	SessionReconnecting SessionState = "reconnecting"
	SessionError        SessionState = "error"
)

// SessionInfo provides metadata about an active or recent session.
type SessionInfo struct {
	ID          string       `json:"id"`
	ProfileID   string       `json:"profileId"`
	ProfileName string       `json:"profileName"`
	Protocol    Protocol     `json:"protocol"`
	Host        string       `json:"host"`
	Port        int          `json:"port"`
	State       SessionState `json:"state"`
	TabName     string       `json:"tabName"`
	Pinned      bool         `json:"pinned"`
	ConnectedAt time.Time    `json:"connectedAt" ts_type:"string"`
	Error       string       `json:"error,omitempty"`
}

// KnownHost records a previously-seen SSH host key fingerprint.
type KnownHost struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	KeyType     string    `json:"keyType"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"publicKey"`
	FirstSeen   time.Time `json:"firstSeen" ts_type:"string"`
	LastSeen    time.Time `json:"lastSeen" ts_type:"string"`
	Trusted     bool      `json:"trusted"`
}

// AppPreferences stores user-level application settings.
type AppPreferences struct {
	Theme               string `json:"theme"`
	TerminalTheme       string `json:"terminalTheme"` // colour scheme name (e.g. "charcoal", "solarized-dark")
	DefaultProtocol     string `json:"defaultProtocol"`
	ConfirmOnClose      bool   `json:"confirmOnClose"`
	ConfirmOnDisconnect bool   `json:"confirmOnDisconnect"`
	ShowStatusBar       bool   `json:"showStatusBar"`
	SidebarWidth        int    `json:"sidebarWidth"`
	DefaultFontFamily   string `json:"defaultFontFamily"`
	DefaultFontSize     int    `json:"defaultFontSize"`
	DefaultScrollback   int    `json:"defaultScrollback"`
	DefaultCursorStyle  string `json:"defaultCursorStyle"`
	UseNativePaste      bool   `json:"useNativePaste"`
	LocalShell          string `json:"localShell"` // path to shell binary for local terminal
}

// AuditEntry records an auditable action within the application.
type AuditEntry struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp" ts_type:"string"`
	Action    string    `json:"action"`
	ProfileID string    `json:"profileId,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Details   string    `json:"details"`
}

// Snippet stores a reusable command for the snippet library.
type Snippet struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Description string    `json:"description"`
	Folder      string    `json:"folder"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"createdAt" ts_type:"string"`
	UpdatedAt   time.Time `json:"updatedAt" ts_type:"string"`
}
