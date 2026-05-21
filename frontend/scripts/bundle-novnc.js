/**
 * bundle-novnc.js — Uses esbuild to bundle noVNC's RFB class into a single
 * script that exposes window.noVNC.RFB for use in the frontend.
 *
 * noVNC 1.5+ uses top-level await which is incompatible with IIFE format,
 * so we bundle as ESM then wrap in an async IIFE via banner/footer.
 */
"use strict";

const esbuild = require("esbuild");
const path = require("path");

const entryPoint = path.resolve(__dirname, "novnc-entry.js");
const outFile = path.resolve(__dirname, "..", "src", "js", "lib", "novnc.min.js");

esbuild
  .build({
    entryPoints: [entryPoint],
    bundle: true,
    minify: true,
    format: "esm",
    outfile: outFile,
    target: ["safari15"],
    logLevel: "info",
    banner: { js: ";(async function() {" },
    footer: { js: "})();" },
  })
  .then(() => {
    console.log("[bundle-novnc] noVNC bundled successfully → src/js/lib/novnc.min.js");
  })
  .catch((err) => {
    console.error("[bundle-novnc] Failed:", err.message);
    process.exit(1);
  });
