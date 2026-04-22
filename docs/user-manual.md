# Nexus Terminal User Manual

## Contents

1. Getting started
2. The interface at a glance
3. Creating and managing profiles
4. Connection types
5. Tabs and split panes
6. Terminal actions and search
7. Snippets and broadcast input
8. SFTP file browser
9. Drag-and-drop uploads and paste
10. Session recordings
11. Security and credentials
12. Settings
13. Troubleshooting

---

## 1. Getting started

### Launch and connect

- Start Nexus Terminal.
- Click **New** in the sidebar, choose a protocol, and fill in the profile details.
- Click **Connect** to open a session in a new tab.

### Quick connect

Use **Cmd+Shift+Q** (macOS) or **Ctrl+Shift+Q** (Windows/Linux) to open a lightweight connect dialog. This starts a one-off session without saving a profile.

---

## 2. The interface at a glance

- **Sidebar**: Profiles grouped by folders or tags, with search at the top.
- **Tab bar**: One tab per session with status indicators, badges, and quick actions.
- **Workspace**: The active terminal or viewer; split panes appear here.
- **Status bar**: Connection state and session details.

Screenshot: Main window with sidebar, tab bar, and active terminal.

Tip: You can drag the divider between split panes to rebalance space.

---

## 3. Creating and managing profiles

### Create a new profile

1. Click **New** in the sidebar.
2. Choose the protocol (SSH, Telnet, RAW, GitHub).
3. Fill in the required fields and optional settings.
4. Save and connect.

Screenshot: New profile form with protocol selector and connection fields.

### Edit, duplicate, or delete

Right-click a profile in the sidebar to edit, duplicate, or delete it. You can also click the pencil icon on hover, or double-click the item to edit.

### Folders, tags, and favourites

Use folders and tags to organise profiles. Mark profiles as favourites for quick access.

Tip: Use the search box to filter profiles by name, host, or tag.

---

## 4. Connection types

### SSH

- Password, public key, keyboard-interactive, SSH agent, and TOTP/2FA.
- Jump hosts, port forwarding, proxy support, and auto-reconnect.
- Built-in SFTP browser and session recording.

### Telnet

- Option negotiation, encoding, local echo, and line endings.
- Auto-login scripts and auto-reconnect.

### RAW TCP

- Text, hex, and split views for binary-safe data.
- Payload presets and configurable encoding and line endings.

### GitHub

- Browse repositories by organisation.
- Sorted by most recent activity.

---

## 5. Tabs and split panes

- **Reorder** tabs by dragging them.
- **Pin** important sessions to keep them at the front.
- **Split** the workspace vertically or horizontally to view multiple sessions at once.
- **Broadcast** input to multiple connected sessions when needed.

Screenshot: Tab bar with pinned sessions and split panes.

Tip: Pinned tabs stay at the front so key systems never get buried.

---

## 6. Terminal actions and search

### Context menu

Right-click in the terminal for:

- Copy and paste
- Select all
- Find
- Send snippet
- Clear and reset

Screenshot: Terminal context menu and search bar.

### Search

Press **Cmd+F** (macOS) or **Ctrl+F** (Windows/Linux) to search in the active terminal. Toggle case sensitivity and regular expressions as needed.

---

## 7. Snippets and broadcast input

### Snippets

Open the snippet library with **Cmd+Shift+S** (macOS) or **Ctrl+Shift+S** (Windows/Linux) and save frequently-used commands. Send a snippet to the active session with one click.

Screenshot: Snippet library with search and folder grouping.

### Broadcast input

Toggle broadcast mode with **Cmd+Shift+B** (macOS) or **Ctrl+Shift+B** (Windows/Linux). While active, all input is sent to every connected session.

---

## 8. SFTP file browser

Open the file browser with the **folder** icon on an SSH tab. You can:

- Navigate folders using the breadcrumb trail.
- Upload, download, rename, and delete files.
- Refresh and create folders.

The browser runs inside the SSH session and shows remote contents in a side panel.

Screenshot: SFTP browser open alongside the terminal.

---

## 9. Drag-and-drop uploads and paste

When you drop file(s) into an active terminal, Nexus Terminal asks whether to:

- **Upload** to a chosen remote directory, or
- **Paste contents** into the terminal as if typed.

### Upload directory picker

The picker looks like the SFTP browser and lists remote folders only. It shows:

- **READ ONLY** for folders you cannot write to.
- **NO READ** for folders you cannot access.

Use the filter box to narrow the list, and the breadcrumb trail or **Up** button to move around. You can also use the keyboard:

- **Up/Down**: move selection
- **Enter**: choose the selected folder
- **Escape**: cancel

If the SFTP browser is already open, the picker starts in the same directory.

Screenshot: Upload directory picker with permissions badges.

Tip: Use the filter box to jump quickly to common folders.

---

## 10. Session recordings

Toggle recording with **Cmd+Shift+R** (macOS) or **Ctrl+Shift+R** (Windows/Linux). Recordings are saved as asciicast v2 files and can be replayed from the recordings panel.

Screenshot: Recordings list with playback controls.

---

## 11. Security and credentials

Nexus Terminal stores credentials securely:

- **OS keychain first** (Keychain, Secret Service, or Credential Manager).
- **Encrypted vault** fallback using AES-256-GCM with Argon2id.

### Host key verification

Choose one of the following policies per SSH profile:

- **Strict**: only accept previously saved fingerprints.
- **Ask**: prompt before trusting a new host key.
- **Accept new**: trust first-time keys, reject changes.
- **Insecure**: accept any key (not recommended).

### TOTP / 2FA

Store a TOTP secret in the profile editor to auto-fill verification codes during SSH authentication.

Screenshot: TOTP configuration in the profile editor.

---

## 12. Settings

Open settings with **Cmd+,** (macOS) or **Ctrl+,** (Windows/Linux) and configure:

- Terminal themes and fonts
- Default behaviours
- Secrets and vault settings
- Recording and shortcut preferences

Screenshot: Settings panel with appearance and security options.

---

## 13. Troubleshooting

### Session shows as disconnected

- The terminal is blurred and shows a **DISCONNECTED** overlay when the connection drops.
- Click **Reconnect** to reopen the session with the same profile.

### Upload fails or is greyed out

- Check the folder is writable; **READ ONLY** indicates no write permission.
- Confirm the SSH session is connected.

### Cannot read a folder

- **NO READ** indicates insufficient permissions.
- Try a parent folder or check remote access controls.

### Session keeps reconnecting

- Check network stability and credentials.
- Review auto-reconnect settings in the profile.

### Terminal size looks wrong

- Try disconnecting and reconnecting the session.
- Avoid heavy UI resizing during initial connection.
