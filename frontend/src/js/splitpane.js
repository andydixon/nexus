(function() {
/**
 * Nexus Terminal — Split Pane Manager.
 * Enables splitting session panels into multiple panes, each hosting
 * its own terminal session. Supports horizontal (top/bottom) and
 * vertical (left/right) splits with draggable resize dividers.
 */

var MIN_PANE_SIZE = 100; // px

/**
 * Unique pane id counter.
 * @type {number}
 */
var _paneIdCounter = 0;

function nextPaneId() {
    return 'pane-' + (++_paneIdCounter);
}

class SplitPaneManager {
    /**
     * @param {App} app
     */
    constructor(app) {
        this.app = app;

        /**
         * Map of sessionId → split tree root (or null if unsplit).
         * The tree is a simple object:
         *   Leaf:   { type: 'leaf', paneId, el, sessionId }
         *   Branch: { type: 'branch', direction, first, second, el, divider, ratio }
         * @type {Map<string, Object|null>}
         */
        this.trees = new Map();

        /**
         * Connections started from placeholders but not yet attached to a pane.
         * Closing a pane/tab marks its token cancelled so a late backend result
         * is disconnected instead of becoming an invisible session.
         * @type {Map<string, {ownerSessionId: string, cancelled: boolean, completed: boolean}>}
         */
        this.pendingConnections = new Map();
    }

    // ── Public API ───────────────────────────────────────────────────

    /**
     * Splits the active session panel vertically (left | right).
     */
    splitVertical() {
        this._splitActive('vertical');
    }

    /**
     * Splits the active session panel horizontally (top / bottom).
     */
    splitHorizontal() {
        this._splitActive('horizontal');
    }

    /**
     * Removes all split state for a session (used on tab close).
     * @param {string} sessionId
     */
    async cleanup(sessionId) {
        var root = this.trees.get(sessionId);
        this.trees.delete(sessionId);

        var childSessionIds = new Set();
        var self = this;
        function releaseNode(node) {
            if (!node) return;
            if (node.type === 'leaf') {
                self._cancelPendingConnection(node.paneId);
                if (node.sessionId && node.sessionId !== sessionId) {
                    childSessionIds.add(node.sessionId);
                }
                if (node.el) node.el._splitNode = null;
                node.parent = null;
                return;
            }
            if (node.cancelDrag) node.cancelDrag();
            releaseNode(node.first);
            releaseNode(node.second);
            if (node.el) node.el._splitNode = null;
            node.parent = null;
        }
        releaseNode(root);

        // A connection may still be resolving before its leaf has a session ID.
        // Its token remains captured by the promise and will disconnect any late
        // result after _cancelPendingConnection marks it cancelled.
        for (var [paneId, token] of this.pendingConnections) {
            if (token.ownerSessionId === sessionId) {
                token.cancelled = true;
                this.pendingConnections.delete(paneId);
            }
        }

        await Promise.allSettled(Array.from(childSessionIds, function(childId) {
            return self.app.closeSessionImmediate(childId, {
                removeTab: false,
                cleanupSplits: false
            });
        }));
    }

    /**
     * Re-fits all terminals visible in the active session's pane tree.
     */
    refitAll() {
        var activeId = this.app.state.activeSessionId;
        if (!activeId) return;
        var tree = this.trees.get(activeId);
        if (tree) this._refitTree(tree);
    }

    // ── Core split logic ─────────────────────────────────────────────

    /**
     * Splits the active session panel.
     * @param {'horizontal'|'vertical'} direction
     * @private
     */
    _splitActive(direction) {
        var sessionId = this.app.state.activeSessionId;
        if (!sessionId) return;

        var panel = document.getElementById('panel-' + sessionId);
        if (!panel) return;

        var tree = this.trees.get(sessionId);

        if (!tree) {
            // First split — wrap existing content
            tree = this._initTree(panel, sessionId);
        }

        // Find the focused leaf (or the first leaf)
        var leaf = this._findFocusedLeaf(tree) || this._firstLeaf(tree);
        if (!leaf) return;

        this._splitLeaf(leaf, direction, sessionId);
    }

    /**
     * Creates the initial tree structure from an unsplit panel.
     * Wraps the existing terminal-container in a split-pane leaf.
     * @param {HTMLElement} panel
     * @param {string} sessionId
     * @returns {Object} root leaf node
     * @private
     */
    _initTree(panel, sessionId) {
        var termContainer = panel.querySelector('.terminal-container');
        // For non-terminal panels (raw, github), wrap whatever content exists
        var contentChildren = Array.from(panel.children);

        var paneId = nextPaneId();
        var paneEl = document.createElement('div');
        paneEl.className = 'split-pane';
        paneEl.dataset.paneId = paneId;
        paneEl.style.flex = '1';

        // Move existing content into the pane
        contentChildren.forEach(function(child) {
            paneEl.appendChild(child);
        });

        // Reset terminal-container from absolute to relative positioning inside a pane
        if (termContainer) {
            termContainer.style.position = 'relative';
            termContainer.style.inset = 'auto';
            termContainer.style.flex = '1';
            termContainer.style.width = '100%';
            termContainer.style.height = '100%';
        }

        panel.appendChild(paneEl);

        var leaf = {
            type: 'leaf',
            paneId: paneId,
            el: paneEl,
            sessionId: sessionId,
            parent: null
        };

        paneEl._splitNode = leaf;
        this.trees.set(sessionId, leaf);

        // Add focus tracking
        this._addFocusTracking(paneEl);

        return leaf;
    }

    /**
     * Splits a leaf node into two panes.
     * @param {Object} leaf - The leaf node to split.
     * @param {'horizontal'|'vertical'} direction
     * @param {string} ownerSessionId - The session that owns this panel.
     * @private
     */
    _splitLeaf(leaf, direction, ownerSessionId) {
        var parentBranch = leaf.parent;
        var panel = this._findPanel(ownerSessionId);
        if (!panel) return;

        // Create a container for the two panes
        var containerEl = document.createElement('div');
        containerEl.className = 'split-container split-' + direction;
        containerEl.style.flex = leaf.el.style.flex || '1';

        // The existing leaf becomes the first child
        leaf.el.style.flex = '1';

        // Create divider
        var divider = document.createElement('div');
        divider.className = 'split-divider';

        // Create new empty pane
        var newPaneId = nextPaneId();
        var newPaneEl = document.createElement('div');
        newPaneEl.className = 'split-pane';
        newPaneEl.dataset.paneId = newPaneId;
        newPaneEl.style.flex = '1';

        // Add a placeholder with session picker to the new pane
        this._renderPlaceholder(newPaneEl, ownerSessionId);

        // Add close buttons to both panes
        this._addCloseButton(leaf.el, ownerSessionId);
        this._addCloseButton(newPaneEl, ownerSessionId);

        // Add focus tracking
        this._addFocusTracking(newPaneEl);

        // Build the new branch node
        var newLeaf = {
            type: 'leaf',
            paneId: newPaneId,
            el: newPaneEl,
            sessionId: null,
            parent: null
        };
        newPaneEl._splitNode = newLeaf;

        var branch = {
            type: 'branch',
            direction: direction,
            first: leaf,
            second: newLeaf,
            el: containerEl,
            divider: divider,
            ratio: 0.5,
            parent: parentBranch
        };

        leaf.parent = branch;
        newLeaf.parent = branch;
        containerEl._splitNode = branch;

        // Replace the leaf element in the DOM
        var leafParent = leaf.el.parentNode;
        leafParent.insertBefore(containerEl, leaf.el);
        containerEl.appendChild(leaf.el);
        containerEl.appendChild(divider);
        containerEl.appendChild(newPaneEl);

        // Update the tree structure
        if (parentBranch) {
            if (parentBranch.first === leaf) {
                parentBranch.first = branch;
            } else {
                parentBranch.second = branch;
            }
        } else {
            // This was the root leaf — the branch becomes the new root
            this.trees.set(ownerSessionId, branch);
        }

        // Set up divider dragging
        this._setupDividerDrag(divider, branch);

        // Refit terminals after layout change
        var self = this;
        requestAnimationFrame(function() {
            self._refitTree(self.trees.get(ownerSessionId));
        });
    }

    // ── Pane closing ─────────────────────────────────────────────────

    /**
     * Closes a pane and unsplits, keeping the sibling.
     * @param {string} paneId
     * @param {string} ownerSessionId
     * @private
     */
    _closePane(paneId, ownerSessionId) {
        var root = this.trees.get(ownerSessionId);
        if (!root) return;

        var leaf = this._findLeafById(root, paneId);
        if (!leaf || !leaf.parent) {
            // Only one pane left — can't close it
            return;
        }

        var branch = leaf.parent;
        var sibling = (branch.first === leaf) ? branch.second : branch.first;

        this._cancelPendingConnection(leaf.paneId);

        // If the closing pane has a child session, release both its frontend
        // renderer and backend connection. The owner pane cannot be closed
        // independently because it provides the tab's identity.
        if (leaf.sessionId && leaf.sessionId !== ownerSessionId) {
            this.app.closeSessionImmediate(leaf.sessionId, {
                removeTab: false,
                cleanupSplits: false
            });
            leaf.sessionId = null;
        }

        if (branch.cancelDrag) branch.cancelDrag();

        // Replace the branch element with the sibling element in the DOM
        sibling.el.style.flex = branch.el.style.flex || '1';
        branch.el.parentNode.insertBefore(sibling.el, branch.el);
        branch.el.remove();
        leaf.el._splitNode = null;
        branch.el._splitNode = null;

        // Remove close button from sibling if it's now the only pane
        sibling.parent = branch.parent;
        if (branch.parent) {
            if (branch.parent.first === branch) {
                branch.parent.first = sibling;
            } else {
                branch.parent.second = sibling;
            }
        } else {
            // Sibling is now root
            this.trees.set(ownerSessionId, sibling);
        }

        // Remove close button if we're back to a single leaf
        var newRoot = this.trees.get(ownerSessionId);
        if (newRoot && newRoot.type === 'leaf') {
            var closeBtn = newRoot.el.querySelector(':scope > .split-pane-close');
            if (closeBtn) closeBtn.remove();
        }

        // Refit terminals
        var self = this;
        requestAnimationFrame(function() {
            self._refitTree(self.trees.get(ownerSessionId));
        });
    }

    // ── Divider drag ─────────────────────────────────────────────────

    /**
     * Sets up mouse drag behaviour on a divider.
     * @param {HTMLElement} divider
     * @param {Object} branch
     * @private
     */
    _setupDividerDrag(divider, branch) {
        var self = this;
        var onMouseMove = null;
        var onMouseUp = null;
        var bodyClass = null;

        function stopDrag(refit) {
            if (bodyClass) document.body.classList.remove(bodyClass);
            divider.classList.remove('is-dragging');
            if (onMouseMove) document.removeEventListener('mousemove', onMouseMove);
            if (onMouseUp) document.removeEventListener('mouseup', onMouseUp);
            onMouseMove = null;
            onMouseUp = null;
            bodyClass = null;
            if (refit) self._refitTree(branch);
        }

        branch.cancelDrag = function() {
            stopDrag(false);
        };

        divider.addEventListener('mousedown', function(e) {
            e.preventDefault();
            stopDrag(false);
            var isHorizontal = branch.direction === 'horizontal';
            bodyClass = isHorizontal ? 'split-resizing-row' : 'split-resizing';
            document.body.classList.add(bodyClass);
            divider.classList.add('is-dragging');

            var containerRect = branch.el.getBoundingClientRect();
            var dividerSize = isHorizontal ? 4 : 4; // divider thickness

            onMouseMove = function(e) {
                var totalSize, mousePos;
                if (isHorizontal) {
                    totalSize = containerRect.height - dividerSize;
                    mousePos = e.clientY - containerRect.top;
                } else {
                    totalSize = containerRect.width - dividerSize;
                    mousePos = e.clientX - containerRect.left;
                }

                var ratio = mousePos / (totalSize + dividerSize);
                ratio = Math.max(MIN_PANE_SIZE / (totalSize + dividerSize), ratio);
                ratio = Math.min(1 - MIN_PANE_SIZE / (totalSize + dividerSize), ratio);

                branch.ratio = ratio;
                branch.first.el.style.flex = ratio + '';
                branch.second.el.style.flex = (1 - ratio) + '';

                // Refit terminals in this branch
                self._refitTree(branch);
            };

            onMouseUp = function() {
                stopDrag(true);
            };

            document.addEventListener('mousemove', onMouseMove);
            document.addEventListener('mouseup', onMouseUp);
        });
    }

    // ── Placeholder / session picker ─────────────────────────────────

    /**
     * Renders a placeholder with a list of available profiles to connect.
     * @param {HTMLElement} paneEl
     * @param {string} ownerSessionId
     * @private
     */
    _renderPlaceholder(paneEl, ownerSessionId) {
        var placeholder = document.createElement('div');
        placeholder.className = 'split-pane-placeholder';

        var icon = document.createElement('div');
        icon.className = 'split-pane-placeholder-icon';
        icon.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"></polyline><line x1="12" y1="19" x2="20" y2="19"></line></svg>';

        var text = document.createElement('div');
        text.className = 'split-pane-placeholder-text';
        text.textContent = 'Select a connection for this pane';

        placeholder.appendChild(icon);
        placeholder.appendChild(text);

        // Build profile list
        var profiles = this.app.state.profiles || [];
        var list = document.createElement('div');
        list.className = 'split-pane-profile-list';

        var self = this;
        var localBtn = document.createElement('button');
        localBtn.className = 'split-pane-profile-btn';

        var localBadge = document.createElement('span');
        localBadge.className = 'protocol-badge';
        localBadge.textContent = 'LOCAL';
        localBadge.style.color = self._protocolColor('local');

        var localLabel = document.createElement('span');
        localLabel.textContent = 'Local Machine';

        localBtn.appendChild(localBadge);
        localBtn.appendChild(localLabel);
        localBtn.addEventListener('click', function() {
            self._connectLocalInPane(paneEl, ownerSessionId);
        });
        list.appendChild(localBtn);

        profiles.forEach(function(profile) {
            var btn = document.createElement('button');
            btn.className = 'split-pane-profile-btn';

            var badge = document.createElement('span');
            badge.className = 'protocol-badge';
            badge.textContent = (profile.protocol || 'ssh').toUpperCase();
            badge.style.color = self._protocolColor(profile.protocol);

            var label = document.createElement('span');
            label.textContent = profile.displayName || profile.host || profile.id;

            btn.appendChild(badge);
            btn.appendChild(label);

            btn.addEventListener('click', function() {
                self._connectInPane(paneEl, profile, ownerSessionId);
            });

            list.appendChild(btn);
        });

        placeholder.appendChild(list);

        paneEl.appendChild(placeholder);
    }

    /**
     * Connects a profile in a specific pane.
     * @param {HTMLElement} paneEl
     * @param {Object} profile
     * @param {string} ownerSessionId
     * @private
     */
    _connectInPane(paneEl, profile, ownerSessionId) {
        var self = this;
        var token = this._beginPendingConnection(paneEl, ownerSessionId);

        // Remove placeholder
        var placeholder = paneEl.querySelector('.split-pane-placeholder');
        if (placeholder) placeholder.remove();

        // Connect via backend
        window.NexusTerminal.wailsService.ConnectSession(profile.id).then(async function(sessionInfo) {
            if (!self._completePendingConnection(paneEl, ownerSessionId, token)) {
                try {
                    await window.NexusTerminal.wailsService.DisconnectSession(sessionInfo.id);
                } catch (_) {
                    // The backend may already have completed the close.
                }
                return;
            }
            self._attachTerminalToPane(paneEl, sessionInfo, profile);

            self.app.notifications.info('Connecting to ' + (profile.displayName || profile.host) + '…');
        }).catch(function(err) {
            if (!self._completePendingConnection(paneEl, ownerSessionId, token)) {
                return;
            }
            self.app.notifications.error('Connection failed: ' + err);
            // Re-render placeholder on failure
            self._renderPlaceholder(paneEl, ownerSessionId);
        });
    }

    /**
     * Opens a local shell in a specific pane.
     * @param {HTMLElement} paneEl
     * @param {string} ownerSessionId
     * @private
     */
    _connectLocalInPane(paneEl, ownerSessionId) {
        var self = this;
        var token = this._beginPendingConnection(paneEl, ownerSessionId);
        var placeholder = paneEl.querySelector('.split-pane-placeholder');
        if (placeholder) placeholder.remove();

        window.NexusTerminal.wailsService.ConnectLocal().then(async function(sessionInfo) {
            if (!self._completePendingConnection(paneEl, ownerSessionId, token)) {
                try {
                    await window.NexusTerminal.wailsService.DisconnectSession(sessionInfo.id);
                } catch (_) {
                    // The backend may already have completed the close.
                }
                return;
            }
            var prefs = self.app.state.preferences || {};
            self._attachTerminalToPane(paneEl, sessionInfo, {
                terminalFontFamily: prefs.defaultFontFamily,
                terminalFontSize: prefs.defaultFontSize,
                terminalScrollback: prefs.defaultScrollback,
                terminalCursorStyle: prefs.defaultCursorStyle
            });
            self.app.notifications.info('Opening local terminal…');
        }).catch(function(err) {
            if (!self._completePendingConnection(paneEl, ownerSessionId, token)) {
                return;
            }
            self.app.notifications.error('Local terminal failed: ' + err);
            self._renderPlaceholder(paneEl, ownerSessionId);
        });
    }

    /**
     * Attaches a connected terminal session to a split pane.
     * @param {HTMLElement} paneEl
     * @param {Object} sessionInfo
     * @param {Object} terminalProfile
     * @private
     */
    _attachTerminalToPane(paneEl, sessionInfo, terminalProfile) {
        this.app.state.addSession(sessionInfo);

        var termContainer = document.createElement('div');
        termContainer.className = 'terminal-container';
        termContainer.id = 'split-term-' + sessionInfo.id;
        termContainer.style.position = 'relative';
        termContainer.style.flex = '1';
        termContainer.style.width = '100%';
        termContainer.style.height = '100%';
        paneEl.appendChild(termContainer);

        var node = paneEl._splitNode;
        if (node) {
            node.sessionId = sessionInfo.id;
        }

        this.app.terminals.createTerminal(sessionInfo.id, termContainer.id, terminalProfile);
    }

    /**
     * Tracks an asynchronous connection attempt owned by a pane.
     * @private
     */
    _beginPendingConnection(paneEl, ownerSessionId) {
        var paneId = paneEl.dataset.paneId;
        this._cancelPendingConnection(paneId);
        var token = {
            ownerSessionId: ownerSessionId,
            cancelled: false,
            completed: false
        };
        this.pendingConnections.set(paneId, token);
        return token;
    }

    /**
     * Returns true only if a connection result still belongs to a live pane.
     * @private
     */
    _completePendingConnection(paneEl, ownerSessionId, token) {
        if (token.completed) return false;
        token.completed = true;
        var paneId = paneEl.dataset.paneId;
        if (this.pendingConnections.get(paneId) === token) {
            this.pendingConnections.delete(paneId);
        }
        var root = this.trees.get(ownerSessionId);
        var leaf = this._findLeafById(root, paneId);
        return !token.cancelled &&
            paneEl.isConnected !== false &&
            leaf &&
            leaf.el === paneEl;
    }

    /**
     * Cancels a pending pane connection without waiting for the backend call.
     * A late successful result will disconnect itself.
     * @private
     */
    _cancelPendingConnection(paneId) {
        var token = this.pendingConnections.get(paneId);
        if (!token) return;
        token.cancelled = true;
        this.pendingConnections.delete(paneId);
    }

    // ── Tree traversal helpers ───────────────────────────────────────

    /**
     * Finds a leaf by paneId in the tree.
     * @param {Object} node
     * @param {string} paneId
     * @returns {Object|null}
     * @private
     */
    _findLeafById(node, paneId) {
        if (!node) return null;
        if (node.type === 'leaf') {
            return node.paneId === paneId ? node : null;
        }
        return this._findLeafById(node.first, paneId) || this._findLeafById(node.second, paneId);
    }

    /**
     * Returns the first leaf node in the tree.
     * @param {Object} node
     * @returns {Object|null}
     * @private
     */
    _firstLeaf(node) {
        if (!node) return null;
        if (node.type === 'leaf') return node;
        return this._firstLeaf(node.first);
    }

    /**
     * Returns the focused leaf node (one whose pane has .is-focused).
     * @param {Object} node
     * @returns {Object|null}
     * @private
     */
    _findFocusedLeaf(node) {
        if (!node) return null;
        if (node.type === 'leaf') {
            return node.el.classList.contains('is-focused') ? node : null;
        }
        return this._findFocusedLeaf(node.first) || this._findFocusedLeaf(node.second);
    }

    /**
     * Recursively refits all terminals in a tree node.
     * @param {Object} node
     * @private
     */
    _refitTree(node) {
        if (!node) return;
        if (node.type === 'leaf') {
            if (node.sessionId) {
                this.app.terminals.fitSession(node.sessionId);
            }
            return;
        }
        this._refitTree(node.first);
        this._refitTree(node.second);
    }

    /**
     * Finds the panel element for a session.
     * @param {string} sessionId
     * @returns {HTMLElement|null}
     * @private
     */
    _findPanel(sessionId) {
        return document.getElementById('panel-' + sessionId);
    }

    // ── UI helpers ───────────────────────────────────────────────────

    /**
     * Adds a close button to a pane element.
     * @param {HTMLElement} paneEl
     * @param {string} ownerSessionId
     * @private
     */
    _addCloseButton(paneEl, ownerSessionId) {
        // Don't add if already present
        if (paneEl.querySelector(':scope > .split-pane-close')) return;
        // The owner session supplies the tab/panel identity. Allowing that leaf
        // to close would leave an invisible backend session behind.
        if (paneEl._splitNode && paneEl._splitNode.sessionId === ownerSessionId) return;

        var btn = document.createElement('button');
        btn.className = 'split-pane-close';
        btn.textContent = '✕';
        btn.title = 'Close pane';

        var self = this;
        btn.addEventListener('click', function(e) {
            e.stopPropagation();
            self._closePane(paneEl.dataset.paneId, ownerSessionId);
        });

        paneEl.appendChild(btn);
    }

    /**
     * Adds focus tracking to a pane — marks it as focused when clicked.
     * @param {HTMLElement} paneEl
     * @private
     */
    _addFocusTracking(paneEl) {
        paneEl.addEventListener('mousedown', function() {
            // Remove focus from all sibling panes
            var allPanes = paneEl.closest('.session-panel');
            if (allPanes) {
                allPanes.querySelectorAll('.split-pane.is-focused').forEach(function(el) {
                    el.classList.remove('is-focused');
                });
            }
            paneEl.classList.add('is-focused');
        });
    }

    /**
     * Returns a colour for a protocol badge.
     * @param {string} protocol
     * @returns {string}
     * @private
     */
    _protocolColor(protocol) {
        var colors = {
            ssh: '#4ecdc4',
            telnet: '#ffa726',
            raw: '#ab47bc',
            github: '#7c8aff',
            local: '#66bb6a'
        };
        return colors[protocol] || '#4ecdc4';
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.SplitPaneManager = SplitPaneManager;

})();
