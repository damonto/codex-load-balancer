const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { TextEncoder } = require("util");
const { webcrypto } = require("crypto");

const authURL = "https://auth.openai.com/create-account/password";
const defaultSDKURL = "https://sentinel.openai.com/sentinel/20260219f9f6/sdk.js";
const localStorageKeys = [
  "d2bd098fc8793ef1",
  "statsig.session_id.444584300",
  "statsig.last_modified_time.evaluations",
  "statsig.stable_id.444584300",
  "6fbbfe1cd1015f3d",
  "statsig.cached.evaluations.3523433505",
];

function readStdin() {
  return new Promise((resolve, reject) => {
    let data = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (chunk) => {
      data += chunk;
    });
    process.stdin.on("end", () => resolve(data));
    process.stdin.on("error", reject);
  });
}

function decodeProof(proof) {
  const trimmed = proof.replace(/~S$/, "").replace(/^gAAAAA[BC]/, "");
  const raw = Buffer.from(trimmed, "base64").toString("utf8");
  const items = JSON.parse(raw);
  const cfg = {
    scriptSrc: items[5] || defaultSDKURL,
    userAgent: items[4] || "",
    language: items[7] || "en-US",
    languages: String(items[8] || "en-US").split(",").filter(Boolean),
    navigatorProbe: String(items[10] || ""),
    documentKey: items[11] || "",
    windowKey: items[12] || "",
    perfNow: Number(items[13] || 0),
    timeOrigin: Number(items[17] || 0),
    hardwareConcurrency: Number(items[16] || 8) || 8,
  };
  const screenValue = items[0];
  if (typeof screenValue === "string") {
    const [width, height] = screenValue.split("x");
    cfg.screenWidth = Number(width) || 1512;
    cfg.screenHeight = Number(height) || 928;
  } else {
    cfg.screenWidth = 1512;
    cfg.screenHeight = 928;
  }
  if (!cfg.languages.length) {
    cfg.languages = [cfg.language];
  }
  const [probeKey, probeValue] = cfg.navigatorProbe.split("−");
  cfg.navigatorProbeKey = probeKey || "";
  cfg.navigatorProbeValue = probeValue;
  return cfg;
}

function parseNavigatorProbeValue(value) {
  if (value == null) {
    return undefined;
  }
  if (value === "true") {
    return true;
  }
  if (value === "false") {
    return false;
  }
  if (/^\d+$/.test(value)) {
    return Number(value);
  }
  if (/^\d+\.\d+$/.test(value)) {
    return Number(value);
  }
  if (value.startsWith("[object ") && value.endsWith("]")) {
    const tag = value.slice(8, -1);
    return {
      toString() {
        return `[object ${tag}]`;
      },
    };
  }
  return value;
}

function patchSDK(source) {
  source = source.replaceAll("(((.+)+)+)+$", "^$");

  if (source.includes("__sentinel_debug")) {
    return source;
  }

  const minifiedMarker = "    (t.token = ye),\n    t";
  if (source.includes(minifiedMarker)) {
    return source.replace(
      minifiedMarker,
      "    (window.__sentinel_debug = { Nt, jt, Et, token: ye }),\n    (t.token = ye),\n    t",
    );
  }

  const deobMarker = "  }(), t.init = mn, t.token = wn, t;";
  if (source.includes(deobMarker)) {
    return source.replace(
      deobMarker,
      "  }(), window.__sentinel_debug = { Et, jt, token: wn, solver: R }, t.init = mn, t.token = wn, t;",
    );
  }

  throw new Error("inject debug hooks: marker not found");
}

function buildInitConfig(proof) {
  const cfg = decodeProof(proof);
  return {
    scriptURL: cfg.scriptSrc || defaultSDKURL,
    userAgent: cfg.userAgent,
    language: cfg.language,
    languages: cfg.languages,
    screen: {
      width: cfg.screenWidth,
      height: cfg.screenHeight,
      availWidth: cfg.screenWidth,
      availHeight: Math.max(cfg.screenHeight - 30, 0),
      availLeft: 0,
      availTop: 0,
      colorDepth: 24,
      pixelDepth: 24,
    },
    perfNow: cfg.perfNow || 24594.19999998808,
    timeOrigin: cfg.timeOrigin || Date.now() - 24594.19999998808,
    hardwareConcurrency: cfg.hardwareConcurrency,
    navigatorProbeKey: cfg.navigatorProbeKey,
    navigatorProbeValue: parseNavigatorProbeValue(cfg.navigatorProbeValue),
    documentKey: cfg.documentKey,
    windowKey: cfg.windowKey,
    localStorageKeys,
  };
}

function makeBase64() {
  return {
    atob(value) {
      return Buffer.from(String(value), "base64").toString("binary");
    },
    btoa(value) {
      return Buffer.from(String(value), "binary").toString("base64");
    },
  };
}

function makeFakeDate(nowValue) {
  function FakeDate(...args) {
    if (new.target) {
      if (args.length === 0) {
        return new Date(nowValue);
      }
      return new Date(...args);
    }
    if (args.length === 0) {
      return new Date(nowValue).toString();
    }
    return Date(...args);
  }

  FakeDate.now = () => nowValue;
  FakeDate.parse = Date.parse;
  FakeDate.UTC = Date.UTC;
  FakeDate.prototype = Date.prototype;
  Object.setPrototypeOf(FakeDate, Date);
  return FakeDate;
}

function makeStorage(keys) {
  const data = {};
  for (const key of keys) {
    data[key] = "";
  }

  const proto = {
    getItem(key) {
      key = String(key);
      return Object.prototype.hasOwnProperty.call(this, key) ? this[key] : null;
    },
    setItem(key, value) {
      this[String(key)] = String(value);
    },
    removeItem(key) {
      delete this[String(key)];
    },
    clear() {
      for (const key of Object.keys(this)) {
        delete this[key];
      }
    },
    toString() {
      return "[object Storage]";
    },
  };

  return Object.assign(Object.create(proto), data);
}

function makeStorageManager() {
  return {
    estimate() {
      return {
        quota: 296630877388,
        usage: 913230,
        usageDetails: {
          indexedDB: 913230,
        },
      };
    },
    toString() {
      return "[object StorageManager]";
    },
  };
}

function makeCanvas() {
  return {
    style: {},
    getContext(kind) {
      if (kind !== "webgl" && kind !== "experimental-webgl") {
        return null;
      }
      return {
        getExtension(name) {
          if (name === "WEBGL_debug_renderer_info") {
            return {};
          }
          return null;
        },
        getParameter(value) {
          if (value === 37445) {
            return "Intel";
          }
          if (value === 37446) {
            return "Mesa Intel(R) UHD Graphics 630 (CFL GT2)";
          }
          return null;
        },
        toString() {
          return "[object WebGLRenderingContext]";
        },
      };
    },
    toString() {
      return "[object HTMLCanvasElement]";
    },
  };
}

function makeDiv() {
  return {
    style: {},
    getBoundingClientRect() {
      return {
        x: 0,
        y: 744.6875,
        width: 19.640625,
        height: 14,
        top: 744.6875,
        right: 19.640625,
        bottom: 758.6875,
        left: 0,
      };
    },
    toString() {
      return "[object HTMLDivElement]";
    },
  };
}

function makeIframe() {
  return {
    style: {},
    src: "",
    contentWindow: {
      postMessage() {},
    },
    _appended: false,
    _loadListener: null,
    addEventListener(type, listener) {
      if (type === "load") {
        this._loadListener = listener;
        if (this._appended && typeof listener === "function") {
          setTimeout(() => listener(), 0);
        }
      }
    },
    removeEventListener(type, listener) {
      if (type === "load" && this._loadListener === listener) {
        this._loadListener = null;
      }
    },
    toString() {
      return "[object HTMLIFrameElement]";
    },
  };
}

function createContext(cfg) {
  const base64 = makeBase64();
  const location = new URL(authURL);
  const currentScript = { src: cfg.scriptURL };
  const localStorage = makeStorage(cfg.localStorageKeys);
  const storageManager = makeStorageManager();
  const navigatorProto = {
    userAgent: cfg.userAgent,
    language: cfg.language,
    languages: cfg.languages,
    hardwareConcurrency: cfg.hardwareConcurrency,
    vendor: "Google Inc.",
    platform: "MacIntel",
    deviceMemory: 8,
    maxTouchPoints: 0,
    storage: storageManager,
  };
  if (cfg.navigatorProbeKey && !(cfg.navigatorProbeKey in navigatorProto)) {
    navigatorProto[cfg.navigatorProbeKey] = cfg.navigatorProbeValue;
  }
  const navigator = Object.create(navigatorProto);

  const documentElementAttrs = new Map([["data-build", "20260219f9f6"]]);
  const documentElement = {
    getAttribute(name) {
      return documentElementAttrs.has(name) ? documentElementAttrs.get(name) : null;
    },
    setAttribute(name, value) {
      documentElementAttrs.set(String(name), String(value));
    },
    toString() {
      return "[object HTMLHtmlElement]";
    },
  };

  const bodyChildren = [];
  const body = {
    appendChild(node) {
      bodyChildren.push(node);
      if (node && typeof node === "object") {
        node._appended = true;
        if (typeof node._loadListener === "function") {
          setTimeout(() => node._loadListener(), 0);
        }
      }
      return node;
    },
    removeChild(node) {
      const index = bodyChildren.indexOf(node);
      if (index >= 0) {
        bodyChildren.splice(index, 1);
      }
      return node;
    },
    toString() {
      return "[object HTMLBodyElement]";
    },
  };

  const document = {
    currentScript,
    scripts: [currentScript],
    documentElement,
    body,
    location,
    URL: authURL,
    documentURI: authURL,
    compatMode: "CSS1Compat",
    implementation: {
      toString() {
        return "[object DOMImplementation]";
      },
    },
    cookie: "",
    addEventListener() {},
    removeEventListener() {},
    createElement(name) {
      const lower = String(name).toLowerCase();
      if (lower === "div") {
        return makeDiv();
      }
      if (lower === "canvas") {
        return makeCanvas();
      }
      if (lower === "iframe") {
        return makeIframe();
      }
      return {
        style: {},
        toString() {
          return "[object HTMLElement]";
        },
      };
    },
    toString() {
      return "[object HTMLDocument]";
    },
  };
  if (cfg.documentKey) {
    document[cfg.documentKey] = true;
  }

  const performance = {
    now() {
      return cfg.perfNow;
    },
    timeOrigin: cfg.timeOrigin,
    memory: {
      jsHeapSizeLimit: 4294967296,
    },
    toString() {
      return "[object Performance]";
    },
  };

  const history = { length: 17 };
  const screen = {
    width: cfg.screen.width,
    height: cfg.screen.height,
    availWidth: cfg.screen.availWidth,
    availHeight: cfg.screen.availHeight,
    availLeft: cfg.screen.availLeft,
    availTop: cfg.screen.availTop,
    colorDepth: cfg.screen.colorDepth,
    pixelDepth: cfg.screen.pixelDepth,
  };

  const mathObject = Object.create(Math);
  const fakeDate = makeFakeDate(cfg.timeOrigin + cfg.perfNow);
  const listeners = new Map();

  const window = {
    document,
    location,
    navigator,
    history,
    localStorage,
    performance,
    screen,
    crypto: webcrypto,
    TextEncoder,
    URL,
    URLSearchParams,
    Math: mathObject,
    Date: fakeDate,
    Object,
    Array,
    Number,
    String,
    Boolean,
    JSON,
    Promise,
    Reflect,
    RegExp,
    Error,
    TypeError,
    Map,
    Set,
    WeakMap,
    WeakSet,
    atob: base64.atob,
    btoa: base64.btoa,
    setTimeout,
    clearTimeout,
    requestIdleCallback(callback) {
      return setTimeout(() => callback({
        timeRemaining: () => 1,
        didTimeout: false,
      }), 0);
    },
    cancelIdleCallback(id) {
      clearTimeout(id);
    },
    structuredClone(value) {
      return global.structuredClone ? global.structuredClone(value) : JSON.parse(JSON.stringify(value));
    },
    addEventListener(type, listener) {
      if (!listeners.has(type)) {
        listeners.set(type, []);
      }
      listeners.get(type).push(listener);
    },
    removeEventListener(type, listener) {
      if (!listeners.has(type)) {
        return;
      }
      const next = listeners.get(type).filter((item) => item !== listener);
      listeners.set(type, next);
    },
    dispatchEvent(event) {
      const items = listeners.get(event.type) || [];
      for (const listener of items) {
        listener.call(window, event);
      }
    },
    fetch: async () => {
      throw new Error("unexpected fetch");
    },
    console,
    __reactRouterContext: { state: { loaderData: {} } },
  };
  if (cfg.windowKey) {
    window[cfg.windowKey] = null;
  }

  window.window = window;
  window.self = window;
  window.top = window;
  window.globalThis = window;
  document.defaultView = window;

  const context = vm.createContext(window);
  return { context, window };
}

function resolveSDKPath() {
  const candidates = [
    process.env.SENTINEL_SDK_PATH,
    path.join(__dirname, "sdk.js"),
    path.join(__dirname, "js", "sentinel_sdk.js"),
  ].filter(Boolean);

  for (const candidate of candidates) {
    if (fs.existsSync(candidate)) {
      return candidate;
    }
  }

  return candidates[0];
}

function readVMTimeout() {
  const value = Number(process.env.SENTINEL_VM_TIMEOUT_MS || "5000");
  if (!Number.isFinite(value) || value <= 0) {
    return 5000;
  }
  return Math.trunc(value);
}

async function main() {
  const input = JSON.parse(await readStdin());
  const sdkPath = resolveSDKPath();
  const sdkSource = patchSDK(fs.readFileSync(sdkPath, "utf8"));
  const initConfig = buildInitConfig(input.proof);
  const { context, window } = createContext(initConfig);

  vm.runInContext(sdkSource, context, {
    filename: sdkPath,
    timeout: readVMTimeout(),
  });

  if (!window.__sentinel_debug || typeof window.__sentinel_debug.jt !== "function") {
    throw new Error("sentinel debug hooks are unavailable");
  }

  window.document.documentElement.setAttribute("data-build", "20260219f9f6");
  window.__sentinel_debug.Et(input.proof);
  const solved = await window.__sentinel_debug.jt(input.dx);
  process.stdout.write(String(solved));
}

main().catch((error) => {
  process.stderr.write(`${error.stack || error.message}\n`);
  process.exitCode = 1;
});
