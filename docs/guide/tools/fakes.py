#!/usr/bin/env python3
"""Tiny fake servers for the user-guide screenshots: RAW sensor feed (42023), IRC (42667),
GitHub API (42080) and a VNC desktop (42900). Stdlib only. Runs until killed."""

import json, math, random, socket, socketserver, struct, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Threaded(socketserver.ThreadingMixIn, socketserver.TCPServer):
    allow_reuse_address = True
    daemon_threads = True


# --- RAW: a chatty sensor --------------------------------------------------------------

class Raw(socketserver.BaseRequestHandler):
    def handle(self):
        s = self.request
        s.sendall(b"SENSOR-7 ready. Commands: STATUS, RESET\n")
        t = 20.5
        s.settimeout(0.25)
        for i in range(400):
            t += random.uniform(-0.3, 0.3)
            s.sendall(f"temp={t:.2f}C hum={48 + 4 * math.sin(i / 3):.1f}% seq={i}\n".encode())
            try:
                if (cmd := s.recv(256)):
                    s.sendall(b"OK " + cmd.strip() + b"\n")
            except socket.timeout:
                pass


# --- IRC: a small, friendly network ----------------------------------------------------

CHAT = [
    ("ferris", "anyone tried the new Nexus build yet?"),
    ("crabby", "yep, the split panes are lovely"),
    ("ferris", "the serial console support saved my afternoon tbh"),
    ("lin", "does it do jump hosts?"),
    ("crabby", "lin: multiple hops, plus SOCKS5/HTTP proxies"),
    ("ferris", "and it's Rust all the way down now :)"),
]


class Irc(socketserver.StreamRequestHandler):
    def send(self, line):
        self.wfile.write((line + "\r\n").encode())

    def handle(self):
        nick = "andy"
        for raw in self.rfile:
            line = raw.decode(errors="replace").strip()
            cmd, _, rest = line.partition(" ")
            if cmd == "NICK":
                nick = rest.strip()
            elif cmd == "USER":
                self.send(f":irc.demo 001 {nick} :Welcome to the Demo IRC Network, {nick}")
                self.send(f":irc.demo 375 {nick} :- irc.demo Message of the Day -")
                self.send(f":irc.demo 372 {nick} :- Be excellent to each other.")
                self.send(f":irc.demo 376 {nick} :End of /MOTD command.")
            elif cmd == "JOIN":
                for room in rest.split()[0].split(","):
                    self.send(f":{nick}!u@demo JOIN {room}")
                    self.send(f":irc.demo 332 {nick} {room} :Nexus Terminal chat · be kind · no pasting walls of logs")
                    self.send(f":irc.demo 353 {nick} = {room} :@ferris +crabby lin {nick} octo rustacean")
                    self.send(f":irc.demo 366 {nick} {room} :End of /NAMES list.")
                    if room == "#nexus":
                        for who, msg in CHAT:
                            self.send(f":{who}!u@demo PRIVMSG #nexus :{msg}")
                    else:
                        self.send(f":octo!u@demo PRIVMSG {room} :borrow checker 1, me 0")
            elif cmd == "LIST":
                for room, n, topic in [("#nexus", 42, "Nexus Terminal"), ("#rust", 3110, "Rust"), ("#linux", 905, "Linux"), ("#homelab", 77, "Racks & routers")]:
                    self.send(f":irc.demo 322 {nick} {room} {n} :{topic}")
                self.send(f":irc.demo 323 {nick} :End of /LIST")
            elif cmd == "PING":
                self.send(f":irc.demo PONG irc.demo {rest}")
            elif cmd == "QUIT":
                return


# --- GitHub API --------------------------------------------------------------------------

REPOS = [
    ("andydixon", "nexus-terminal", "SSH, Telnet, Serial, IRC and VNC in one tidy native app", "Rust", 128, False, False, False, 1),
    ("andydixon", "leviathan", "The egui design system Nexus borrows its looks from", "Rust", 64, False, False, False, 3),
    ("andydixon", "dotfiles", "Shell, editor and terminal config", "Shell", 12, False, False, False, 6),
    ("andydixon", "homelab-ansible", "Playbooks for the rack under the stairs", "Jinja", 7, True, False, False, 9),
    ("andydixon", "alchementary-link", "The Wails/Go predecessor to Nexus Terminal", "Go", 31, False, False, True, 40),
    ("alchementary", "website", "Marketing site", "TypeScript", 3, True, False, False, 2),
    ("alchementary", "infra", "Terraform for everything", "HCL", 2, True, False, False, 5),
    ("alchementary", "russh", "Fork with a jump-host fix", "Rust", 0, False, True, False, 15),
]
WHO = ["Andy Dixon", "ferris", "crabby", "dependabot[bot]"]


def iso(days):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - days * 86400 - 3600 * 5))


class Github(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def reply(self, obj):
        body = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/user":
            return self.reply({"login": "andydixon"})
        if self.path.startswith("/user/repos"):
            page = 1 if "page=1" in self.path else 2
            if page != 1:
                return self.reply([])
            return self.reply([
                {"full_name": f"{o}/{n}", "name": n, "description": d, "html_url": f"https://github.com/{o}/{n}",
                 "default_branch": "main", "private": p, "fork": f, "archived": a, "language": l,
                 "stargazers_count": s, "pushed_at": iso(age), "updated_at": iso(age), "owner": {"login": o}}
                for o, n, d, l, s, p, f, a, age in REPOS])
        if "/commits/" in self.path:
            i = sum(map(ord, self.path)) % len(WHO)
            age = next((r[8] for r in REPOS if f"/{r[1]}/" in self.path), 1)
            return self.reply({"commit": {"committer": {"name": WHO[i], "date": iso(age)}, "author": {"name": WHO[i], "date": iso(age)}}})
        self.send_error(404)


# --- VNC: a static "desktop" ---------------------------------------------------------------

W, H = 1280, 800


def desktop():
    px = bytearray(W * H * 4)
    wins = [(90, 90, 620, 420, (40, 44, 52)), (560, 260, 1180, 700, (250, 250, 252)), (140, 560, 460, 740, (32, 140, 120))]
    for y in range(H):
        for x in range(W):
            # Diagonal violet → teal wallpaper.
            k = (x + y) / (W + H)
            r, g, b = int(88 * (1 - k) + 20 * k), int(40 * (1 - k) + 150 * k), int(160 * (1 - k) + 170 * k)
            for x0, y0, x1, y1, c in wins:
                if x0 <= x < x1 and y0 <= y < y1:
                    r, g, b = (200, 70, 90) if y < y0 + 28 else c
                    if y >= y0 + 28 and c == (40, 44, 52) and (y - y0) % 22 < 9 and x0 + 20 < x < x0 + 20 + ((y * 37) % 400):
                        r, g, b = (120, 220, 140) if (y // 22) % 3 else (230, 200, 120)
            if y >= H - 40:
                r, g, b = 24, 24, 30
            i = (y * W + x) * 4
            px[i:i + 4] = bytes((r, g, b, 0))
    return bytes(px)


PIXELS = None


class Vnc(socketserver.BaseRequestHandler):
    def recv(self, n):
        b = b""
        while len(b) < n:
            c = self.request.recv(n - len(b))
            if not c:
                raise EOFError
            b += c
        return b

    def handle(self):
        s = self.request
        s.sendall(b"RFB 003.008\n")
        self.recv(12)
        s.sendall(bytes([1, 1]))  # one security type: None
        self.recv(1)
        s.sendall(struct.pack(">I", 0))
        self.recv(1)  # ClientInit
        name = b"studio:0 (demo)"
        s.sendall(struct.pack(">HH", W, H) + bytes([32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 0, 8, 16, 0, 0, 0]) + struct.pack(">I", len(name)) + name)
        sent = False
        try:
            while True:
                t = self.recv(1)[0]
                if t == 0:
                    self.recv(19)
                elif t == 2:
                    n = struct.unpack(">xH", self.recv(3))[0]
                    self.recv(4 * n)
                elif t == 3:
                    self.recv(9)
                    if not sent:
                        s.sendall(struct.pack(">BxH", 0, 1) + struct.pack(">HHHHi", 0, 0, W, H, 0) + PIXELS)
                        sent = True
                    else:
                        time.sleep(0.5)
                        s.sendall(struct.pack(">BxH", 0, 0))
                elif t == 4:
                    self.recv(7)
                elif t == 5:
                    self.recv(5)
                elif t == 6:
                    n = struct.unpack(">3xI", self.recv(7))[0]
                    self.recv(n)
                else:
                    return
        except (EOFError, ConnectionError):
            pass


if __name__ == "__main__":
    PIXELS = desktop()
    for port, handler in [(42023, Raw), (42667, Irc), (42900, Vnc)]:
        threading.Thread(target=Threaded(("127.0.0.1", port), handler).serve_forever, daemon=True).start()
    print("fakes ready", flush=True)
    ThreadingHTTPServer(("127.0.0.1", 42080), Github).serve_forever()
