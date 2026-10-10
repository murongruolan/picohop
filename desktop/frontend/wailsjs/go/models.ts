export namespace app {
	
	export class NodeView {
	    id: string;
	    name: string;
	    protocol: string;
	    protocolLabel: string;
	    transport: string;
	    server: string;
	    port: number;
	    listenHost: string;
	    listenPort: number;
	    front: string;
	    frontLabel: string;
	    remark: string;
	    tlsEnabled: boolean;
	    active: boolean;
	    state: string;
	    stateLabel: string;
	    detail: string;
	    latencyMs: number;
	    realMs: number;
	    speedMbps: number;
	    exitIp: string;
	    todayUp: number;
	    todayDown: number;
	    todayTotal: number;
	    todayText: string;
	    activeConn: number;
	    totalConn: number;
	    // Go type: time
	    lastUsed: any;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new NodeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.protocolLabel = source["protocolLabel"];
	        this.transport = source["transport"];
	        this.server = source["server"];
	        this.port = source["port"];
	        this.listenHost = source["listenHost"];
	        this.listenPort = source["listenPort"];
	        this.front = source["front"];
	        this.frontLabel = source["frontLabel"];
	        this.remark = source["remark"];
	        this.tlsEnabled = source["tlsEnabled"];
	        this.active = source["active"];
	        this.state = source["state"];
	        this.stateLabel = source["stateLabel"];
	        this.detail = source["detail"];
	        this.latencyMs = source["latencyMs"];
	        this.realMs = source["realMs"];
	        this.speedMbps = source["speedMbps"];
	        this.exitIp = source["exitIp"];
	        this.todayUp = source["todayUp"];
	        this.todayDown = source["todayDown"];
	        this.todayTotal = source["todayTotal"];
	        this.todayText = source["todayText"];
	        this.activeConn = source["activeConn"];
	        this.totalConn = source["totalConn"];
	        this.lastUsed = this.convertValues(source["lastUsed"], null);
	        this.createdAt = this.convertValues(source["createdAt"], null);
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
	export class Summary {
	    total: number;
	    running: number;
	    error: number;
	    todayUp: number;
	    todayDown: number;
	    todayText: string;
	    activeConn: number;
	    frontDesc: string;
	    frontOk: boolean;
	    configPath: string;
	    dataDir: string;
	    allowLan: boolean;
	    startOnBoot: string;
	    listeningAll: number[];
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.running = source["running"];
	        this.error = source["error"];
	        this.todayUp = source["todayUp"];
	        this.todayDown = source["todayDown"];
	        this.todayText = source["todayText"];
	        this.activeConn = source["activeConn"];
	        this.frontDesc = source["frontDesc"];
	        this.frontOk = source["frontOk"];
	        this.configPath = source["configPath"];
	        this.dataDir = source["dataDir"];
	        this.allowLan = source["allowLan"];
	        this.startOnBoot = source["startOnBoot"];
	        this.listeningAll = source["listeningAll"];
	    }
	}

}

export namespace config {
	
	export class Front {
	    type: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	    timeoutMs: number;
	
	    static createFrom(source: any = {}) {
	        return new Front(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.timeoutMs = source["timeoutMs"];
	    }
	}
	export class Issue {
	    scope: string;
	    field: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Issue(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.scope = source["scope"];
	        this.field = source["field"];
	        this.message = source["message"];
	    }
	}
	export class WireGuardConfig {
	    privateKey: string;
	    address: string;
	    peerPublicKey: string;
	    presharedKey?: string;
	    allowedIPs: string;
	    persistentKeepalive: number;
	    mtu: number;
	    dnsServers?: string[];
	
	    static createFrom(source: any = {}) {
	        return new WireGuardConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.privateKey = source["privateKey"];
	        this.address = source["address"];
	        this.peerPublicKey = source["peerPublicKey"];
	        this.presharedKey = source["presharedKey"];
	        this.allowedIPs = source["allowedIPs"];
	        this.persistentKeepalive = source["persistentKeepalive"];
	        this.mtu = source["mtu"];
	        this.dnsServers = source["dnsServers"];
	    }
	}
	export class TLSOptions {
	    enabled: boolean;
	    serverName: string;
	    alpn?: string[];
	    allowInsecure: boolean;
	    fingerprint?: string;
	    realityPublicKey?: string;
	    realityShortId?: string;
	    realitySpiderX?: string;
	
	    static createFrom(source: any = {}) {
	        return new TLSOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.serverName = source["serverName"];
	        this.alpn = source["alpn"];
	        this.allowInsecure = source["allowInsecure"];
	        this.fingerprint = source["fingerprint"];
	        this.realityPublicKey = source["realityPublicKey"];
	        this.realityShortId = source["realityShortId"];
	        this.realitySpiderX = source["realitySpiderX"];
	    }
	}
	export class TransportOptions {
	    network: string;
	    path?: string;
	    host?: string;
	    serviceName?: string;
	    headerType?: string;
	
	    static createFrom(source: any = {}) {
	        return new TransportOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.network = source["network"];
	        this.path = source["path"];
	        this.host = source["host"];
	        this.serviceName = source["serviceName"];
	        this.headerType = source["headerType"];
	    }
	}
	export class Node {
	    id: string;
	    name: string;
	    protocol: string;
	    remark: string;
	    listenHost: string;
	    listenPort: number;
	    listenUsername?: string;
	    listenPassword?: string;
	    front: string;
	    server: string;
	    port: number;
	    username?: string;
	    password?: string;
	    uuid?: string;
	    alterId?: number;
	    security?: string;
	    flow?: string;
	    method?: string;
	    upMbps?: number;
	    downMbps?: number;
	    obfs?: string;
	    obfsPassword?: string;
	    pinSHA256?: string;
	    transport: TransportOptions;
	    tls: TLSOptions;
	    wireguard?: WireGuardConfig;
	    active: boolean;
	    // Go type: time
	    lastUsed?: any;
	    latencyMs?: number;
	    realMs?: number;
	    speedMBps?: number;
	    statsDay?: string;
	    upBytes?: number;
	    downBytes?: number;
	    // Go type: time
	    createdAt: any;
	
	    static createFrom(source: any = {}) {
	        return new Node(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.remark = source["remark"];
	        this.listenHost = source["listenHost"];
	        this.listenPort = source["listenPort"];
	        this.listenUsername = source["listenUsername"];
	        this.listenPassword = source["listenPassword"];
	        this.front = source["front"];
	        this.server = source["server"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.uuid = source["uuid"];
	        this.alterId = source["alterId"];
	        this.security = source["security"];
	        this.flow = source["flow"];
	        this.method = source["method"];
	        this.upMbps = source["upMbps"];
	        this.downMbps = source["downMbps"];
	        this.obfs = source["obfs"];
	        this.obfsPassword = source["obfsPassword"];
	        this.pinSHA256 = source["pinSHA256"];
	        this.transport = this.convertValues(source["transport"], TransportOptions);
	        this.tls = this.convertValues(source["tls"], TLSOptions);
	        this.wireguard = this.convertValues(source["wireguard"], WireGuardConfig);
	        this.active = source["active"];
	        this.lastUsed = this.convertValues(source["lastUsed"], null);
	        this.latencyMs = source["latencyMs"];
	        this.realMs = source["realMs"];
	        this.speedMBps = source["speedMBps"];
	        this.statsDay = source["statsDay"];
	        this.upBytes = source["upBytes"];
	        this.downBytes = source["downBytes"];
	        this.createdAt = this.convertValues(source["createdAt"], null);
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
	export class ProtocolInfo {
	    value: string;
	    label: string;
	    defaultPort: number;
	    showUuid: boolean;
	    showAlterId: boolean;
	    showSecurity: boolean;
	    showFlow: boolean;
	    showMethod: boolean;
	    showPassword: boolean;
	    showUsername: boolean;
	    showTransport: boolean;
	    showTls: boolean;
	    showReality: boolean;
	    showHysteria2: boolean;
	    showWireGuard: boolean;
	    tlsDefault: boolean;
	    tlsRequired: boolean;
	    supportsUdp: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProtocolInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.label = source["label"];
	        this.defaultPort = source["defaultPort"];
	        this.showUuid = source["showUuid"];
	        this.showAlterId = source["showAlterId"];
	        this.showSecurity = source["showSecurity"];
	        this.showFlow = source["showFlow"];
	        this.showMethod = source["showMethod"];
	        this.showPassword = source["showPassword"];
	        this.showUsername = source["showUsername"];
	        this.showTransport = source["showTransport"];
	        this.showTls = source["showTls"];
	        this.showReality = source["showReality"];
	        this.showHysteria2 = source["showHysteria2"];
	        this.showWireGuard = source["showWireGuard"];
	        this.tlsDefault = source["tlsDefault"];
	        this.tlsRequired = source["tlsRequired"];
	        this.supportsUdp = source["supportsUdp"];
	    }
	}
	export class Settings {
	    defaultListenHost: string;
	    allowLan: boolean;
	    portStart: number;
	    idleTimeoutSec: number;
	    autoActivateOnLaunch: boolean;
	    tcpTestTimeoutMs: number;
	    testConcurrency: number;
	    realTestUrl: string;
	    speedTestUrl: string;
	    speedTestSeconds: number;
	    theme: string;
	    notificationDurationMs: number;
	    logLines: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.defaultListenHost = source["defaultListenHost"];
	        this.allowLan = source["allowLan"];
	        this.portStart = source["portStart"];
	        this.idleTimeoutSec = source["idleTimeoutSec"];
	        this.autoActivateOnLaunch = source["autoActivateOnLaunch"];
	        this.tcpTestTimeoutMs = source["tcpTestTimeoutMs"];
	        this.testConcurrency = source["testConcurrency"];
	        this.realTestUrl = source["realTestUrl"];
	        this.speedTestUrl = source["speedTestUrl"];
	        this.speedTestSeconds = source["speedTestSeconds"];
	        this.theme = source["theme"];
	        this.notificationDurationMs = source["notificationDurationMs"];
	        this.logLines = source["logLines"];
	    }
	}
	
	

}

export namespace main {
	
	export class PendingNode {
	    node: config.Node;
	    protocol: string;
	    protocolLabel: string;
	    transport: string;
	    server: string;
	    port: number;
	    suggestedPort: number;
	    listenPortFree: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PendingNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.node = this.convertValues(source["node"], config.Node);
	        this.protocol = source["protocol"];
	        this.protocolLabel = source["protocolLabel"];
	        this.transport = source["transport"];
	        this.server = source["server"];
	        this.port = source["port"];
	        this.suggestedPort = source["suggestedPort"];
	        this.listenPortFree = source["listenPortFree"];
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
	export class ImportResult {
	    ok: boolean;
	    source: string;
	    nodes: PendingNode[];
	    message: string;
	    raw: string;
	
	    static createFrom(source: any = {}) {
	        return new ImportResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.source = source["source"];
	        this.nodes = this.convertValues(source["nodes"], PendingNode);
	        this.message = source["message"];
	        this.raw = source["raw"];
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
	export class MetaView {
	    version: string;
	    dataDir: string;
	    configPath: string;
	    logPath: string;
	    protocols: config.ProtocolInfo[];
	    shadowsocksMethods: string[];
	    vmessSecurities: string[];
	    transportNetworks: string[];
	    utlsFingerprints: string[];
	    engineOk: boolean;
	    engineError: string;
	    goVersion: string;
	    defaultPortStart: number;
	    randomPortMin: number;
	    randomPortMax: number;
	    devOpen: string;
	
	    static createFrom(source: any = {}) {
	        return new MetaView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.dataDir = source["dataDir"];
	        this.configPath = source["configPath"];
	        this.logPath = source["logPath"];
	        this.protocols = this.convertValues(source["protocols"], config.ProtocolInfo);
	        this.shadowsocksMethods = source["shadowsocksMethods"];
	        this.vmessSecurities = source["vmessSecurities"];
	        this.transportNetworks = source["transportNetworks"];
	        this.utlsFingerprints = source["utlsFingerprints"];
	        this.engineOk = source["engineOk"];
	        this.engineError = source["engineError"];
	        this.goVersion = source["goVersion"];
	        this.defaultPortStart = source["defaultPortStart"];
	        this.randomPortMin = source["randomPortMin"];
	        this.randomPortMax = source["randomPortMax"];
	        this.devOpen = source["devOpen"];
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
	
	export class ShareResult {
	    ok: boolean;
	    name: string;
	    link: string;
	    qrDataUrl: string;
	    protocolLabel: string;
	    transport: string;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new ShareResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.name = source["name"];
	        this.link = source["link"];
	        this.qrDataUrl = source["qrDataUrl"];
	        this.protocolLabel = source["protocolLabel"];
	        this.transport = source["transport"];
	        this.error = source["error"];
	    }
	}

}

export namespace ports {
	
	export class Status {
	    port: number;
	    valid: boolean;
	    inProject: boolean;
	    inProjectBy: string;
	    busy: boolean;
	    free: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.port = source["port"];
	        this.valid = source["valid"];
	        this.inProject = source["inProject"];
	        this.inProjectBy = source["inProjectBy"];
	        this.busy = source["busy"];
	        this.free = source["free"];
	        this.message = source["message"];
	    }
	}

}

