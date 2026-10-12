# Nexus Terminal Quick Start

Nexus Terminal is a native desktop client for SSH, Telnet, RAW TCP, Serial, VNC, RDP, IRC, GitHub and local terminal sessions. This page is a shareable quick start for everyday use.

## Get connected in minutes

1. Open Nexus Terminal.
2. Click **+** at the bottom of the sidebar, or press **Cmd+N** (macOS) / **Ctrl+N** (Windows/Linux).
3. Choose a protocol at the top of the editor and enter the connection details.
4. Click **Create & Connect** to save the profile and open the session in a new tab.

Next time, just click the profile in the sidebar.

One-off session? Use **Cmd+Shift+Q** / **Ctrl+Shift+Q** for quick connect. Need a local shell? Click **Local Machine** at the top of the sidebar.

## Daily workflow

- **Command palette**: Press **Cmd+K** / **Ctrl+K** to connect, switch tabs or run any command by typing a few letters.
- **Tabs and splits**: Drag tabs to reorder them, right-click a tab to pin, rename, duplicate or reconnect it, and split the workspace with **Cmd+Shift+D** / **Ctrl+Shift+D** (right) or **Cmd+Shift+E** / **Ctrl+Shift+E** (down).
- **Scrollable tabs**: Use the mouse wheel or trackpad over the tab strip when many tabs are open. Hover a tab to see its host, state and connection start time.
- **Broadcast input**: Toggle with **Cmd+Shift+B** / **Ctrl+Shift+B** to send the same keystrokes to every connected terminal. Right-click the broadcast button on the tab strip to pick targets.
- **Snippets**: Save common commands with **Cmd+Shift+S** / **Ctrl+Shift+S** and send them with one click.
- **Search**: Press **Cmd+F** / **Ctrl+F** to search within the active terminal.
- **Copy, paste and links**: Select text and press **Cmd+C** / **Ctrl+C** to copy (with nothing selected, Ctrl+C interrupts as usual). **Cmd+V** / **Ctrl+V** pastes. **Cmd-click** / **Ctrl-click** a URL to open it.
- **Reconnect quickly**: If a session drops, click **Reconnect** on the disconnected overlay.

## Serial consoles

Create a **Serial** profile, pick the device from the list (click refresh after plugging in an adapter) and set the baud rate and line settings to match the device — 115200 8N1 with no flow control is the default. On Linux your user needs to be in the `dialout` (or `uucp`) group; on macOS use the `/dev/cu.*` device.

## VNC remote desktops

Create a **VNC** profile for remote desktop sessions. Use the SSH tunnel option when possible, especially outside trusted networks. Direct VNC is available for isolated or trusted networks, but classic VNC traffic is not encrypted, so Nexus Terminal asks before connecting without a tunnel.

## RDP remote desktops

Create an **RDP** profile with the host, your username and, if needed, a domain. Windows servers use Network Level Authentication by default; for xrdp and other servers without it, tick **Allow servers without NLA**. The first connection shows the server certificate's fingerprint to trust. Use the **Proxy** or **SSH Tunnel** sections to reach desktops behind a jump host or proxy.

## File transfers on SSH

Click the **folder** icon on an SSH tab to open the SFTP browser. You can browse, upload, download, rename and delete files, create folders and bookmark locations.

### Drag and drop files

Drop file(s) onto a terminal and choose to:

- **Upload to current directory** — pick a remote folder (SSH sessions only), or
- **Paste contents** into the terminal as if typed.

The upload picker lists folders only and marks permissions:

- **READ ONLY**: you can read but cannot write
- **NO READ**: you cannot access the folder

## Security basics

- Credentials are stored in the OS keychain where available.
- An encrypted vault (AES-256-GCM, Argon2id) is used as a fallback; set it up in **Settings → Security**.
- Each password, passphrase or token can be saved, remembered for the session only, or asked for every time.
- SSH host key checks can be set per profile (Strict, Ask, Accept new, Insecure).

## Common shortcuts

| macOS Shortcut  | Windows/Linux Shortcut | Action                   |
| --------------- | ---------------------- | ------------------------ |
| Cmd + K         | Ctrl + K               | Command palette          |
| Cmd + N         | Ctrl + N               | New connection           |
| Cmd + W         | Ctrl + W               | Close tab                |
| Cmd + F         | Ctrl + F               | Find in terminal         |
| Cmd + Shift + S | Ctrl + Shift + S       | Snippet library          |
| Cmd + Shift + B | Ctrl + Shift + B       | Broadcast input          |
| Cmd + Shift + Q | Ctrl + Shift + Q       | Quick connect            |
| Cmd + Shift + R | Ctrl + Shift + R       | Toggle session recording |
| Cmd + Shift + D | Ctrl + Shift + D       | Split pane right         |
| Cmd + Shift + E | Ctrl + Shift + E       | Split pane down          |

## Need more detail?

- Product overview: [docs/overview.md](overview.md)
- User manual: [docs/user-manual.md](user-manual.md)
