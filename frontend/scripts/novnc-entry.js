// Entry point for esbuild bundling of noVNC.
// Assigns RFB to window.noVNC so it's available as a global in the frontend.
import RFB from "@novnc/novnc";
window.noVNC = { RFB: RFB };
