# Nexus Terminal Overview

Nexus Terminal is a desktop connection workspace for people who manage servers, network services, remote desktops, and repositories every day. It brings SSH, Telnet, RAW TCP, VNC, GitHub, and local shell workflows into one focused app with secure credential handling, multi-session organisation, file transfer, automation, and recording built in.

## Product positioning

Nexus is more than an SSH client. It is a single command centre for remote access, terminal operations, file movement, raw socket inspection, GitHub repository visibility, and repeatable operational workflows.

Use Nexus when you need to:

- Connect quickly to many systems without re-entering credentials.
- Keep multiple live sessions organised with tabs, pinned tabs, split panes, and status indicators.
- Move files over SSH without leaving the terminal workspace.
- Work safely with host key verification, encrypted secrets, TOTP support, and audit logging.
- Reuse commands, automate login flows, record sessions, and react to terminal output.
- Support mixed estates that include SSH servers, Telnet devices, raw TCP services, VNC desktops, GitHub repositories, and local shells.

## Main feature groups

### Multi-protocol connections

- **SSH** for secure remote terminal access.
- **Telnet** for legacy devices and services.
- **RAW TCP** for direct socket interaction, protocol testing, and device troubleshooting.
- **VNC** for remote desktop access.
- **GitHub** for repository browsing and download workflows.
- **Local terminal** for opening a shell on the local machine.
- **Quick connect** for one-off sessions without saving a profile.
- **Saved profiles** for repeat connections.

### Profile management

- Create, edit, duplicate, and delete connection profiles.
- Store display names, descriptions, notes, groups, tags, environment labels, and favourites.
- Search profiles by name, host, or tag.
- Filter profiles by protocol, group, tag, and favourite status.
- Test basic host and port reachability before connecting.
- Import existing OpenSSH config entries from `~/.ssh/config`, including hostnames, usernames, ports, identity files, and jump hosts.
- Export and import profiles using encrypted `.nexus` bundles.
- Choose import behaviour for duplicates: skip, overwrite, or duplicate.

### Multi-session workspace

- Tabbed sessions with connection state indicators.
- Pin tabs to keep important systems visible.
- Rename sessions.
- Reorder tabs.
- Close one session or all sessions.
- Split panes vertically or horizontally.
- Resize split panes.
- Switch tabs by keyboard shortcut.
- Broadcast input to multiple connected sessions.
- Show disconnected overlays with a one-click reconnect action.
- Auto-reconnect support for SSH, Telnet, and RAW TCP profiles.
- Command palette for fast access to connections, settings, terminal actions, security tools, import/export, snippets, triggers, and local terminal.

### Terminal experience

- xterm.js-powered terminal rendering.
- Configurable terminal font family, font size, scrollback, cursor style, and colour theme.
- Search within terminal output.
- Search options for case sensitivity, regular expressions, and whole-word matching.
- Copy, paste, select all, clear, and reset from the terminal context menu.
- Clipboard-aware paste behaviour.
- Clickable web links where supported.
- Alternate-screen scroll handling for tools such as `tmux`, `screen`, and `byobu`.
- Export terminal scrollback as plain text.
- Send break signals where supported.
- Ping active sessions to check responsiveness.

### SSH capabilities

- Password authentication.
- Public key authentication.
- Keyboard-interactive authentication.
- SSH agent authentication.
- SSH agent forwarding.
- Private key path discovery from `~/.ssh`.
- SSH key manager for listing, generating, copying, and deleting key pairs.
- Ed25519, RSA, and ECDSA key generation.
- Optional private key passphrase handling.
- TOTP / 2FA secret storage and code generation for keyboard-interactive login flows.
- Host key verification policies per profile: Strict, Ask, Accept new, and Insecure.
- Known hosts storage and management.
- Jump host support for bastion and multi-hop access.
- SOCKS5 proxy support.
- HTTP CONNECT proxy support.
- Local and remote port forwarding.
- Runtime port forward management for active SSH sessions.
- Startup commands.
- Custom environment variables.
- Terminal type configuration.
- Keepalive interval and missed-keepalive settings.
- Compression option.
- Expect-style login scripts with wait and send steps.
- Built-in SFTP browser for file transfer.

### SFTP file browser

- Open a side panel for SSH file browsing.
- Navigate remote folders with breadcrumbs.
- Go directly to a remote path.
- Bookmark remote locations.
- Refresh remote listings.
- Create folders.
- Upload files.
- Download single files or multiple selected files.
- Rename files and folders.
- Delete files and folders.
- Multi-select support for batch actions.
- Resizable SFTP panel alongside the terminal.
- Drag and drop files into an SSH terminal.
- Choose whether dropped files are uploaded or pasted into the terminal as text.
- Upload directory picker with folder filtering, breadcrumbs, keyboard navigation, and permission badges.
- Permission labels for read-only and inaccessible folders.

### Telnet capabilities

- Telnet option negotiation.
- Terminal type negotiation.
- NAWS window-size updates.
- Local echo option.
- Line mode option.
- Configurable line endings: LF, CR, CRLF, or none.
- Configurable encoding: UTF-8, ASCII, and ISO 8859-1.
- Negotiation debug mode.
- Expect-style login scripts.
- Auto-reconnect with configurable retry count.
- Break signal support.

### RAW TCP capabilities

- Direct TCP socket sessions.
- Text, hex, and split data views.
- Send data as text, hex, or escaped bytes.
- Quick insert controls for common bytes such as NUL, CR, LF, ETX, and EOT.
- Payload presets for reusable test messages.
- Optional timestamps.
- Optional send/receive direction labels.
- Pause and resume display updates.
- Clear the capture buffer.
- Save RAW session captures.
- Configurable encoding and line endings.
- Connect and read timeouts.
- Auto-reconnect option.

### VNC capabilities

- VNC remote desktop sessions.
- noVNC-based viewer inside the Nexus workspace.
- Password prompt and password storage modes.
- View-only mode.
- Scale-to-fit display.
- Shared-session option.
- Quality setting.
- SSH tunnel option for VNC traffic.
- SSH tunnel authentication by password, public key, or SSH agent.

### GitHub capabilities

- GitHub profile type using a personal access token.
- GitHub.com and GitHub Enterprise API base URL support.
- Repository viewer grouped by owner or organisation.
- Repository filtering by name, description, or language.
- Repositories sorted by latest commit activity.
- Shows repository metadata such as language, stars, private/fork/archived state, latest committer, and latest commit time.
- Manual refresh.
- Configurable refresh interval.
- Open repositories in the browser.
- Download repository ZIP archives.

### Automation and reusable workflows

- Snippet library for saved commands.
- Snippet folders, descriptions, and tags.
- Send snippets to active sessions.
- Snippet variables before sending commands.
- Broadcast input mode for running the same command across connected sessions.
- Trigger rules that watch terminal output.
- Trigger matching by text or regular expression.
- Trigger actions for notifications, tab highlighting, sound, or combined notify/highlight behaviour.
- Expect-style login scripts for SSH and Telnet.
- Startup commands for SSH sessions.

### Session recording

- Start and stop terminal recording per session.
- Recording indicator on active tabs.
- Recordings saved in asciicast v2-compatible format.
- Recording browser for listing saved recordings.
- Playback controls.
- Delete saved recordings.
- Recording metadata includes title, dimensions, duration, size, creation time, and file path.

### Security and credential handling

- OS keychain first where available, including macOS Keychain, Linux Secret Service, and Windows Credential Manager.
- Encrypted vault fallback.
- Vault encryption with AES-256-GCM and Argon2id-derived keys.
- Secret store lock and unlock workflow.
- Per-profile secret modes: Saved, Session only, and Ask every time.
- Encrypted storage for TOTP secrets.
- Known host verification and known host management.
- Audit log for security-relevant operations.
- Audit entries for connections, disconnections, profile changes, secret access, imports, and exports.
- Secrets are not written into audit entries.
- Encrypted profile exports with password-based AES-256-GCM encryption and Argon2id key derivation.
- Plain profile export can redact inline secrets.

### Settings and maintenance

- Application theme setting.
- Terminal theme selection.
- Default protocol preference.
- Default terminal font, size, scrollback, and cursor settings.
- Confirm-on-close and confirm-on-disconnect preferences.
- Status bar visibility setting.
- Sidebar width preference.
- Native paste preference.
- Local shell selection.
- Secret store settings.
- Known hosts management.
- Help and troubleshooting content inside settings.
- Background and manual update checks.
- Update checks report availability, current version, latest version, release notes, release URL, and publish date.

## Website-ready summary

### Suggested headline

Nexus Terminal: one desktop workspace for SSH, files, sockets, remote desktops, and repositories.

### Suggested subheadline

Manage SSH, Telnet, RAW TCP, VNC, GitHub, and local shell sessions from a fast tabbed interface with secure credentials, SFTP file transfer, split panes, snippets, recordings, and automation built in.

### Short product description

Nexus Terminal helps developers, operators, support engineers, and homelab users manage many connections without juggling separate tools. Save profiles, organise them with groups and tags, connect in tabs or split panes, move files over SFTP, inspect raw TCP traffic, open VNC desktops, browse GitHub repositories, and keep credentials protected with keychain and encrypted-vault storage.

### Product page feature bullets

- All-in-one connection workspace for SSH, Telnet, RAW TCP, VNC, GitHub, and local shells.
- Fast multi-session interface with tabs, pinned sessions, split panes, reconnect, and broadcast input.
- Rich SSH support including agent auth, TOTP, jump hosts, proxies, port forwarding, startup commands, login scripts, and host key policies.
- Built-in SFTP browser with upload, download, rename, delete, folders, bookmarks, drag-and-drop upload, and permission-aware directory picking.
- RAW TCP tooling with text, hex, split views, payload presets, captures, timestamps, and byte-level sending.
- VNC viewer with scale-to-fit, view-only mode, quality controls, shared sessions, and optional SSH tunnelling.
- GitHub repository browser with organisation grouping, filtering, activity sorting, metadata, ZIP downloads, and GitHub Enterprise support.
- Secure credential model using OS keychain first, encrypted vault fallback, per-profile secret modes, known hosts, encrypted exports, and audit logging.
- Productivity tools including command palette, snippets, trigger rules, session recording, terminal search, scrollback export, and local terminal.

## Who it is for

- Developers who connect to many application servers, staging environments, and production hosts.
- Platform and DevOps teams who need organised, secure access across infrastructure.
- Support teams who troubleshoot customer systems, network appliances, and legacy Telnet devices.
- Network and hardware engineers who need RAW TCP and Telnet tools beside normal SSH workflows.
- Homelab users who want SSH, VNC, file transfer, and repo access in one desktop app.

## Learn more

For a short guide, see the quick start: [docs/quick-start.md](quick-start.md).

For a full guide, see the user manual: [docs/user-manual.md](user-manual.md).
