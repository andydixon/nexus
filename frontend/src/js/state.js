(function() {
/**
 * Nexus Terminal — application state management.
 * Provides a centralised store for profiles, sessions, and preferences,
 * emitting change events so the UI stays in sync.
 */

class State {
    constructor() {
        /** @type {Array<Object>} */
        this.profiles = [];

        /** @type {Map<string, Object>} sessionId → SessionInfo */
        this.sessions = new Map();

        /** @type {string|null} */
        this.activeSessionId = null;

        /** @type {Object} */
        this.preferences = {};

        /** @type {boolean} */
        this.secretStoreUnlocked = false;

        /** @type {boolean} */
        this.secretStoreInitialised = false;

        /** @type {boolean} Whether broadcast mode is active (keystrokes to all sessions). */
        this.broadcastMode = false;

        /** @type {Map<string, Set<Function>>} event name → callbacks */
        this._listeners = new Map();
    }

    // ── Event emitter ────────────────────────────────────────────────

    /**
     * Subscribes to a state change event.
     * @param {string} event
     * @param {Function} callback
     */
    on(event, callback) {
        if (!this._listeners.has(event)) {
            this._listeners.set(event, new Set());
        }
        this._listeners.get(event).add(callback);
    }

    /**
     * Unsubscribes from a state change event.
     * @param {string} event
     * @param {Function} callback
     */
    off(event, callback) {
        const set = this._listeners.get(event);
        if (set) {
            set.delete(callback);
        }
    }

    /**
     * Emits a state change event to all subscribers.
     * @param {string} event
     * @param {*} data
     */
    emit(event, data) {
        const set = this._listeners.get(event);
        if (set) {
            for (const cb of set) {
                try {
                    cb(data);
                } catch (err) {
                    console.error(`State listener error [${event}]:`, err);
                }
            }
        }
    }

    // ── Profiles ─────────────────────────────────────────────────────

    /**
     * Replaces the entire profiles list.
     * @param {Array<Object>} profiles
     */
    setProfiles(profiles) {
        this.profiles = profiles;
        this.emit('profiles:changed', this.profiles);
    }

    /**
     * Adds a single profile.
     * @param {Object} profile
     */
    addProfile(profile) {
        this.profiles.push(profile);
        this.emit('profiles:changed', this.profiles);
        this.emit('profile:added', profile);
    }

    /**
     * Updates an existing profile in place.
     * @param {Object} profile
     */
    updateProfile(profile) {
        const idx = this.profiles.findIndex((p) => p.id === profile.id);
        if (idx !== -1) {
            this.profiles[idx] = profile;
        }
        this.emit('profiles:changed', this.profiles);
        this.emit('profile:updated', profile);
    }

    /**
     * Removes a profile by ID.
     * @param {string} id
     */
    removeProfile(id) {
        this.profiles = this.profiles.filter((p) => p.id !== id);
        this.emit('profiles:changed', this.profiles);
        this.emit('profile:removed', id);
    }

    /**
     * Returns a profile by ID, or undefined.
     * @param {string} id
     * @returns {Object|undefined}
     */
    getProfile(id) {
        return this.profiles.find((p) => p.id === id);
    }

    // ── Sessions ─────────────────────────────────────────────────────

    /**
     * Registers a new session.
     * @param {Object} sessionInfo
     */
    addSession(sessionInfo) {
        this.sessions.set(sessionInfo.id, sessionInfo);
        this.emit('sessions:changed', this.sessions);
        this.emit('session:added', sessionInfo);
    }

    /**
     * Merges partial updates into an existing session.
     * @param {string} id
     * @param {Object} updates
     */
    updateSession(id, updates) {
        const existing = this.sessions.get(id);
        if (existing) {
            Object.assign(existing, updates);
            this.emit('sessions:changed', this.sessions);
            this.emit('session:updated', existing);
        }
    }

    /**
     * Removes a session.
     * @param {string} id
     */
    removeSession(id) {
        this.sessions.delete(id);
        if (this.activeSessionId === id) {
            // Activate the most recent remaining session, or null
            const remaining = [...this.sessions.keys()];
            this.activeSessionId = remaining.length > 0 ? remaining[remaining.length - 1] : null;
        }
        this.emit('sessions:changed', this.sessions);
        this.emit('session:removed', id);
    }

    /**
     * Returns session info by ID, or undefined.
     * @param {string} id
     * @returns {Object|undefined}
     */
    getSession(id) {
        return this.sessions.get(id);
    }

    /**
     * Sets the currently visible/active session.
     * @param {string|null} id
     */
    setActiveSession(id) {
        this.activeSessionId = id;
        this.emit('session:activated', id);
    }

    /**
     * Returns an ordered array of all sessions.
     * @returns {Array<Object>}
     */
    getAllSessions() {
        return [...this.sessions.values()];
    }

    // ── Preferences ──────────────────────────────────────────────────

    /**
     * Replaces the entire preferences object.
     * @param {Object} prefs
     */
    setPreferences(prefs) {
        this.preferences = prefs;
        this.emit('preferences:changed', this.preferences);
    }

    // ── Secret store ─────────────────────────────────────────────────

    /**
     * Updates the secret store state.
     * @param {boolean} unlocked
     */
    setSecretStoreState(unlocked) {
        this.secretStoreUnlocked = unlocked;
        this.emit('secretStore:changed', unlocked);
    }

    /**
     * Marks whether the secret store has been initialised (vault exists).
     * @param {boolean} initialised
     */
    setSecretStoreInitialised(initialised) {
        this.secretStoreInitialised = initialised;
        this.emit('secretStore:init', initialised);
    }
}

window.NexusTerminal = window.NexusTerminal || {};
window.NexusTerminal.State = State;

})();
