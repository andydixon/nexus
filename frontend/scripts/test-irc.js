#!/usr/bin/env node
const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

const frontendRoot = path.resolve(__dirname, "..");
const source = fs.readFileSync(path.join(frontendRoot, "src/js/irc.js"), "utf8");
const context = {
  window: {
    NexusTerminal: {
      createElement: () => {
        throw new Error("formatting tests must not create DOM elements");
      },
    },
  },
};
vm.createContext(context);
vm.runInContext(source, context, { filename: "src/js/irc.js" });

const formatting = context.window.NexusTerminal.IRCFormatting;
assert.ok(formatting, "IRC formatting helpers should be exported");

assert.strictEqual(
  formatting.formatText("plain \x02bold\x02 \x1ditalic\x1d \x1funderlined\x1f"),
  "plain \x1b[1mbold\x1b[22m \x1b[3mitalic\x1b[23m \x1b[4munderlined\x1b[24m",
);
assert.strictEqual(
  formatting.formatText("\x0304red\x03 default"),
  "\x1b[38;5;9mred\x1b[39;49m default",
);
assert.strictEqual(
  formatting.formatText("\x0352extended \x0303,01matrix"),
  "\x1b[38;5;196mextended \x1b[38;5;2m\x1b[48;5;0mmatrix",
);
assert.strictEqual(
  formatting.formatText("\x04ff8800orange\x04 default"),
  "\x1b[38;2;255;136;0morange\x1b[39;49m default",
);
assert.strictEqual(
  formatting.formatText("\x0304,comma"),
  "\x1b[38;5;9m,comma",
  "a colour code without a background must leave its comma visible",
);
assert.strictEqual(
  formatting.plainText("\x02bold\x02 \x0304red\x03"),
  "bold red",
);
assert.strictEqual(
  formatting.formatText("before\x1b[31munsafe\x1b]0;title\x07after"),
  "beforeunsafeafter",
  "untrusted ANSI and OSC sequences must not reach xterm",
);

const message = formatting.formatEvent(
  {
    type: "message",
    from: "alice",
    text: "hello \x02world\x02",
  },
  "12:34:56",
);
assert.ok(message.includes("[12:34:56]"));
assert.ok(message.includes("\x1b[1mworld\x1b[22m"));
assert.ok(message.includes("\x1b[1m\x1b[38;5;"), "nicknames should be bold and coloured");
assert.ok(message.endsWith("\x1b[0m"), "every line must reset terminal styling");

const action = formatting.formatEvent(
  { type: "action", from: "bob", text: "waves" },
  "01:02:03",
);
assert.ok(action.includes("\x1b[3m"), "actions should be italicised");

console.log("IRC formatting regression tests passed.");
