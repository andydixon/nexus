(function() {
/**
 * Nexus Terminal — terminal colour themes.
 * Provides a collection of preset xterm.js colour schemes and a manager
 * for applying them to terminal instances.
 */

/**
 * Preset terminal colour themes.
 * Each entry contains the full xterm.js theme object.
 */
const THEMES = {
    charcoal: {
        label: 'Charcoal (Default)',
        theme: {
            background: '#1c1c1c',
            foreground: '#e4e4e4',
            cursor: '#4ecdc4',
            cursorAccent: '#1c1c1c',
            selectionBackground: 'rgba(78, 205, 196, 0.3)',
            selectionForeground: '#ffffff',
            black: '#1c1c1c',
            red: '#ef5350',
            green: '#66bb6a',
            yellow: '#ffa726',
            blue: '#42a5f5',
            magenta: '#ab47bc',
            cyan: '#4ecdc4',
            white: '#e4e4e4',
            brightBlack: '#555555',
            brightRed: '#ff7043',
            brightGreen: '#81c784',
            brightYellow: '#ffcc02',
            brightBlue: '#64b5f6',
            brightMagenta: '#ce93d8',
            brightCyan: '#80deea',
            brightWhite: '#ffffff',
        },
    },

    'solarized-dark': {
        label: 'Solarized Dark',
        theme: {
            background: '#002b36',
            foreground: '#839496',
            cursor: '#93a1a1',
            cursorAccent: '#002b36',
            selectionBackground: 'rgba(147, 161, 161, 0.3)',
            selectionForeground: '#fdf6e3',
            black: '#073642',
            red: '#dc322f',
            green: '#859900',
            yellow: '#b58900',
            blue: '#268bd2',
            magenta: '#d33682',
            cyan: '#2aa198',
            white: '#eee8d5',
            brightBlack: '#586e75',
            brightRed: '#cb4b16',
            brightGreen: '#586e75',
            brightYellow: '#657b83',
            brightBlue: '#839496',
            brightMagenta: '#6c71c4',
            brightCyan: '#93a1a1',
            brightWhite: '#fdf6e3',
        },
    },

    monokai: {
        label: 'Monokai',
        theme: {
            background: '#272822',
            foreground: '#f8f8f2',
            cursor: '#f8f8f0',
            cursorAccent: '#272822',
            selectionBackground: 'rgba(73, 72, 62, 0.6)',
            selectionForeground: '#f8f8f2',
            black: '#272822',
            red: '#f92672',
            green: '#a6e22e',
            yellow: '#f4bf75',
            blue: '#66d9ef',
            magenta: '#ae81ff',
            cyan: '#a1efe4',
            white: '#f8f8f2',
            brightBlack: '#75715e',
            brightRed: '#f92672',
            brightGreen: '#a6e22e',
            brightYellow: '#f4bf75',
            brightBlue: '#66d9ef',
            brightMagenta: '#ae81ff',
            brightCyan: '#a1efe4',
            brightWhite: '#f9f8f5',
        },
    },

    nord: {
        label: 'Nord',
        theme: {
            background: '#2e3440',
            foreground: '#d8dee9',
            cursor: '#d8dee9',
            cursorAccent: '#2e3440',
            selectionBackground: 'rgba(136, 192, 208, 0.3)',
            selectionForeground: '#eceff4',
            black: '#3b4252',
            red: '#bf616a',
            green: '#a3be8c',
            yellow: '#ebcb8b',
            blue: '#81a1c1',
            magenta: '#b48ead',
            cyan: '#88c0d0',
            white: '#e5e9f0',
            brightBlack: '#4c566a',
            brightRed: '#bf616a',
            brightGreen: '#a3be8c',
            brightYellow: '#ebcb8b',
            brightBlue: '#81a1c1',
            brightMagenta: '#b48ead',
            brightCyan: '#8fbcbb',
            brightWhite: '#eceff4',
        },
    },

    dracula: {
        label: 'Dracula',
        theme: {
            background: '#282a36',
            foreground: '#f8f8f2',
            cursor: '#f8f8f2',
            cursorAccent: '#282a36',
            selectionBackground: 'rgba(68, 71, 90, 0.6)',
            selectionForeground: '#f8f8f2',
            black: '#21222c',
            red: '#ff5555',
            green: '#50fa7b',
            yellow: '#f1fa8c',
            blue: '#bd93f9',
            magenta: '#ff79c6',
            cyan: '#8be9fd',
            white: '#f8f8f2',
            brightBlack: '#6272a4',
            brightRed: '#ff6e6e',
            brightGreen: '#69ff94',
            brightYellow: '#ffffa5',
            brightBlue: '#d6acff',
            brightMagenta: '#ff92df',
            brightCyan: '#a4ffff',
            brightWhite: '#ffffff',
        },
    },

    'one-dark': {
        label: 'One Dark',
        theme: {
            background: '#282c34',
            foreground: '#abb2bf',
            cursor: '#528bff',
            cursorAccent: '#282c34',
            selectionBackground: 'rgba(82, 139, 255, 0.3)',
            selectionForeground: '#abb2bf',
            black: '#282c34',
            red: '#e06c75',
            green: '#98c379',
            yellow: '#e5c07b',
            blue: '#61afef',
            magenta: '#c678dd',
            cyan: '#56b6c2',
            white: '#abb2bf',
            brightBlack: '#5c6370',
            brightRed: '#e06c75',
            brightGreen: '#98c379',
            brightYellow: '#e5c07b',
            brightBlue: '#61afef',
            brightMagenta: '#c678dd',
            brightCyan: '#56b6c2',
            brightWhite: '#ffffff',
        },
    },

    'gruvbox-dark': {
        label: 'Gruvbox Dark',
        theme: {
            background: '#282828',
            foreground: '#ebdbb2',
            cursor: '#ebdbb2',
            cursorAccent: '#282828',
            selectionBackground: 'rgba(214, 93, 14, 0.3)',
            selectionForeground: '#ebdbb2',
            black: '#282828',
            red: '#cc241d',
            green: '#98971a',
            yellow: '#d79921',
            blue: '#458588',
            magenta: '#b16286',
            cyan: '#689d6a',
            white: '#a89984',
            brightBlack: '#928374',
            brightRed: '#fb4934',
            brightGreen: '#b8bb26',
            brightYellow: '#fabd2f',
            brightBlue: '#83a598',
            brightMagenta: '#d3869b',
            brightCyan: '#8ec07c',
            brightWhite: '#ebdbb2',
        },
    },

    'tokyo-night': {
        label: 'Tokyo Night',
        theme: {
            background: '#1a1b26',
            foreground: '#a9b1d6',
            cursor: '#c0caf5',
            cursorAccent: '#1a1b26',
            selectionBackground: 'rgba(41, 46, 66, 0.9)',
            selectionForeground: '#c0caf5',
            black: '#15161e',
            red: '#f7768e',
            green: '#9ece6a',
            yellow: '#e0af68',
            blue: '#7aa2f7',
            magenta: '#bb9af7',
            cyan: '#7dcfff',
            white: '#a9b1d6',
            brightBlack: '#414868',
            brightRed: '#f7768e',
            brightGreen: '#9ece6a',
            brightYellow: '#e0af68',
            brightBlue: '#7aa2f7',
            brightMagenta: '#bb9af7',
            brightCyan: '#7dcfff',
            brightWhite: '#c0caf5',
        },
    },
};

/**
 * Returns the theme object for the given theme name.
 * Falls back to 'charcoal' if not found.
 * @param {string} name
 * @returns {Object} xterm.js theme object
 */
function getTerminalTheme(name) {
    const entry = THEMES[name || 'charcoal'];
    return entry ? entry.theme : THEMES.charcoal.theme;
}

/**
 * Returns all theme names and labels for building a selector.
 * @returns {Array<{value: string, label: string}>}
 */
function listTerminalThemes() {
    return Object.entries(THEMES).map(([value, entry]) => ({
        value,
        label: entry.label,
    }));
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.getTerminalTheme = getTerminalTheme;
window.NexusTerminal.listTerminalThemes = listTerminalThemes;

})();
