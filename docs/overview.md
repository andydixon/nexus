# Nexus Terminal Overview

Nexus Terminal is a native desktop connection workspace for people who manage servers, network services, serial devices, remote desktops, chat networks and repositories every day. It brings SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local shell sessions into one focused app with secure credential handling, multi-session organisation, file transfer, automation and recording built in.

It is a single Rust application drawn with egui: there is no webview, no bundled browser engine and no runtime to install. Terminal output goes straight from the network into the terminal emulator, so large outputs stay responsive.

## Product positioning

Nexus Terminal is more than an SSH client. It is a single command centre for remote access, terminal operations, file movement, raw socket inspection, serial consoles, IRC, GitHub repository visibility and repeatable operational workflows.

Use Nexus Terminal when you need to:

- Connect quickly to many systems without re-entering credentials.
- Keep multiple live sessions organised with tabs, pinned tabs, split panes and status indicators.
- Move files over SSH without leaving the terminal workspace.
- Work safely with host key verification, encrypted secrets, TOTP support and audit logging.
- Reuse commands, automate login flows, record sessions and react to terminal output.
- Support mixed estates that include SSH servers, Telnet devices, raw TCP services, serial consoles, VNC desktops, IRC networks, GitHub repositories and local shells.

## Main feature groups

### Multi-protocol connections

- **SSH** for secure remote terminal access.
- **Telnet** for legacy devices and services.
- **RAW TCP** for direct socket interaction, protocol testing and device troubleshooting.
- **Serial** for USB-serial adapters, console cables, development boards and network gear.
- **VNC** and **RDP** for remote desktop access, with native viewers.
- **IRC** for chat networks, with rooms, users and an optional SSH tunnel.
- **GitHub** for repository browsing and download workflows.
- **Local terminal** for opening a shell on the local machine.
- **Quick connect** for one-off SSH, Telnet, RAW, IRC or Serial sessions without saving a profile.
- **Saved profiles** for repeat connections.

### Profile management

- Create, edit, duplicate and delete connection profiles.
- Store display names, descriptions, notes, groups, tags and favourites.
- Fuzzy search across name, host, tags and protocol.
- Sidebar sections for Local Machine, Favourites, Recent (last five), groups and Ungrouped.
- Multi-select profiles with Ctrl/Cmd-click for bulk connect, disconnect, tagging, moving between groups and deleting.
- Test basic host and port reachability before connecting.
- Import existing OpenSSH config entries from `~/.ssh/config`, including hostnames, usernames, ports, identity files and jump hosts.
- Export and import profiles using encrypted `.nexus` bundles.
- Plain JSON export and import, with secrets redacted unless you choose to include them.
- Choose import behaviour for duplicates: skip, overwrite or duplicate.

### Multi-session workspace

- Custom title bar with File, Session, View and Help menus, a command palette launcher and a light/dark toggle.
- Tabbed sessions with protocol badges, connection state dots and activity markers.
- Pin tabs to keep important systems at the front.
- Rename, duplicate, reconnect and reorder tabs (drag, or Move Left/Right).
- Scroll overflowing tabs and hover them to see the host, state and connection start time.
- Split the active tab right or down, as many times as you like, with draggable dividers.
- Switch tabs by keyboard shortcut.
- Broadcast input to all connected terminal sessions, or to a chosen set.
- Disconnected and error overlays with one-click reconnect.
- Auto-reconnect with exponential backoff for SSH, Telnet, RAW TCP, Serial and IRC profiles.
- Guided error dialogs for authentication and host-key failures, with shortcuts to edit the profile or open Known Hosts.
- Command palette for connections, tabs, new profiles, settings, import/export, themes, the secret store, SSH keys, snippets, triggers, recordings and terminal actions.
- Status bar with session state, protocol, recording indicator, SSH latency, broadcast state, vault state, session count and update notices.

### Terminal experience

- Native terminal emulation (alacritty_terminal) with full colour, mouse reporting, bracketed paste and IME input.
- Eleven terminal colour schemes, applied to every open terminal at once.
- Configurable font family, font size, scrollback and cursor style, per profile or as defaults.
- Find in terminal output, with case-sensitive and regular-expression options and a match counter.
- Copy with Ctrl/Cmd+C when text is selected (otherwise it sends an interrupt), paste with Ctrl/Cmd+V.
- Double-click selects a word, triple-click selects a line, Shift-click extends a selection.
- Ctrl/Cmd-click a web link to open it in your browser.
- Shift+PageUp/PageDown/Home/End and the mouse wheel scroll the scrollback; on the alternate screen the wheel sends arrow keys, so `tmux`, `screen` and `byobu` scroll naturally.
- Context menu with Copy, Paste, Select All, Find, Send Snippet, Port Forwards, Split Right, Split Down, Export Output, Clear and Reset.
- Export the whole scrollback as plain text.
- Status-bar latency for the active SSH session.

### SSH capabilities

- Password, public key, keyboard-interactive and SSH agent authentication.
- SSH agent forwarding.
- Private key suggestions from `~/.ssh`, with a key file check.
- SSH key manager for listing, generating, copying and deleting key pairs (Ed25519, RSA 2048/4096 and ECDSA, optional passphrase).
- TOTP / 2FA secret storage and automatic code entry for keyboard-interactive logins.
- Host key verification policies per profile: Strict, Ask, Accept new and Insecure.
- Known hosts storage and management.
- Jump hosts for bastion and multi-hop access.
- SOCKS5 and HTTP CONNECT proxies.
- Local and remote port forwarding, started on connect or managed at runtime.
- Startup commands, custom environment variables and terminal type.
- Keepalive interval and missed-keepalive settings, and compression.
- Expect-style login scripts with wait and send steps.
- Built-in SFTP browser for file transfer.

### SFTP file browser

- Opens in a resizable dock beside the terminal.
- Navigate remote folders with breadcrumbs, or go directly to a path.
- Bookmark remote locations (kept per profile).
- Refresh listings and create folders.
- Upload files, and download single or multiple selected files.
- Rename and delete files and folders.
- Multi-select for batch actions.
- Transfer queue with progress.
- Drag and drop files onto an SSH terminal, then upload them or paste their contents.
- Upload directory picker with folder filtering, breadcrumbs, keyboard navigation and READ ONLY / NO READ badges.

### Telnet capabilities

- Telnet option negotiation, including echo, terminal type and NAWS window-size updates.
- Local echo and line mode options.
- Configurable line endings: LF, CR, CRLF or none.
- Configurable encoding: UTF-8, ASCII and ISO 8859-1.
- Negotiation debug mode.
- Expect-style login scripts.
- Auto-reconnect with a configurable retry count.

### RAW TCP capabilities

- Direct TCP socket sessions.
- Text, hex and split data views.
- Send data as text, hex or escaped bytes.
- Quick insert buttons for NUL, CR, LF, ETX and EOT.
- Payload presets for reusable test messages.
- Optional timestamps and send/receive direction labels.
- Pause and resume display updates, and clear the capture buffer.
- Save RAW session captures as JSON.
- Configurable encoding and line endings.
- Connect and read timeouts.
- Auto-reconnect option.

### Serial capabilities

- Pick a device from the detected port list, or type a path such as `/dev/ttyUSB0`, `/dev/cu.usbserial-…` or `COM3`.
- Baud rate from common presets or any custom value, 5–8 data bits, none/odd/even parity, 1 or 2 stop bits.
- Flow control: none, XON/XOFF (software) or RTS/CTS (hardware).
- Line ending, encoding and local echo.
- Assert DTR and RTS on connect (both on by default).
- Auto-reconnect: unplugging the adapter disconnects the session, and the port is reopened when it comes back.
- Clear hints for permission-denied and busy ports.
- Works with everything a terminal session supports: recording, triggers, broadcast, snippets, find and split panes.

### RDP capabilities

- Native RDP viewer (IronRDP) with Network Level Authentication (CredSSP/NTLM) and optional TLS-only logon for servers such as xrdp.
- Server certificate pinned by fingerprint in Known Hosts, checked before credentials are sent.
- Direct, SOCKS5, HTTP CONNECT, SSH-tunnelled or (experimental) RD Gateway connections.
- Scale-to-fit or 1:1, a fixed resolution or one that follows the tab (Display Control).
- Keyboard (scancodes for keys and shortcuts, Unicode for text), mouse and wheel; Ctrl+Alt+Del; text clipboard sharing in both directions.
- Password storage modes and domain logon.
- Remote audio playback, a printer whose jobs are saved as PostScript files, and a shared folder redirected as a drive (Linux and macOS), confined to that folder.

### VNC capabilities

- Native VNC viewer inside the Nexus Terminal workspace (RFB 3.3–3.8; Raw, CopyRect, Hextile, ZRLE and Tight encodings).
- Password prompt and password storage modes.
- View-only mode.
- Scale-to-fit or 1:1 display, switchable from the toolbar.
- Shared-session option and a quality setting.
- Send the local clipboard to the remote desktop, send Ctrl+Alt+Del, and request a full refresh.
- SSH tunnel option for VNC traffic, with password, public key or SSH agent authentication.
- A warning before connecting without a tunnel, because classic VNC traffic is not encrypted.

### IRC capabilities

- Nick, real name, TLS and an optional server password.
- NickServ identification on connect.
- Auto-join rooms.
- A Server tab plus one tab per room or private conversation, with unread markers.
- Slash commands such as `/join`, `/part`, `/msg`, `/me`, `/nick`, `/topic`, `/list`, `/names`, `/whois` and `/raw`.
- Tab nick completion, and click a nick to mention it.
- Room browser with a filter, and a users list for the current room.
- mIRC colour and formatting codes rendered in the conversation.
- Optional SSH tunnel and auto-reconnect.

### GitHub capabilities

- GitHub profile type using a personal access token.
- GitHub.com and GitHub Enterprise API base URL support.
- Repository viewer grouped by owner or organisation.
- Repository filtering.
- Repositories sorted by latest commit activity.
- Repository metadata such as language, stars, private/fork/archived state, latest committer and latest commit time.
- Manual refresh and a configurable refresh interval.
- Open repositories in the browser.
- Download repository ZIP archives.

### Automation and reusable workflows

- Snippet library for saved commands, with names, descriptions and folders.
- Send snippets to the active session.
- Built-in snippet variables (`{{hostname}}`, `{{date}}`, `{{time}}`, `{{timestamp}}`) and prompted custom variables.
- Broadcast input mode for running the same command across connected sessions.
- Trigger rules that watch terminal output, matching plain text or regular expressions.
- Trigger actions for notifications, tab highlighting, both, or a sound (attention request).
- Expect-style login scripts for SSH and Telnet.
- Startup commands for SSH sessions.

### Session recording

- Start and stop terminal recording per session.
- Recording indicator on the tab and in the status bar.
- Recordings saved in asciicast v2 format.
- Recording browser with storage usage and clean-up by age or total size.
- Playback with play/pause, restart, a seek bar and speed control.
- Delete saved recordings.

### Security and credential handling

- OS keychain first where available: macOS Keychain, Linux Secret Service and Windows Credential Manager.
- Encrypted vault fallback, using AES-256-GCM with an Argon2id-derived key.
- Secret store lock and unlock workflow, with a master-password strength meter.
- Per-credential secret modes: Save securely, Remember for this session, and Ask every time.
- Encrypted storage for TOTP secrets.
- Known host verification and known host management.
- Audit log of connections, disconnections, profile changes, credential access, imports and exports. Secrets are never written to it.
- Encrypted profile exports with password-based AES-256-GCM encryption and Argon2id key derivation.

### Settings and maintenance

- App theme (Pro, Midnight or Light) and terminal colour scheme.
- Default protocol preference.
- Default terminal font, size, scrollback and cursor settings.
- Confirm-on-close and confirm-on-disconnect preferences.
- Status bar visibility and sidebar width.
- Local shell selection.
- Secret store settings.
- Known hosts management.
- Help topics and troubleshooting inside Settings.
- A background update check at launch (can be turned off in Settings → General), and a manual check in Settings → About.

## Website-ready summary

### Suggested headline

Nexus Terminal: one native desktop workspace for SSH, files, sockets, serial consoles, remote desktops, chat and repositories.

### Suggested subheadline

Manage SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local shell sessions from a fast tabbed interface with secure credentials, SFTP file transfer, split panes, snippets, recordings and automation built in.

### Short product description

Nexus Terminal helps developers, operators, support engineers, network and hardware engineers, and homelab users manage many connections without juggling separate tools. Save profiles, organise them with groups and tags, connect in tabs or split panes, move files over SFTP, inspect raw TCP traffic, talk to serial devices, open VNC desktops, join IRC networks, browse GitHub repositories, and keep credentials protected with keychain and encrypted-vault storage.

### Product page feature bullets

- All-in-one connection workspace for SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local shells.
- Native and fast: no webview, with a terminal that stays responsive under heavy output.
- Multi-session interface with tabs, pinned sessions, split panes, reconnect and broadcast input.
- Rich SSH support including agent auth and forwarding, TOTP, jump hosts, proxies, port forwarding, startup commands, login scripts and host key policies.
- Built-in SFTP browser with upload, download, rename, delete, folders, bookmarks, drag-and-drop upload and permission-aware directory picking.
- RAW TCP tooling with text, hex and split views, payload presets, captures, timestamps and byte-level sending.
- Serial consoles with full line settings, flow control, DTR/RTS control and auto-reconnect on replug.
- Native VNC viewer with scale-to-fit, view-only mode, quality controls, shared sessions and optional SSH tunnelling.
- IRC client with rooms, nick completion, NickServ and mIRC formatting.
- GitHub repository browser with organisation grouping, filtering, activity sorting, ZIP downloads and GitHub Enterprise support.
- Secure credential model using the OS keychain first, encrypted vault fallback, per-credential secret modes, known hosts, encrypted exports and audit logging.
- Productivity tools including command palette, snippets, trigger rules, session recording, terminal search, scrollback export and local terminal.

## Who it is for

- Developers who connect to many application servers, staging environments and production hosts.
- Platform and DevOps teams who need organised, secure access across infrastructure.
- Support teams who troubleshoot customer systems, network appliances and legacy Telnet devices.
- Network and hardware engineers who need RAW TCP, Telnet and serial tools beside normal SSH workflows.
- Homelab users who want SSH, VNC, file transfer, serial consoles and repo access in one desktop app.

## Learn more

For a short guide, see the quick start: [docs/quick-start.md](quick-start.md).

For a full guide, see the user manual: [docs/user-manual.md](user-manual.md).
