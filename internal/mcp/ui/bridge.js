// The host-to-view bridge for Litescope's MCP App views.
//
// A view is an HTML document the host renders in a sandboxed iframe. It talks
// to the host with JSON-RPC over postMessage: it announces itself with
// ui/initialize, the host answers with theme and layout context, and the tool's
// arguments and result arrive as notifications. No SDK, no bundler, no network
// — the whole protocol a view needs is the few dozen lines below.
(function () {
  "use strict";

  var nextId = 1;
  var pending = {};
  var handlers = {};
  var target = window.parent;

  function post(msg) {
    // The host's sandbox frame is a different origin by design; the sandbox,
    // not the target check, is what isolates us.
    target.postMessage(msg, "*");
  }

  function request(method, params) {
    var id = nextId++;
    post({ jsonrpc: "2.0", id: id, method: method, params: params || {} });
    return new Promise(function (resolve, reject) {
      pending[id] = { resolve: resolve, reject: reject };
    });
  }

  function notify(method, params) {
    post({ jsonrpc: "2.0", method: method, params: params || {} });
  }

  function respond(id, result) {
    post({ jsonrpc: "2.0", id: id, result: result || {} });
  }

  window.addEventListener("message", function (event) {
    var msg = event.data;
    if (!msg || msg.jsonrpc !== "2.0") return;

    if (msg.id !== undefined && msg.method === undefined) {
      var p = pending[msg.id];
      if (!p) return;
      delete pending[msg.id];
      if (msg.error) p.reject(new Error(msg.error.message || "request failed"));
      else p.resolve(msg.result);
      return;
    }
    if (!msg.method) return;

    // ui/resource-teardown is a request: the host waits for an answer before
    // discarding the view.
    if (msg.method === "ui/resource-teardown") {
      if (msg.id !== undefined) respond(msg.id, {});
      return;
    }
    var fn = handlers[msg.method];
    if (fn) fn(msg.params || {});
  });

  function on(method, fn) {
    handlers[method] = fn;
  }

  // ── theming ───────────────────────────────────────────────────────────────

  // The host hands us its own palette. Adopting it makes the view look native
  // in whichever client renders it, and our own tokens stay as the fallback.
  function applyHostContext(ctx) {
    if (!ctx) return;
    var root = document.documentElement;
    root.setAttribute("data-theme", ctx.theme === "dark" ? "dark" : "light");
    var styles = ctx.styles || {};
    var vars = styles.variables || {};
    Object.keys(vars).forEach(function (k) {
      if (typeof vars[k] === "string") root.style.setProperty(k, vars[k]);
    });
    if (styles.css && styles.css.fonts) {
      var el = document.createElement("style");
      el.textContent = styles.css.fonts;
      document.head.appendChild(el);
    }
  }

  // ── size reporting ────────────────────────────────────────────────────────

  var lastH = 0;
  function reportSize() {
    var h = Math.ceil(document.documentElement.scrollHeight);
    if (!h || h === lastH) return;
    lastH = h;
    notify("ui/notifications/size-changed", { width: window.innerWidth, height: h });
  }
  var sizeTimer = null;
  function scheduleSize() {
    clearTimeout(sizeTimer);
    sizeTimer = setTimeout(reportSize, 60);
  }

  // ── public surface ────────────────────────────────────────────────────────

  var api = {
    host: {},
    // onResult registers the view's renderer. It fires for the result of the
    // tool call that opened the view and again after any refresh the view
    // triggers itself.
    onResult: function (fn) {
      function deliver(result) {
        try {
          fn(api.data(result), result);
        } catch (err) {
          api.fail(err);
        }
        scheduleSize();
      }
      on("ui/notifications/tool-result", deliver);
      api._deliver = deliver;
    },
    onInput: function (fn) {
      on("ui/notifications/tool-input", function (p) {
        api.args = (p && p.arguments) || {};
        fn(api.args);
      });
    },
    // data pulls the tool's JSON payload out of a CallToolResult: every
    // litescope tool emits structuredContent, and the text block is the
    // fallback for a host that strips it.
    data: function (result) {
      if (!result) return null;
      if (result.structuredContent) return result.structuredContent;
      var content = result.content || [];
      for (var i = 0; i < content.length; i++) {
        if (content[i].type === "text") {
          try {
            return JSON.parse(content[i].text);
          } catch (e) {
            /* not JSON: fall through */
          }
        }
      }
      return null;
    },
    // callTool re-invokes a tool on the same server through the host, so a
    // refresh button does not have to go back through the model.
    callTool: function (name, args) {
      return request("tools/call", { name: name, arguments: args || {} });
    },
    // refresh re-runs the tool that opened this view and re-renders.
    refresh: function (overrides) {
      if (!api.toolName) return Promise.resolve();
      var args = Object.assign({}, api.args || {}, overrides || {});
      return api.callTool(api.toolName, args).then(function (result) {
        api.args = args;
        if (api._deliver) api._deliver(result);
        return result;
      });
    },
    // say puts a message in the conversation, so a click in the view can hand
    // the thread back to the agent with the context the user just saw.
    say: function (text) {
      return request("ui/message", { role: "user", content: { type: "text", text: text } });
    },
    fail: function (err) {
      var el = document.getElementById("root") || document.body;
      el.innerHTML = '<div class="empty">' + escapeHTML(String((err && err.message) || err)) + "</div>";
    },
    escape: escapeHTML,
    resize: scheduleSize,
  };

  function escapeHTML(s) {
    return String(s === undefined || s === null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }

  window.litescope = api;

  // ── handshake ─────────────────────────────────────────────────────────────

  function start() {
    var params = {
      protocolVersion: "2026-01-26",
      clientInfo: { name: "litescope-view", version: "1" },
      capabilities: {},
      appCapabilities: { availableDisplayModes: ["inline", "fullscreen"] },
    };
    var settled = false;
    function ready(result) {
      if (settled) return;
      settled = true;
      result = result || {};
      api.host = result.hostContext || {};
      api.toolName = (api.host.toolInfo && api.host.toolInfo.tool && api.host.toolInfo.tool.name) || api.toolName;
      applyHostContext(api.host);
      notify("ui/notifications/initialized", {});
      scheduleSize();
    }
    request("ui/initialize", params).then(ready, function () {
      ready(null);
    });
    // Some hosts still answer the pre-extension "initialize" name; ask once
    // more rather than rendering an unthemed view.
    setTimeout(function () {
      if (!settled) request("initialize", params).then(ready, function () { ready(null); });
    }, 400);
    setTimeout(function () { ready(null); }, 1500);
  }

  if (window.ResizeObserver) new ResizeObserver(scheduleSize).observe(document.documentElement);
  window.addEventListener("load", scheduleSize);
  start();
})();
