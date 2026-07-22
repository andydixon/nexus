export namespace api {
	
	export class SSHKeyInfo {
	    name: string;
	    type: string;
	    bits: string;
	    publicKey: string;
	    privateKeyPath: string;
	
	    static createFrom(source: any = {}) {
	        return new SSHKeyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.type = source["type"];
	        this.bits = source["bits"];
	        this.publicKey = source["publicKey"];
	        this.privateKeyPath = source["privateKeyPath"];
	    }
	}
	export class leviathanDocState {
	    name: string;
	    size: number;
	    dirty: boolean;
	    undoLen: number;
	    redoLen: number;
	    compare?: leviathanDocState;
	    previewHex: any[];
	
	    static createFrom(source: any = {}) {
	        return new leviathanDocState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.size = source["size"];
	        this.dirty = source["dirty"];
	        this.undoLen = source["undoLen"];
	        this.redoLen = source["redoLen"];
	        this.compare = this.convertValues(source["compare"], leviathanDocState);
	        this.previewHex = source["previewHex"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace models {
	
	export class AppPreferences {
	    theme: string;
	    terminalTheme: string;
	    defaultProtocol: string;
	    confirmOnClose: boolean;
	    confirmOnDisconnect: boolean;
	    showStatusBar: boolean;
	    sidebarWidth: number;
	    defaultFontFamily: string;
	    defaultFontSize: number;
	    defaultScrollback: number;
	    defaultCursorStyle: string;
	    useNativePaste: boolean;
	    localShell: string;
	    leviathanEnabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new AppPreferences(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.theme = source["theme"];
	        this.terminalTheme = source["terminalTheme"];
	        this.defaultProtocol = source["defaultProtocol"];
	        this.confirmOnClose = source["confirmOnClose"];
	        this.confirmOnDisconnect = source["confirmOnDisconnect"];
	        this.showStatusBar = source["showStatusBar"];
	        this.sidebarWidth = source["sidebarWidth"];
	        this.defaultFontFamily = source["defaultFontFamily"];
	        this.defaultFontSize = source["defaultFontSize"];
	        this.defaultScrollback = source["defaultScrollback"];
	        this.defaultCursorStyle = source["defaultCursorStyle"];
	        this.useNativePaste = source["useNativePaste"];
	        this.localShell = source["localShell"];
	        this.leviathanEnabled = source["leviathanEnabled"];
	    }
	}
	export class AuditEntry {
	    id: string;
	    timestamp: string;
	    action: string;
	    profileId?: string;
	    sessionId?: string;
	    details: string;
	
	    static createFrom(source: any = {}) {
	        return new AuditEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.timestamp = source["timestamp"];
	        this.action = source["action"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.details = source["details"];
	    }
	}
	export class GitHubConfig {
	    baseURL: string;
	    secretMode: string;
	    refreshIntervalSecs: number;
	
	    static createFrom(source: any = {}) {
	        return new GitHubConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.baseURL = source["baseURL"];
	        this.secretMode = source["secretMode"];
	        this.refreshIntervalSecs = source["refreshIntervalSecs"];
	    }
	}
	export class GitHubRepo {
	    fullName: string;
	    name: string;
	    owner: string;
	    description: string;
	    htmlURL: string;
	    defaultBranch: string;
	    latestCommitter: string;
	    latestCommitAt: string;
	    private: boolean;
	    fork: boolean;
	    archived: boolean;
	    language: string;
	    stars: number;
	    pushedAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new GitHubRepo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fullName = source["fullName"];
	        this.name = source["name"];
	        this.owner = source["owner"];
	        this.description = source["description"];
	        this.htmlURL = source["htmlURL"];
	        this.defaultBranch = source["defaultBranch"];
	        this.latestCommitter = source["latestCommitter"];
	        this.latestCommitAt = source["latestCommitAt"];
	        this.private = source["private"];
	        this.fork = source["fork"];
	        this.archived = source["archived"];
	        this.language = source["language"];
	        this.stars = source["stars"];
	        this.pushedAt = source["pushedAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class IRCConfig {
	    nick: string;
	    realName: string;
	    useTLS: boolean;
	    serverPasswordRequired: boolean;
	    serverPasswordMode: string;
	    nickServIdentify: boolean;
	    nickServAccount: string;
	    nickServMode: string;
	    autoJoinRooms: string[];
	    sshTunnel: boolean;
	    sshHost: string;
	    sshPort: number;
	    sshUsername: string;
	    sshAuthMethod: string;
	    sshSecretMode: string;
	    sshPrivateKeyPath: string;
	    sshAgentAuth: boolean;
	    autoReconnect: boolean;
	    maxReconnectTries: number;
	
	    static createFrom(source: any = {}) {
	        return new IRCConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nick = source["nick"];
	        this.realName = source["realName"];
	        this.useTLS = source["useTLS"];
	        this.serverPasswordRequired = source["serverPasswordRequired"];
	        this.serverPasswordMode = source["serverPasswordMode"];
	        this.nickServIdentify = source["nickServIdentify"];
	        this.nickServAccount = source["nickServAccount"];
	        this.nickServMode = source["nickServMode"];
	        this.autoJoinRooms = source["autoJoinRooms"];
	        this.sshTunnel = source["sshTunnel"];
	        this.sshHost = source["sshHost"];
	        this.sshPort = source["sshPort"];
	        this.sshUsername = source["sshUsername"];
	        this.sshAuthMethod = source["sshAuthMethod"];
	        this.sshSecretMode = source["sshSecretMode"];
	        this.sshPrivateKeyPath = source["sshPrivateKeyPath"];
	        this.sshAgentAuth = source["sshAgentAuth"];
	        this.autoReconnect = source["autoReconnect"];
	        this.maxReconnectTries = source["maxReconnectTries"];
	    }
	}
	export class JumpHost {
	    host: string;
	    port: number;
	    username: string;
	    authMethod: string;
	    secretMode: string;
	    privateKeyPath: string;
	
	    static createFrom(source: any = {}) {
	        return new JumpHost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.authMethod = source["authMethod"];
	        this.secretMode = source["secretMode"];
	        this.privateKeyPath = source["privateKeyPath"];
	    }
	}
	export class KnownHost {
	    id: string;
	    host: string;
	    port: number;
	    keyType: string;
	    fingerprint: string;
	    publicKey: string;
	    firstSeen: string;
	    lastSeen: string;
	    trusted: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KnownHost(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.keyType = source["keyType"];
	        this.fingerprint = source["fingerprint"];
	        this.publicKey = source["publicKey"];
	        this.firstSeen = source["firstSeen"];
	        this.lastSeen = source["lastSeen"];
	        this.trusted = source["trusted"];
	    }
	}
	export class LoginScriptStep {
	    type: string;
	    value: string;
	    timeout: number;
	
	    static createFrom(source: any = {}) {
	        return new LoginScriptStep(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.value = source["value"];
	        this.timeout = source["timeout"];
	    }
	}
	export class PayloadPreset {
	    id: string;
	    name: string;
	    payload: string;
	    format: string;
	
	    static createFrom(source: any = {}) {
	        return new PayloadPreset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.payload = source["payload"];
	        this.format = source["format"];
	    }
	}
	export class PortForward {
	    id: string;
	    direction: string;
	    bindHost: string;
	    bindPort: number;
	    destHost: string;
	    destPort: number;
	    enabled: boolean;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new PortForward(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.direction = source["direction"];
	        this.bindHost = source["bindHost"];
	        this.bindPort = source["bindPort"];
	        this.destHost = source["destHost"];
	        this.destPort = source["destPort"];
	        this.enabled = source["enabled"];
	        this.label = source["label"];
	    }
	}
	export class VNCConfig {
	    vncHost: string;
	    vncPort: number;
	    secretMode: string;
	    viewOnly: boolean;
	    scaleToFit: boolean;
	    quality: number;
	    sharedSession: boolean;
	    sshTunnel: boolean;
	    sshHost: string;
	    sshPort: number;
	    sshUsername: string;
	    sshAuthMethod: string;
	    sshSecretMode: string;
	    sshPrivateKeyPath: string;
	    sshAgentAuth: boolean;
	
	    static createFrom(source: any = {}) {
	        return new VNCConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vncHost = source["vncHost"];
	        this.vncPort = source["vncPort"];
	        this.secretMode = source["secretMode"];
	        this.viewOnly = source["viewOnly"];
	        this.scaleToFit = source["scaleToFit"];
	        this.quality = source["quality"];
	        this.sharedSession = source["sharedSession"];
	        this.sshTunnel = source["sshTunnel"];
	        this.sshHost = source["sshHost"];
	        this.sshPort = source["sshPort"];
	        this.sshUsername = source["sshUsername"];
	        this.sshAuthMethod = source["sshAuthMethod"];
	        this.sshSecretMode = source["sshSecretMode"];
	        this.sshPrivateKeyPath = source["sshPrivateKeyPath"];
	        this.sshAgentAuth = source["sshAgentAuth"];
	    }
	}
	export class RAWConfig {
	    autoReconnect: boolean;
	    lineEnding: string;
	    encoding: string;
	    showTimestamps: boolean;
	    showDirection: boolean;
	    payloadPresets: PayloadPreset[];
	
	    static createFrom(source: any = {}) {
	        return new RAWConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.autoReconnect = source["autoReconnect"];
	        this.lineEnding = source["lineEnding"];
	        this.encoding = source["encoding"];
	        this.showTimestamps = source["showTimestamps"];
	        this.showDirection = source["showDirection"];
	        this.payloadPresets = this.convertValues(source["payloadPresets"], PayloadPreset);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class TelnetConfig {
	    localEcho: boolean;
	    lineEnding: string;
	    terminalType: string;
	    encoding: string;
	    negotiationDebug: boolean;
	    lineMode: boolean;
	    autoReconnect: boolean;
	    maxReconnectTries: number;
	    loginScripts: LoginScriptStep[];
	
	    static createFrom(source: any = {}) {
	        return new TelnetConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.localEcho = source["localEcho"];
	        this.lineEnding = source["lineEnding"];
	        this.terminalType = source["terminalType"];
	        this.encoding = source["encoding"];
	        this.negotiationDebug = source["negotiationDebug"];
	        this.lineMode = source["lineMode"];
	        this.autoReconnect = source["autoReconnect"];
	        this.maxReconnectTries = source["maxReconnectTries"];
	        this.loginScripts = this.convertValues(source["loginScripts"], LoginScriptStep);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SSHConfig {
	    authMethods: string[];
	    secretMode: string;
	    privateKeyPath: string;
	    keySecretMode: string;
	    hostKeyPolicy: string;
	    terminalType: string;
	    keepaliveInterval: number;
	    keepaliveCountMax: number;
	    compression: boolean;
	    agentAuth: boolean;
	    agentForwarding: boolean;
	    autoReconnect: boolean;
	    maxReconnectTries: number;
	    loginScripts: LoginScriptStep[];
	    totpSecret: string;
	    proxyType: string;
	    proxyHost: string;
	    proxyPort: number;
	    proxyUsername: string;
	    proxyPassword: string;
	    jumpHosts: JumpHost[];
	    portForwards: PortForward[];
	    startupCommand: string;
	    envVars: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new SSHConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.authMethods = source["authMethods"];
	        this.secretMode = source["secretMode"];
	        this.privateKeyPath = source["privateKeyPath"];
	        this.keySecretMode = source["keySecretMode"];
	        this.hostKeyPolicy = source["hostKeyPolicy"];
	        this.terminalType = source["terminalType"];
	        this.keepaliveInterval = source["keepaliveInterval"];
	        this.keepaliveCountMax = source["keepaliveCountMax"];
	        this.compression = source["compression"];
	        this.agentAuth = source["agentAuth"];
	        this.agentForwarding = source["agentForwarding"];
	        this.autoReconnect = source["autoReconnect"];
	        this.maxReconnectTries = source["maxReconnectTries"];
	        this.loginScripts = this.convertValues(source["loginScripts"], LoginScriptStep);
	        this.totpSecret = source["totpSecret"];
	        this.proxyType = source["proxyType"];
	        this.proxyHost = source["proxyHost"];
	        this.proxyPort = source["proxyPort"];
	        this.proxyUsername = source["proxyUsername"];
	        this.proxyPassword = source["proxyPassword"];
	        this.jumpHosts = this.convertValues(source["jumpHosts"], JumpHost);
	        this.portForwards = this.convertValues(source["portForwards"], PortForward);
	        this.startupCommand = source["startupCommand"];
	        this.envVars = source["envVars"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Profile {
	    id: string;
	    displayName: string;
	    description: string;
	    protocol: string;
	    host: string;
	    port: number;
	    username: string;
	    tags: string[];
	    folder: string;
	    environment: string;
	    notes: string;
	    favourite: boolean;
	    lastConnected?: string;
	    connectTimeout: number;
	    readTimeout: number;
	    createdAt: string;
	    updatedAt: string;
	    terminalFontFamily: string;
	    terminalFontSize: number;
	    terminalScrollback: number;
	    terminalCursorStyle: string;
	    ssh?: SSHConfig;
	    telnet?: TelnetConfig;
	    raw?: RAWConfig;
	    github?: GitHubConfig;
	    vnc?: VNCConfig;
	    irc?: IRCConfig;
	
	    static createFrom(source: any = {}) {
	        return new Profile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.description = source["description"];
	        this.protocol = source["protocol"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.tags = source["tags"];
	        this.folder = source["folder"];
	        this.environment = source["environment"];
	        this.notes = source["notes"];
	        this.favourite = source["favourite"];
	        this.lastConnected = source["lastConnected"];
	        this.connectTimeout = source["connectTimeout"];
	        this.readTimeout = source["readTimeout"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	        this.terminalFontFamily = source["terminalFontFamily"];
	        this.terminalFontSize = source["terminalFontSize"];
	        this.terminalScrollback = source["terminalScrollback"];
	        this.terminalCursorStyle = source["terminalCursorStyle"];
	        this.ssh = this.convertValues(source["ssh"], SSHConfig);
	        this.telnet = this.convertValues(source["telnet"], TelnetConfig);
	        this.raw = this.convertValues(source["raw"], RAWConfig);
	        this.github = this.convertValues(source["github"], GitHubConfig);
	        this.vnc = this.convertValues(source["vnc"], VNCConfig);
	        this.irc = this.convertValues(source["irc"], IRCConfig);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	export class SessionInfo {
	    id: string;
	    profileId: string;
	    profileName: string;
	    protocol: string;
	    host: string;
	    port: number;
	    state: string;
	    tabName: string;
	    pinned: boolean;
	    connectedAt: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.profileId = source["profileId"];
	        this.profileName = source["profileName"];
	        this.protocol = source["protocol"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.state = source["state"];
	        this.tabName = source["tabName"];
	        this.pinned = source["pinned"];
	        this.connectedAt = source["connectedAt"];
	        this.error = source["error"];
	    }
	}
	export class Snippet {
	    id: string;
	    name: string;
	    command: string;
	    description: string;
	    folder: string;
	    tags: string[];
	    createdAt: string;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new Snippet(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.command = source["command"];
	        this.description = source["description"];
	        this.folder = source["folder"];
	        this.tags = source["tags"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	

}

