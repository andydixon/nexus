# Nexus Terminal Quick Start

Nexus Terminal is a desktop client for SSH, Telnet, RAW TCP, and GitHub sessions. This page is a shareable quick start for everyday use.

## Get connected in minutes

1. Open Nexus Terminal.
2. Click **New** in the sidebar.
3. Choose a protocol and enter the connection details.
4. Click **Connect** to open the session in a new tab.

One-off session? Use **Cmd+Shift+Q** / **Ctrl+Shift+Q** for quick connect.

## Daily workflow

- **Tabs and splits**: Drag tabs to reorder, and split the workspace to view multiple sessions at once.
- **Broadcast input**: Toggle with **Cmd+Shift+B** to send the same command to all connected sessions.
- **Snippets**: Save common commands and run them with one click.
- **Search**: Press **Cmd+F** to search within the active terminal.

## File transfers on SSH

Click the **folder** icon on an SSH tab to open the SFTP browser. You can browse, upload, download, rename, and delete files.

### Drag and drop files

Drop file(s) into the terminal and choose to:

- **Upload** to a selected remote folder, or
- **Paste contents** into the terminal as if typed.

The upload picker lists folders only and marks permissions:

- **READ ONLY**: you can read but cannot write
- **NO READ**: you cannot access the folder

## Security basics

- Credentials are stored in the OS keychain where available.
- An encrypted vault is used as a fallback.
- SSH host key checks can be set per profile (Strict, Ask, Accept new, Insecure).

## Common shortcuts

| Shortcut          | Action                      |
| ----------------- | --------------------------- |
| Cmd + K           | Command palette             |
| Cmd + N           | New connection              |
| Cmd + W           | Close tab                   |
| Cmd + F           | Find in terminal            |
| Cmd + Shift + S   | Snippet library             |
| Cmd + Shift + B   | Broadcast input             |
| Cmd + Shift + Q   | Quick connect               |
| Cmd + Shift + R   | Toggle session recording    |
| Cmd + Shift + D   | Split pane vertically       |
| Cmd + Shift + E   | Split pane horizontally     |

## Need more detail?

- Product overview: [docs/overview.md](overview.md)
- User manual: [docs/user-manual.md](user-manual.md)
