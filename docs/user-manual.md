# Nexus Terminal User Manual

## Contents

1. Getting started
2. The interface at a glance
3. Creating and managing profiles
4. Connection types
5. Tabs and split panes
6. Terminal actions and search
7. Snippets, broadcast input and triggers
8. SFTP file browser
9. Drag-and-drop uploads and paste
10. Port forwards
11. Session recordings
12. Security and credentials
13. Import and export
14. Settings and updates
15. Data locations
16. Troubleshooting

---

## 1. Getting started

### Launch and connect

- Start Nexus Terminal.
- Click **+** at the bottom of the sidebar (or press **Cmd+N** / **Ctrl+N**), choose a protocol, and fill in the profile details.
- Click **Create & Connect** to save the profile and open a session in a new tab, or **Create** to save it for later.
- Click a profile in the sidebar to connect to it.

### Quick connect

Use **Cmd+Shift+Q** (macOS) or **Ctrl+Shift+Q** (Windows/Linux) to open a lightweight connect dialog. Choose SSH, Telnet, RAW, IRC or Serial, enter the host (or serial device), port, and username or nick where relevant, then press **Enter**. This starts a one-off session without saving a profile.

### Local terminal

Click **Local Machine** at the top of the sidebar, choose **File → Local terminal**, or click **Local** on the welcome screen to open a shell on this computer. When the shell exits, the tab shows `[Exited - press enter to restart]`; press **Enter** to start a new shell in the same tab.

### Command palette

Press **Cmd+K** / **Ctrl+K**, or click the search box in the title bar, to open the command palette. Type a few letters to find a connection, switch to a tab, create a profile, open a window, change theme, lock or unlock the secret store, or run a terminal action. Use **Up/Down** to move, **Enter** to run, and **Esc** to close.

---

## 2. The interface at a glance

- **Title bar**: the File, Session, View and Help menus, the active session's name and host, the command palette launcher, a light/dark toggle, and the window controls. Drag the title bar to move the window; double-click it to maximise.
- **Sidebar**: a search box, then **Local Machine**, **★ Favourites**, **Recent** (the last five connections), one section per group, and **Ungrouped** (or **All Connections** when you have no groups). Sections collapse when you click their headers. The footer has buttons for New connection, Command palette, Import, Export, Recordings and Settings. Drag the sidebar edge to resize it.
- **Tab strip**: one tab per session, with a state dot, protocol badge, pin marker, activity dot and close button. SSH tabs also have a folder icon for SFTP. On the right are **+** (new connection) and the broadcast button.
- **Workspace**: the active terminal or viewer; split panes appear here. With no tabs open it shows a welcome screen with **New Connection**, **Quick Connect** and **Local** buttons.
- **Right dock**: the profile editor or the SFTP browser, resizable.
- **Status bar**: the active session's state and protocol, a **REC** marker while recording, latency for SSH sessions, a **BROADCAST** marker, the vault state (click it to open Security settings), the number of connected sessions, and an update notice when a new version is available.

Notifications appear as toasts in the top-right corner and dismiss themselves after a few seconds; click **×** to dismiss one early.

---

## 3. Creating and managing profiles

### Create a new profile

1. Click **+** in the sidebar footer or on the tab strip, press **Cmd+N** / **Ctrl+N**, or right-click a group header and choose **New connection in …**.
2. Choose the protocol (SSH, Telnet, RAW TCP, GitHub, VNC, IRC or Serial) at the top of the editor. Switching protocol keeps the common fields.
3. Fill in the **General** section: Display Name, Host (or Device for serial), Port, Username, Group and Description.
4. Fill in the protocol sections and, for terminal protocols, the **Terminal** section (font family, font size 8–36, scrollback 0–100000 lines, cursor style).
5. Optionally add **Tags** (comma-separated) and **Notes**.
6. Click **Create & Connect**, or **Create** to save without connecting.

Click **Test** to check that the host and port are reachable; a toast reports the round-trip time. Fields with problems are highlighted when you save.

The **Group** field suggests existing groups; type a new name to create one.

### Edit, duplicate, favourite, export or delete

Right-click a profile in the sidebar for **Connect**, **Edit**, **Duplicate**, **Add to Favourites** / **Remove from Favourites**, **Export…** and **Delete**. Double-click a profile to edit it. Deleting a profile also removes its saved credentials.

### Bulk actions

Ctrl-click (Cmd-click on macOS) profiles to select several. A bar appears at the bottom of the sidebar with:

- **Connect all**
- **Disconnect** (asks first when **Confirm on disconnect** is on)
- **Tags** → **Add tags…** or **Replace tags…**
- **Move group** (leave the name empty to move to Ungrouped)
- **Delete**
- **Clear** (clear the selection)

### Search

Type in the sidebar search box to fuzzy-search profiles by name, host, tag and protocol.

### Already open?

If you connect to a profile that already has a live session, Nexus Terminal asks whether to **Use Existing**, open a **New Connection**, or **Cancel**.

---

## 4. Connection types

### SSH

- **Authentication**: password, public key, keyboard-interactive and SSH agent. Choose which methods to try under **Auth Methods**, and tick **Use SSH Agent for Authentication** to use `ssh-agent` (Pageant or the OpenSSH agent on Windows).
- **Key & Passphrase**: choose a key from the suggestions (keys in `~/.ssh`), click **Browse**, or type a full path (`~` is not expanded). The editor checks that the file exists and is a private key.
- **Host Verification**: the host key policy (see section 12).
- **Jump Hosts**: one or more bastions, each with user, host, port and an optional key. Nexus Terminal tunnels through each hop in order.
- **Port Forwarding**: local and remote forwards, each with a bind address and port, destination, label and an **Active** box to start it on connect. A remote forward with no bind address listens on the server's loopback only (as `ssh -R` does); set it to `0.0.0.0` to expose it to the server's network, which also needs `GatewayPorts` on the server.
- **Startup**: a startup command and environment variables (`KEY=VALUE`, one per line).
- **Login Script**: expect-style steps (see below).
- **SSH Agent**: agent authentication and **Forward Agent to Remote** (like `ssh -A`).
- **TOTP / 2FA**: store a Base32 TOTP secret to answer two-factor prompts automatically. Click **Test** to show the current code, **Save** to store the secret, and **Delete** to remove it. Save the profile first.
- **Proxy**: none, SOCKS5 or HTTP CONNECT, with host, port and optional username and password. The proxy is used to reach the SSH server, or the first jump host.
- **Advanced**: terminal type, keepalive interval, max missed keepalives, compression, auto-reconnect and the maximum number of reconnect attempts (1–20).

The SFTP browser, port forwards and status-bar latency are available on SSH sessions.

### Telnet

- **Login**: local echo and line mode.
- **Terminal & Encoding**: terminal type and encoding (UTF-8, ASCII or ISO 8859-1).
- **Echo & Line Endings**: LF, CR, CRLF or none.
- **Negotiation**: **Debug Negotiation** for troubleshooting option negotiation.
- **Login Script** and **Reconnection** settings.

Telnet sends everything in plain text. Avoid it for anything sensitive over an untrusted network.

### RAW TCP

- **Encoding & Framing**: encoding (UTF-8, ASCII or Hex) and line ending.
- **Display**: show timestamps and show direction.
- **Payload Presets**: named payloads with a format (Text, Hex or Escaped) for quick sending.
- **Timeouts & Reconnect**: connect and read timeouts, and auto-reconnect.

A RAW session opens a viewer instead of a terminal:

- **Text**, **Hex** and **Split** views of everything sent and received.
- **Pause** / **Resume**, **Clear**, and **Save** (writes the capture as JSON).
- A send bar with a format selector (Text, Hex or Escaped), a **Presets…** menu, and quick buttons for EOT, ETX, LF, CR and NUL. Press **Enter** to send.

### Serial

Serial profiles open a terminal on a local serial port.

- **Device**: pick the port from the list and click refresh after plugging in an adapter, or type a path such as `/dev/ttyUSB0`, `/dev/cu.usbserial-…` or `COM3`.
- **Line Settings**: baud rate (a preset from 300 to 1000000, or a custom value), data bits (5–8), parity (none, odd or even), stop bits (1 or 2) and flow control (none, XON/XOFF or RTS/CTS).
- **Behaviour**: line ending, encoding, local echo (for devices that don't echo), **Assert DTR on connect**, **Assert RTS on connect**, and auto-reconnect.

The defaults are 115200 baud, 8 data bits, no parity, 1 stop bit, no flow control, CR line endings, and DTR and RTS asserted.

While connected, right-click the tab to **Send Break** or toggle **DTR** and **RTS**. Telnet tabs also offer **Send Break**.

When the adapter is unplugged the session disconnects; with auto-reconnect on, the port is reopened when the device returns. Serial sessions support everything other terminals do: recording, triggers, broadcast, snippets, find and split panes.

### VNC

- **VNC Server**: VNC host (defaults to the profile's Host), port, and password storage mode.
- **Display**: **View Only**, **Scale to Fit**, **Shared Session** and **Quality (0-9)**.
- **SSH Tunnel**: carry VNC traffic through SSH, with host, port, username and password, public key or SSH agent authentication.

The viewer's toolbar shows the connection state and desktop size, and has buttons to switch between scaled and 1:1 display, refresh the screen, **Paste Clipboard** (send your local clipboard to the remote desktop) and **Ctrl+Alt+Del**. Text the remote desktop copies arrives on your local clipboard. Click the desktop to give it keyboard focus.

Use SSH tunnelling for VNC whenever possible. If a VNC profile has no tunnel, Nexus Terminal warns that the traffic is unencrypted and offers **Connect Anyway**, **Edit Profile** or **Cancel**.

### RDP

- **RDP**: domain (optional), password storage mode, **Certificate Policy** and **Allow servers without NLA**. The username, host and port (3389) are the profile's own.
- **Display**: **Scale to Fit**, a fixed **Width** and **Height** (0 × 0 fits the tab) and **Resize With Tab**.
- **RD Gateway** (experimental): reach the desktop through a Remote Desktop Gateway (`host` or `host:port`) with its own username and password. The gateway's certificate must be valid for its name (public CA); only gateways that accept Basic authentication over HTTPS work.
- **Proxy**: **SOCKS5** or **HTTP CONNECT**, with an optional username and password.
- **Devices**: **Play Remote Audio**; **Redirect Printer**, which adds a *Nexus Terminal* printer on the remote desktop whose jobs are saved as PostScript files in your Downloads folder (up to 256 MiB per job); and on Linux and macOS a **Shared Folder** that appears on the remote desktop as a drive named Nexus. The server can read and change everything in that folder and nothing outside it: paths that try to leave it (`..`, or symlinks pointing elsewhere) are refused.
- **SSH Tunnel**: carry RDP through SSH, as for VNC. Use either a proxy or a tunnel, not both.

Nexus Terminal logs on with Network Level Authentication (CredSSP), so your password is checked before a session is set up. It uses NTLM unless you set a **Kerberos KDC** (`host` or `host:port`), in which case it gets a Kerberos ticket from that KDC over TCP; the KDC must be reachable directly (not through the profile's proxy or tunnel). Use your Kerberos realm as the domain. Servers without NLA, such as xrdp, need **Allow servers without NLA**; the password is then passed to the server's own logon. Smart cards are not supported.

RDP servers usually present self-signed certificates, so the certificate is pinned like an SSH host key: its SHA-256 fingerprint is shown the first time and stored in **Known Hosts**, and a changed certificate offers Reject, Accept Once or Accept & Update. The check happens before any credentials are sent.

The viewer works like the VNC one: click the desktop to give it keyboard focus, **Ctrl+Alt+Del** sends the key combination, and text is shared through the clipboard both ways: text you copy on the remote desktop arrives on your clipboard (subject to the remote-clipboard setting), and your clipboard is sent to the remote desktop only when you paste (Ctrl+V / Cmd+V on the desktop, or **Paste Clipboard**), so a server can't read what you last copied. Files and images are not shared. With **Resize With Tab**, the remote desktop follows the tab's size on servers that support it (Windows); otherwise the picture is scaled.

### IRC

- **IRC**: nick, real name, **Use TLS** (port 6697 for most TLS networks) and an optional server password.
- **SASL**: log in during registration with SASL PLAIN (account defaults to your nick), which most networks now expect; use TLS or an SSH tunnel so the password isn't sent in the clear. If the network doesn't offer SASL or rejects the login, the Server tab says so and the connection carries on.
- **NickServ**: identify on connect, with an optional account name and password.
- **Rooms**: rooms to join automatically, one per line or comma-separated. Networks that support IRCv3 server-time stamp messages with the time they were sent, so replayed history shows its real times.
- **SSH Tunnel** and **Reconnection** settings.

The IRC view has a **Server** tab and a tab per room or conversation, with a dot on tabs that have unread messages. Type messages or `/commands` in the composer (`/join`, `/part`, `/msg`, `/me`, `/notice`, `/nick`, `/topic`, `/list`, `/names`, `/whois`, `/who`, `/raw`, `/quit`, `/help`). Press **Tab** to complete a nick, or click a nick to mention it. The side panel lists joined rooms, a room browser with a filter (click a room to join it), and the users in the current room.

### GitHub

- **API**: API base URL (`https://api.github.com`, or your GitHub Enterprise API URL) and personal access token storage. The token needs the `repo` scope.
- **Refresh**: refresh interval, 60–3600 seconds.

Repositories are grouped by owner or organisation and sorted by most recent activity. Each card shows Private, Fork and Archived badges, language, stars, description, and the last commit and committer. Use the filter box to narrow the list, **Refresh now** to update it, **Visit** to open a repository in your browser, and **ZIP** to download an archive.

---

## 5. Tabs and split panes

- **Reorder** tabs by dragging them, or right-click and choose **Move Left** / **Move Right**.
- **Pin** important sessions from the tab's right-click menu; pinned tabs stay at the front.
- **Rename**, **Duplicate** (open another session to the same profile) and **Reconnect** from the right-click menu.
- **Scroll** the tab strip with the mouse wheel or trackpad when many tabs are open.
- **Hover** a tab to see its name, host, state and, for connected sessions, the connection start time.
- **Close** a tab with its **×**, **Cmd+W** / **Ctrl+W**, or **Close** in the right-click menu. When **Confirm on close** is on, closing a connected tab asks first (GitHub tabs close straight away).
- **Switch** tabs with **Cmd+Tab** / **Ctrl+Tab**, **Cmd+Shift+Tab** / **Ctrl+Shift+Tab**, or **Cmd+1–9** / **Ctrl+1–9**.
- A dot on a background tab means it has new output.

### Split panes

- **Cmd+Shift+D** / **Ctrl+Shift+D** splits the focused pane to the right (side by side).
- **Cmd+Shift+E** / **Ctrl+Shift+E** splits it downwards (top and bottom).
- Split Right and Split Down are also in the Session menu, the terminal's right-click menu and the command palette.

A new pane offers **Local Machine** and your terminal profiles (SSH, Telnet and Serial); click one to open it in that pane. Drag a divider to resize panes. Click **×** on a pane's header to close it; its neighbour takes the space. Closing the tab closes every pane in it.

---

## 6. Terminal actions and search

### Inline images

Programs that print images in the iTerm2 format (`imgcat`, `wezterm imgcat`, `chafa -f iterm`) show PNG, JPEG and GIF (first frame) images inline, sized as the program asks and never wider than the terminal; the text continues below the image. Images scroll with the text and are dropped with the scrollback. Very large images (over 8192 pixels a side) are ignored, and each terminal keeps at most 32 megapixels of images, dropping the oldest. Sixel and kitty graphics aren't supported.

### Shell integration

When the shell marks its prompts and commands (the OSC 133 sequences), Nexus Terminal knows where each command starts and ends:

- **Ctrl+Shift+Up / Ctrl+Shift+Down** (**Cmd+Shift** on macOS), or **Previous Prompt** / **Next Prompt** in the right-click menu, jump between prompts in the scrollback.
- **Copy Last Command Output** in the right-click menu copies what the last finished command printed.
- A thin bar beside each finished command's prompt shows how it ended: green for success, red for a non-zero exit. Hover it for the exit code and how long the command took.

fish 4 and several prompt themes (Starship, Powerlevel10k with its shell integration option) send the marks already. For bash, add this to `~/.bashrc` on the machine the shell runs on:

```bash
PS0='\[\e]133;C\a\]'
PROMPT_COMMAND='printf "\033]133;D;%s\007" "$?"; '"${PROMPT_COMMAND}"
PS1='\[\e]133;A\a\]'"$PS1"'\[\e]133;B\a\]'
```

For zsh, add to `~/.zshrc`:

```zsh
precmd() { print -Pn "\e]133;D;$?\a\e]133;A\a" }
preexec() { print -n "\e]133;C\a" }
PS1="$PS1%{\e]133;B\a%}"
```

Shells that also report their working directory (OSC 7; fish, and zsh/bash with the usual `printf '\e]7;file://%s%s\a' "$HOSTNAME" "$PWD"` hook) let **SFTP Browser** open in that directory.

### Keyboard and mouse

- **Copy**: select text and press **Cmd+C** / **Ctrl+C**. With nothing selected, **Ctrl+C** sends an interrupt as usual.
- **Paste**: **Cmd+V** / **Ctrl+V**. Pastes are bracketed when the remote application asks for it.
- **Select**: drag to select, double-click for a word, triple-click for a line, Shift-click to extend.
- **Links**: hold **Cmd** / **Ctrl** and click a web link to open it in your browser. Programs can also print real hyperlinks (OSC 8, as `ls --hyperlink` and many build tools do); these have a dotted underline, and hovering one shows where it really points. Only http(s) links are opened.
- **Scroll**: the mouse wheel, or **Shift+PageUp**, **Shift+PageDown**, **Shift+Home** and **Shift+End**. On the alternate screen (for example in `tmux`, `screen`, `byobu` or `less`) the wheel sends arrow keys instead.
- Applications that use the mouse (such as `htop`, `vim` or `tmux` with mouse mode) receive mouse clicks and the wheel directly.

### Context menu

Right-click in the terminal for:

- Copy and Paste
- Select All
- Find
- Send Snippet
- Port Forwards (SSH)
- Split Right and Split Down
- Export Output (save the whole scrollback as a text file)
- Clear and Reset

### Search

Press **Cmd+F** (macOS) or **Ctrl+F** (Windows/Linux) to search in the active terminal. Toggle **Aa** for case-sensitive matching and **.\*** for regular expressions. The bar shows "*n* of *total*"; press **Enter** for the next match, **Shift+Enter** for the previous one, and **Esc** to close.

### Disconnects and errors

When a session drops, the pane is dimmed and shows a **Disconnected** (or **Connection error**) card with **Reconnect** and **Close**. While auto-reconnect is running it shows **Reconnecting…** with a **Cancel** button, and a toast shows the attempt number and delay.

If a connection fails because of authentication or a host key, a guided dialog explains what happened and offers **Retry**, **Edit Profile**, **Known Hosts** (for host-key errors) and **Close**.

---

## 7. Snippets, broadcast input and triggers

### Snippets

Open the snippet library with **Cmd+Shift+S** (macOS) or **Ctrl+Shift+S** (Windows/Linux), from the Session menu, or with **Send Snippet** in the terminal's right-click menu. Create snippets with a name, description, command and folder, search them, and click **Send to Session** to type the command into the active session followed by Enter.

Snippets can use variables:

- `{{hostname}}`, `{{date}}`, `{{time}}` and `{{timestamp}}` are filled in automatically.
- Any other `{{name}}` is asked for when you send the snippet.

### Broadcast input

Toggle broadcast mode with **Cmd+Shift+B** (macOS) or **Ctrl+Shift+B** (Windows/Linux), the broadcast button on the tab strip, or the Session menu. While it is on, everything you type is also sent to every other connected terminal session, and the status bar shows **BROADCAST**.

To broadcast to only some sessions, right-click the broadcast button (or choose **Session → Broadcast targets…**), tick the sessions you want, and click **Apply & Enable**.

### Trigger rules

Open **Trigger Rules** from the Session menu or the command palette. Each rule has a name, a pattern (plain text or regular expression), a case-sensitive option, an enabled switch and an action:

- **Notify**: show a toast.
- **Highlight tab**: briefly outline the pane.
- **Notify + Highlight**: both.
- **Sound**: ask the operating system for attention (for example, a bouncing dock icon or flashing taskbar button) and show a toast.

Rules are checked against each complete line of terminal output, with colour codes removed.

---

## 8. SFTP file browser

Open the file browser with the **folder** icon on an SSH tab, or **Session → SFTP browser**. It opens in the right dock beside the terminal. You can:

- Navigate folders using the breadcrumb trail, the `..` entry, or **Go to path**.
- Upload files, and download one or more selected files to a folder you choose.
- Rename a single item, and delete one or more items (with confirmation).
- Create folders and refresh the listing.
- Bookmark the current folder with the star button; bookmarks are kept per profile.
- Watch transfers and their progress in the transfer list.

Ctrl-click (Cmd-click) or Shift-click to select several items. Drag the dock edge to resize it.

---

## 9. Drag-and-drop uploads and paste

When you drop file(s) onto an active terminal, Nexus Terminal asks whether to:

- **Upload to current directory** — choose a remote directory (SSH sessions only), or
- **Paste contents** into the terminal as if typed.

### Upload directory picker

The picker looks like the SFTP browser and lists remote folders only. It shows:

- **READ ONLY** for folders you cannot write to.
- **NO READ** for folders you cannot access.

Use the filter box to narrow the list, and the breadcrumb trail or **Up** button to move around. **Use this folder** is disabled when the folder is not writable. You can also use the keyboard:

- **Up/Down**: move selection
- **Enter**: choose the selected folder
- **Backspace**: go up a folder (when the filter is empty)
- **Escape**: cancel

If the SFTP browser is already open, the picker starts in the same directory.

Tip: Use the filter box to jump quickly to common folders.

---

## 10. Port forwards

Forwards saved in an SSH profile start when the session connects. To manage forwards on a live SSH session, choose **Port Forwards** from the terminal's right-click menu or **Session → Port forwards**. The dialog lists active forwards (marked **L** for local or **R** for remote) with a stop button, and lets you start a new one:

- **Local →**: listen on a local bind address and port, and forward to a destination reachable from the server.
- **Remote ←**: ask the server to listen, and forward connections back to a destination reachable from this computer.

Toasts report when a forward starts, stops or fails.

---

## 11. Session recordings

Toggle recording with **Cmd+Shift+R** (macOS) or **Ctrl+Shift+R** (Windows/Linux), or **Session → Toggle recording**. A red dot appears on the tab and **REC** in the status bar. Recordings are saved as asciicast v2 files.

Open the recordings browser from the sidebar footer, the Session menu or the command palette. It shows how many recordings you have and how much space they use, lets you play or delete each one, and has **Clean up** for removing recordings older than 7, 30 or 90 days, or keeping the total under 1 GB or 5 GB.

Playback starts automatically and has play/pause, restart, a seek bar with elapsed and total time, and speed control.

---

## 12. Security and credentials

### Where credentials are stored

- **OS keychain first** (macOS Keychain, Linux Secret Service or Windows Credential Manager).
- **Encrypted vault** fallback, using AES-256-GCM with an Argon2id-derived key (128 MiB of memory, which makes brute-force attacks expensive).

If the keychain is unavailable, or refuses to store a secret, credentials go to the vault. Set the vault up in **Settings → Security** with **Initialise Secret Store**; the master-password dialog shows a strength meter (Weak, Fair, Good, Strong) and asks you to confirm the password. Unlock and lock it from the same page, the command palette, or by clicking the vault state in the status bar. The vault is locked again when Nexus Terminal quits.

Without the master password, credentials in the vault cannot be recovered.

### Secret modes

Each password, passphrase, token or TOTP secret has its own mode:

- **Save securely**: stored in the keychain or vault and used automatically.
- **Remember for this session**: asked for once, kept in memory for that session, never written to disk.
- **Ask every time**: asked for on every connection.

When a session needs a credential it doesn't have, a prompt appears; press **Esc** or **Cancel** to abandon the connection.

### Host key verification

Choose one of the following policies per SSH profile:

- **Strict (known hosts only)**: only accept previously saved host keys.
- **Ask on new or changed keys**: prompt before trusting a new host key; the dialog shows the key type and fingerprint with **Accept & Save** and **Reject**.
- **Accept new, reject changed**: trust first-time keys automatically.
- **Insecure (accept anything)**: accept any key (not recommended).

**Settings → Known Hosts** can **Import ~/.ssh/known_hosts** (hashed host names can't be read back and are skipped) and **Export…** the list in OpenSSH format, so Nexus Terminal and `ssh` can share what they trust.

**SSH certificates.** **Trust a CA…** adds a certificate authority for a host pattern in OpenSSH syntax (`*.example.com`, `[host]:2222`, `!excluded.example.com`; `@cert-authority` lines in an imported known_hosts work too). Servers covered by a trusted CA are asked for their host certificate, and a certificate that the CA signed, that names the host and that is in date is trusted without a first-use prompt; any other certificate is refused. For logging in, a user certificate next to your key (`id_ed25519-cert.pub` beside `id_ed25519`) is used automatically.

Under every policy except Insecure, a key that differs from the saved one is rejected. Nexus Terminal keeps its own known-hosts list and does not read `~/.ssh/known_hosts`. Review and remove entries in **Settings → Known Hosts**; click a fingerprint to copy it.

### TOTP / 2FA

Store a TOTP secret in the profile editor to answer verification-code prompts automatically during SSH keyboard-interactive authentication. Secrets are stored in the keychain or vault, never in the profile database.

### SSH key manager

Open **SSH key manager** from the Session menu or the command palette. It lists key pairs in `~/.ssh` with their type, lets you copy a public key to the clipboard or delete a pair, and generates new Ed25519, RSA (2048 or 4096 bits) or ECDSA keys with an optional passphrase. Key generation uses the `ssh-keygen` program.

### Audit log

Connections, disconnections, profile changes, credential access, imports and exports are recorded in an audit log in the database. Secrets are never written to it.

---

## 13. Import and export

### Profile sync

**Settings → Security → Profile sync** keeps connection profiles the same on several machines. Choose a folder all of them can reach (a Dropbox, iCloud Drive, OneDrive or Nextcloud folder, a network share, or a git checkout you commit), and set the same **sync password** on each. Nexus Terminal keeps one encrypted file there, `nexus-profiles.nexus` (Argon2id + AES-256-GCM, like an encrypted export), and merges it at launch, a few seconds after you change a profile, every ten minutes and when you click **Sync Now**:

- for a profile changed on both sides, the more recent change wins;
- a profile deleted on one machine is deleted on the others (unless it was edited after the deletion);
- saved passwords, keys and tokens are never synced; each machine keeps its own in its keychain or vault. Values that a login script `send`s (including a password typed after a `wait "Password:"`) are part of the profile and do travel, as they do in an export;
- timestamps in the shared file are never trusted beyond the local clock, so a file edited to claim a future date can't override later local changes.


- **Encrypted export**: **File → Export profiles…**, the sidebar footer, the command palette, or **Export…** on a profile's right-click menu. Choose the profiles, set a password and save a `.nexus` file. Credentials saved in the keychain or vault are not included.
- **Encrypted import**: **File → Import profiles…** or the sidebar footer. Choose a `.nexus` file, enter its password, and choose what happens when a profile already exists: **Skip existing**, **Overwrite existing** or **Create duplicates**.
- **JSON export and import**: **Export Profiles (JSON)** and **Import Profiles (JSON)** in the command palette. JSON export includes every profile; passwords in login-script `send` steps that answer a password prompt are blanked unless you tick **Include secrets**. Proxy passwords and TOTP secrets live in the keychain or vault, never in the profile, so no export carries them.
- **OpenSSH config**: **File → Import ~/.ssh/config** or **Settings → About → Import ~/.ssh/config** creates profiles from your `Host` blocks, including hostnames, usernames, ports, identity files and jump hosts.

---

## 14. Settings and updates

Open settings with **Cmd+,** (macOS) or **Ctrl+,** (Windows/Linux), the sidebar footer, or **File → Settings**. Click **Save** to apply changes.

- **General**: app theme (Pro, Midnight or Light), default protocol, restore tabs at launch (reopens the last session's tabs and split panes: saved connections reconnect, local shells start in the directory they were in; Quick Connect tabs aren't restored), confirm on close, confirm on disconnect, remote clipboard writes (OSC 52 and VNC; a toast shows what was copied), multi-line paste confirmation, the launch-time update check, status bar, sidebar width, and local shell (leave empty for the system default; **Browse…** to pick one).
- **Terminal**: colour scheme, font family, font size, scrollback lines and cursor style. These are the defaults for new profiles; profiles can override them.
- **Security**: keychain and vault status; initialise, unlock or lock the secret store; **Change Master Password…** (re-encrypts every saved credential under the new password in one step); **Stored credentials**, listing each profile's saved passwords, passphrases and tokens and whether they're in the keychain or the vault, with **Forget**; and profile sync.
- **Known Hosts**: saved host keys with first-seen and last-seen times, and **Remove**; import/export `~/.ssh/known_hosts` and trusted SSH certificate authorities.
- **Diagnostics**: version, platform, data, database and log locations, keychain and vault state, counts and sync status, with **Copy Diagnostics** for bug reports (no credentials are included).
- **Help**: built-in help topics, including connection types, shortcuts and troubleshooting.
- **About**: version, **Check for Updates**, and **Import ~/.ssh/config**.

The app theme and terminal colour scheme can also be changed from the **View** menu, and the light/dark button in the title bar.

### Updates

Nexus Terminal checks for a new version once at launch. When one is available, the status bar shows **Update available**; click it to open **Settings → About**, which shows the current and latest versions, release notes and a **View release** button. Nothing is downloaded or installed automatically.

---

## 15. Data locations

### Data locations

| OS      | Data directory                                     |
| ------- | -------------------------------------------------- |
| macOS   | `~/Library/Application Support/NexusTerminal`      |
| Windows | `%APPDATA%\NexusTerminal`                          |
| Linux   | `$XDG_CONFIG_HOME/NexusTerminal` (usually `~/.config/NexusTerminal`) |

The directory holds:

- `nexus.db` — profiles, known hosts, preferences, snippets, IRC channel state and the audit log (SQLite)
- `vault/vault.json` — the encrypted vault
- `recordings/` — session recordings
- `triggers.json` — trigger rules
- `sftp-bookmarks.json` — SFTP bookmarks
- `nexus.log` — the diagnostic log (owner-only, rotated to `nexus.log.1` at 5 MiB): connections, reconnects, host-key and certificate checks and errors. Credentials and terminal contents are never logged. Set `RUST_LOG` (for example `RUST_LOG=debug`) for more detail; it then also goes to the terminal you started Nexus Terminal from.

Credentials saved in the OS keychain are stored under the service name `NexusTerminal`.

---

## 16. Troubleshooting

### Session shows as disconnected

- The pane is dimmed and shows a **Disconnected** overlay when the connection drops.
- Click **Reconnect** to reopen the session with the same profile.

### Authentication fails

- Check the username, password, key path and the auth methods ticked in the profile.
- If the key has a passphrase, check the passphrase mode.
- If the server uses two-factor prompts, check the TOTP secret with **Test** and make sure your system clock is accurate.

### "Host key verification failed"

The server's key differs from the one saved earlier. This can happen when a server is reinstalled, but it can also indicate an attack. If you're certain it's safe, open **Settings → Known Hosts**, remove the entry, and reconnect.

### Upload fails or is greyed out

- Check the folder is writable; **READ ONLY** indicates no write permission.
- Confirm the session is connected over SSH; upload isn't available for other protocols.

### Cannot read a folder

- **NO READ** indicates insufficient permissions.
- Try a parent folder or check remote access controls.

### Serial port won't open

- Check the device path and that the adapter is plugged in; refresh the device list in the editor.
- **Permission denied** on Linux: add your user to the `dialout` group (`uucp` on some distributions), then log out and back in.
- On macOS, use the `/dev/cu.*` device rather than `/dev/tty.*`.
- **Port is in use by another program**: close any other program that has the port open, such as `screen`, `minicom` or an IDE serial monitor.
- Garbled output usually means the baud rate, parity or stop bits don't match the device.

### VNC will not connect

- Confirm the VNC host and port are correct. The common VNC port is `5900` plus the display number.
- If using a password, check whether it is set to save securely, remember for this session, or ask every time.
- If using an SSH tunnel, verify the tunnel host, username, authentication method, private key and SSH host key policy.
- If direct VNC is enabled, test from a trusted network first and prefer an SSH tunnel for untrusted networks.

### The secret store is locked

If a saved credential isn't used, the vault may be locked. Click the vault state in the status bar, or open **Settings → Security**, and unlock it.

### Session keeps reconnecting

- Check network stability and credentials.
- Review the auto-reconnect settings in the profile. Reconnection backs off exponentially (1 s, 2 s, 4 s … up to 30 s) and stops after the maximum number of attempts.

### Terminal size looks wrong

- Resize the window or drag a split divider; the terminal re-fits automatically.
- Try disconnecting and reconnecting the session.
- Some older servers don't support modern terminal features; try a different terminal type in the profile.

### Trying things safely

To try something without touching your real data, start Nexus Terminal with `--data-dir DIR` to use a different data directory.
