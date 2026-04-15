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
	    }
	}
	export class AuditEntry {
	    id: string;
	    // Go type: time
	    timestamp: any;
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
	        this.timestamp = this.convertValues(source["timestamp"], null);
	        this.action = source["action"];
	        this.profileId = source["profileId"];
	        this.sessionId = source["sessionId"];
	        this.details = source["details"];
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
	        this.private = source["private"];
	        this.fork = source["fork"];
	        this.archived = source["archived"];
	        this.language = source["language"];
	        this.stars = source["stars"];
	        this.pushedAt = source["pushedAt"];
	        this.updatedAt = source["updatedAt"];
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
	    // Go type: time
	    firstSeen: any;
	    // Go type: time
	    lastSeen: any;
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
	        this.firstSeen = this.convertValues(source["firstSeen"], null);
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	        this.trusted = source["trusted"];
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
	    // Go type: time
	    lastConnected?: any;
	    connectTimeout: number;
	    readTimeout: number;
	    // Go type: time
	    createdAt: any;
	    // Go type: time
	    updatedAt: any;
	    terminalFontFamily: string;
	    terminalFontSize: number;
	    terminalScrollback: number;
	    terminalCursorStyle: string;
	    ssh?: SSHConfig;
	    telnet?: TelnetConfig;
	    raw?: RAWConfig;
	    github?: GitHubConfig;
	
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
	        this.lastConnected = this.convertValues(source["lastConnected"], null);
	        this.connectTimeout = source["connectTimeout"];
	        this.readTimeout = source["readTimeout"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
	        this.updatedAt = this.convertValues(source["updatedAt"], null);
	        this.terminalFontFamily = source["terminalFontFamily"];
	        this.terminalFontSize = source["terminalFontSize"];
	        this.terminalScrollback = source["terminalScrollback"];
	        this.terminalCursorStyle = source["terminalCursorStyle"];
	        this.ssh = this.convertValues(source["ssh"], SSHConfig);
	        this.telnet = this.convertValues(source["telnet"], TelnetConfig);
	        this.raw = this.convertValues(source["raw"], RAWConfig);
	        this.github = this.convertValues(source["github"], GitHubConfig);
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
	    // Go type: time
	    connectedAt: any;
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
	        this.connectedAt = this.convertValues(source["connectedAt"], null);
	        this.error = source["error"];
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

