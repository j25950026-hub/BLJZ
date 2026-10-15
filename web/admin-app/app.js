(function () {
  var root = document.getElementById("app");
  if (!root) {
    return;
  }

  var groupState = {
    filters: { q: "", tag: "", type: "", status: "" },
    groups: [],
    views: [],
    total: 0,
    selected: {},
    preview: null,
    receipt: null,
    dirty: false
  };

  var switchState = {
    totalGroups: 0,
    switches: [],
    groups: [],
    feature: "",
    detail: null,
    detailRequest: 0,
    detailFilters: { q: "", status: "all", page: 1, pageSize: 20 },
    selected: {},
    scope: "all",
    preview: null,
    receipt: null,
    dirty: false
  };

  var broadcastState = {
    groups: [],
    selected: {},
    draft: { sourceChatId: "", mediaType: "text", mediaFileId: "", identityName: "", content: "" },
    preview: null,
    receipt: null,
    dirty: false
  };

  var scheduleState = {
    groups: [],
    schedules: [],
    preview: null,
    receipt: null,
    editing: null,
    dirty: false
  };

  var templateState = {
    groups: [],
    chatId: "",
    data: null,
    welcomeDraft: { enabled: false, template: "", sample: { userId: 10001, firstName: "新成员", username: "" } },
    welcomeRender: null,
    welcomePreview: null,
    autoEditing: null,
    autoPreview: null,
    autoReceipt: null,
    closingSelected: {},
    closingPreview: null,
    closingReceipt: null,
    dirty: false
  };

  var closeoutState = {
    snapshots: [],
    library: [],
    risk: [],
    insights: { stats: {}, recommendations: [] },
    retries: [],
    selectedRetries: {},
    retryPreview: null,
    retryReceipt: null,
    libraryPreview: null,
    dirty: false
  };

  var securityState = {
    csrfToken: "",
    sessions: [],
    reauthScopes: {},
    qrLoaded: false,
    message: "",
    binding: { active: false, bindingId: "", expiresAt: "", imageURL: "", pending: [], timer: null }
  };

  var runtimeState = {
    data: null,
    message: "",
    confirmation: "",
    busy: false,
    refreshTimer: null
  };

  var setupState = {
    csrfToken: "",
    started: false,
    message: ""
  };

  var permissionsState = {
    message: "",
    ownerDraft: { userId: "", username: "" },
    operatorDraft: { userId: "", username: "" }
  };

  var lastRoutePath = window.location.pathname + (window.location.search || "");

  function escapeHTML(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function formatNumber(value) {
    if (typeof value !== "number" || !isFinite(value)) {
      return "0";
    }
    return String(value);
  }

  function safeAdminHref(value) {
    var href = String(value == null ? "" : value).trim();
    if (href !== "/admin" && href.indexOf("/admin/") !== 0) {
      return "";
    }
    return href;
  }

  function currentPage() {
    if (window.location.pathname === "/admin/device-bind") {
      return "device-bind";
    }
    if (window.location.pathname === "/admin/setup") {
      return "setup";
    }
    if (window.location.pathname === "/admin/security") {
      return "security";
    }
    if (window.location.pathname === "/admin/groups") {
      return "groups";
    }
    if (window.location.pathname === "/admin/permissions") {
      return "permissions";
    }
    if (window.location.pathname === "/admin/switches") {
      return "switches";
    }
    if (window.location.pathname === "/admin/broadcast") {
      return "broadcast";
    }
    if (window.location.pathname === "/admin/schedules") {
      return "schedules";
    }
    if (window.location.pathname === "/admin/templates") {
      return "templates";
    }
    if (window.location.pathname === "/admin/snapshots") {
      return "snapshots";
    }
    if (window.location.pathname === "/admin/template-library") {
      return "template-library";
    }
    if (window.location.pathname === "/admin/risk-radar") {
      return "risk-radar";
    }
    if (window.location.pathname === "/admin/insights") {
      return "insights";
    }
    if (window.location.pathname === "/admin/retries") {
      return "retries";
    }
    return "console";
  }

  function metricCard(label, value, note) {
    return [
      '<article class="metric-card">',
      '<div class="metric-label">' + escapeHTML(label) + '</div>',
      '<div class="metric-value">' + escapeHTML(value) + '</div>',
      note ? '<div class="metric-note">' + escapeHTML(note) + '</div>' : '',
      '</article>'
    ].join("");
  }

  function badgeText(severity) {
    if (severity === "high") return "高";
    if (severity === "normal" || severity === "medium") return "中";
    if (severity === "info") return "低";
    return severity || "";
  }

  function badgeClass(severity) {
    if (severity === "high") return "badge badge-high";
    if (severity === "normal" || severity === "medium") return "badge badge-medium";
    return "badge badge-low";
  }

  function statusClass(status) {
    if (status === "healthy") return "badge badge-low";
    if (status === "risk") return "badge badge-high";
    return "badge badge-medium";
  }

  function statusLabel(status) {
    if (status === "healthy") return "正常";
    if (status === "attention") return "关注";
    return "风险";
  }

  function switchLabel(value) {
    return value ? "开启" : "关闭";
  }

  function selectedChatIDs() {
    return Object.keys(groupState.selected)
      .filter(function (key) { return groupState.selected[key]; })
      .map(function (key) { return Number(key); })
      .filter(function (value) { return isFinite(value) && value !== 0; });
  }

  function setGroupDirty(value) {
    groupState.dirty = !!value;
    var banner = document.getElementById("unsaved-banner");
    if (banner) {
      banner.style.display = groupState.dirty ? "block" : "none";
    }
  }

  function setSwitchDirty(value) {
    switchState.dirty = !!value;
    var banner = document.getElementById("switch-unsaved-banner");
    if (banner) {
      banner.style.display = switchState.dirty ? "block" : "none";
    }
  }

  function setTemplateDirty(value) {
    templateState.dirty = !!value;
    var banner = document.getElementById("template-unsaved-banner");
    if (banner) {
      banner.style.display = templateState.dirty ? "block" : "none";
    }
  }

  function setCloseoutDirty(value) {
    closeoutState.dirty = !!value;
    var banner = document.getElementById("closeout-unsaved-banner");
    if (banner) {
      banner.style.display = closeoutState.dirty ? "block" : "none";
    }
  }

  function hasDirtyState() {
    return !!(groupState.dirty || switchState.dirty || broadcastState.dirty || scheduleState.dirty || templateState.dirty || closeoutState.dirty);
  }

  window.addEventListener("beforeunload", function (event) {
    if (!hasDirtyState()) {
      return;
    }
    event.preventDefault();
    event.returnValue = "有未保存的后台操作草稿，确认离开？";
    return event.returnValue;
  });

  function renderActionList(items, emptyText) {
    if (!items || items.length === 0) {
      return '<div class="state-empty">' + escapeHTML(emptyText || "暂无") + '</div>';
    }
    var html = ['<ul class="steps">'];
    for (var i = 0; i < items.length; i++) {
      var item = items[i];
      if (item.visibility && item.visibility !== "admin_only") {
        continue;
      }
      var href = safeAdminHref(item.actionUrl);
      html.push(
        '<li class="step-item">',
        '<div class="step-title-row">',
        '<span class="step-title">' + escapeHTML(item.title || "") + '</span>',
        item.severity ? '<span class="' + badgeClass(item.severity) + '">' + escapeHTML(badgeText(item.severity)) + '</span>' : '',
        '</div>',
        (item.description || item.reason) ? '<div class="step-desc">' + escapeHTML(item.description || item.reason) + '</div>' : '',
        href ? '<a class="step-link" href="' + escapeHTML(href) + '">进入处理</a>' : '',
        '</li>'
      );
    }
    html.push('</ul>');
    return html.join("");
  }

  function renderHealthRows(rows) {
    if (!rows || rows.length === 0) {
      return '<div class="state-empty">暂无群数据</div>';
    }
    var html = ['<div class="table-wrap"><table class="health-table"><thead><tr><th>群</th><th>健康分</th><th>状态</th><th>原因</th><th>下一步</th></tr></thead><tbody>'];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      html.push('<tr>');
      html.push('<td><div class="group-title">' + escapeHTML(row.title || row.chatId) + '</div><div class="group-sub">' + escapeHTML(row.chatId) + '</div></td>');
      html.push('<td><strong>' + escapeHTML(formatNumber(row.score)) + '</strong></td>');
      html.push('<td><span class="' + statusClass(row.status) + '">' + escapeHTML(statusLabel(row.status)) + '</span></td>');
      html.push('<td>' + renderReasonList(row.reasons) + '</td>');
      html.push('<td>' + renderActionMiniList(row.nextSteps) + '</td>');
      html.push('</tr>');
    }
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderReasonList(reasons) {
    if (!reasons || reasons.length === 0) {
      return '<div class="state-empty">无</div>';
    }
    var html = ['<ul class="reason-list">'];
    for (var i = 0; i < reasons.length; i++) {
      html.push('<li>' + escapeHTML(reasons[i]) + '</li>');
    }
    html.push('</ul>');
    return html.join("");
  }

  function renderActionMiniList(items) {
    if (!items || items.length === 0) {
      return '<div class="state-empty">无</div>';
    }
    var html = ['<div class="mini-steps">'];
    for (var i = 0; i < items.length; i++) {
      var item = items[i];
      if (item.visibility && item.visibility !== "admin_only") {
        continue;
      }
      var href = safeAdminHref(item.actionUrl) || "/admin";
      html.push(
        '<a class="mini-step" href="' + escapeHTML(href) + '">',
        escapeHTML(item.title || ""),
        '</a>'
      );
    }
    html.push('</div>');
    return html.join("");
  }

  function navLink(path, label, page, activePage) {
    var active = page === activePage;
    return '<a class="nav-link' + (active ? ' nav-link-active' : '') + '" data-route="' + escapeHTML(page) + '" href="' + escapeHTML(path) + '">' + escapeHTML(label) + '</a>';
  }

  function bindShellEvents() {
    var logout = document.getElementById("logout-btn");
    if (logout) {
      logout.addEventListener("click", async function () {
        await fetch("/admin/api/logout", { method: "POST", credentials: "same-origin" });
        window.location.href = "/admin";
      });
    }
    var links = root.querySelectorAll("a[data-route]");
    for (var i = 0; i < links.length; i++) {
      links[i].addEventListener("click", function (event) {
        event.preventDefault();
        var path = this.getAttribute("href") || "/admin";
        history.pushState({}, "", path);
        route();
      });
    }
  }

  function renderShell(meta, innerHtml) {
    meta = meta || {};
    var activePage = meta.activePage || currentPage();
    root.innerHTML = [
      '<main class="admin-shell">',
      '<aside class="admin-nav">',
      '<div class="brand">运营后台</div>',
      navLink("/admin", "运营总控台", "console", activePage),
      navLink("/admin/security", "安全设置 / 设备会话", "security", activePage),
      navLink("/admin/groups", "群组工作台", "groups", activePage),
      navLink("/admin/permissions", "人员与权限中心", "permissions", activePage),
      navLink("/admin/switches", "全局功能开关中心", "switches", activePage),
      navLink("/admin/broadcast", "群发面板", "broadcast", activePage),
      navLink("/admin/schedules", "定时群发", "schedules", activePage),
      navLink("/admin/templates", "自动化管理", "templates", activePage),
      navLink("/admin/snapshots", "变更快照", "snapshots", activePage),
      navLink("/admin/template-library", "模板库", "template-library", activePage),
      navLink("/admin/risk-radar", "权限风险雷达", "risk-radar", activePage),
      navLink("/admin/insights", "运营洞察", "insights", activePage),
      navLink("/admin/retries", "失败重试中心", "retries", activePage),
      '<a class="nav-link" href="/admin/kill-switch">紧急停机</a>',
      '</aside>',
      '<section class="admin-main">',
      '<header class="page-head">',
      '<div><div class="page-kicker">' + escapeHTML(meta.kicker || "") + '</div><h1>' + escapeHTML(meta.title || "运营后台") + '</h1></div>',
      '<button type="button" id="logout-btn">退出</button>',
      '</header>',
      innerHtml,
      '</section>',
      '</main>'
    ].join("");
    bindShellEvents();
  }

  function renderLoading(title, activePage) {
    renderShell({ title: title, kicker: "正在加载", activePage: activePage }, [
      '<section class="console-grid">',
      '<div class="console-surface console-loading">正在加载' + escapeHTML(title) + '...</div>',
      '</section>'
    ].join(""));
  }

  function renderError(title, activePage, message) {
    renderShell({ title: title, kicker: "加载失败", activePage: activePage }, [
      '<section class="console-grid">',
      '<div class="console-surface state-error">' + escapeHTML(message || "加载失败") + '</div>',
      '</section>'
    ].join(""));
  }

  async function fetchAdminJSON(url, options) {
    var resp = await fetch(url, options || {
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    });
    var payload = null;
    try {
      payload = await resp.json();
    } catch (err) {
      payload = null;
    }
    if (resp.status === 401) {
      window.location.href = "/admin";
      return null;
    }
    if (!resp.ok || !payload || !payload.ok) {
      var message = (payload && payload.error) || ("请求失败，HTTP " + resp.status);
      throw new Error(message);
    }
    return payload.data || {};
  }

  async function getAdminCSRFToken() {
    if (securityState.csrfToken) {
      return securityState.csrfToken;
    }
    var resp = await fetch("/admin/api/csrf", { credentials: "same-origin", headers: { Accept: "application/json" } });
    var payload = null;
    try { payload = await resp.json(); } catch (err) { payload = null; }
    if (!resp.ok || !payload || !payload.ok || !payload.data || !payload.data.token) {
      throw new Error((payload && payload.error) || "获取安全令牌失败");
    }
    securityState.csrfToken = String(payload.data.token);
    return securityState.csrfToken;
  }

  async function postAdminJSON(url, body) {
    var csrfToken = await getAdminCSRFToken();
    return fetchAdminJSON(url, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
        "X-CSRF-Token": csrfToken
      },
      body: JSON.stringify(body || {})
    });
  }

  function formatRuntimeTime(value, fallback) {
    if (!value) return fallback || "未记录";
    var parsed = new Date(value);
    return isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
  }

  function runtimePollLabel(poll) {
    var readiness = (poll && poll.readiness) || {};
    if (readiness.ready) return readiness.reason === "starting" ? "启动中" : "正常";
    if (readiness.reason === "missing") return "未获得状态";
    if (readiness.reason === "invalid") return "状态无效";
    return "异常";
  }

  function runtimeGuardianLabel(guardian) {
    var phase = guardian && guardian.phase;
    if (phase === "recovered") return "已恢复";
    if (phase === "rolled_back") return "已回滚";
    if (phase === "restarting") return "恢复中";
    if (phase === "frozen") return "已冻结";
    return "待命";
  }

  function runtimeBadgeClass(ready, phase) {
    if (ready && phase !== "frozen") return "badge badge-low";
    if (phase === "frozen") return "badge badge-high";
    return "badge badge-medium";
  }

  function renderRuntimePanel(data) {
    data = data || {};
    var poll = data.poll || {};
    var guardian = data.guardian || {};
    var readiness = poll.readiness || {};
    var degraded = readiness.ready === false;
    var canRollback = degraded && poll.last_error_class === "release_regression";
    var confirmation = runtimeState.confirmation;
    var actionLabel = confirmation === "rollback" ? "试回滚至已验证版本" : "受控恢复";
    var controls = "";

    if (degraded && !confirmation) {
      controls = [
        '<div class="runtime-actions">',
        '<button type="button" id="runtime-recover"' + (runtimeState.busy ? ' disabled' : '') + '>受控恢复</button>',
        canRollback ? '<button type="button" id="runtime-rollback" class="danger-btn"' + (runtimeState.busy ? ' disabled' : '') + '>试回滚至已验证版本</button>' : '',
        '</div>'
      ].join("");
    }
    if (confirmation) {
      controls = [
        '<form id="runtime-confirmation" class="runtime-confirmation">',
        '<div><strong>' + escapeHTML(actionLabel) + '</strong><p>请确认当前 Google Authenticator 验证码。</p></div>',
        '<label>验证码<input name="totp" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required></label>',
        '<div class="runtime-actions">',
        '<button type="submit"' + (runtimeState.busy ? ' disabled' : '') + '>确认</button>',
        '<button type="button" id="runtime-cancel" class="ghost-btn"' + (runtimeState.busy ? ' disabled' : '') + '>取消</button>',
        '</div>',
        '</form>'
      ].join("");
    }

    return [
      '<section id="runtime-panel" class="console-surface panel runtime-panel">',
      '<div class="panel-head"><h2>Telegram 输入状态</h2><span class="' + runtimeBadgeClass(readiness.ready, guardian.phase) + '">' + escapeHTML(runtimePollLabel(poll)) + '</span></div>',
      '<div class="runtime-status-grid">',
      '<article class="runtime-status-card"><span>最近成功轮询</span><strong>' + escapeHTML(formatRuntimeTime(poll.last_successful_poll_at, "未记录")) + '</strong></article>',
      '<article class="runtime-status-card"><span>连续失败</span><strong>' + escapeHTML(formatNumber(poll.consecutive_failures || 0)) + '</strong><small>' + escapeHTML(poll.last_error_class || "none") + '</small></article>',
      '<article class="runtime-status-card"><span>Guardian</span><strong>' + escapeHTML(runtimeGuardianLabel(guardian)) + '</strong><small>' + escapeHTML(guardian.last_action || "无动作") + '</small></article>',
      '<article class="runtime-status-card"><span>当前版本</span><strong>' + escapeHTML(poll.release_id || "未标记") + '</strong><small>' + escapeHTML(poll.release_sha || "") + '</small></article>',
      '</div>',
      runtimeState.message ? '<div class="state-message runtime-message" role="status">' + escapeHTML(runtimeState.message) + '</div>' : '',
      controls,
      '</section>'
    ].join("");
  }

  function bindRuntimeEvents() {
    var recover = document.getElementById("runtime-recover");
    if (recover) recover.addEventListener("click", function () { runtimeState.confirmation = "recover"; renderRuntimePanelInPlace(); });
    var rollback = document.getElementById("runtime-rollback");
    if (rollback) rollback.addEventListener("click", function () { runtimeState.confirmation = "rollback"; renderRuntimePanelInPlace(); });
    var cancel = document.getElementById("runtime-cancel");
    if (cancel) cancel.addEventListener("click", function () { runtimeState.confirmation = ""; runtimeState.message = ""; renderRuntimePanelInPlace(); });
    var form = document.getElementById("runtime-confirmation");
    if (form) form.addEventListener("submit", submitRuntimeRecovery);
  }

  function renderRuntimePanelInPlace() {
    var node = document.getElementById("runtime-panel");
    if (!node) return;
    node.outerHTML = renderRuntimePanel(runtimeState.data || {});
    bindRuntimeEvents();
  }

  async function submitRuntimeRecovery(event) {
    event.preventDefault();
    if (runtimeState.busy || !runtimeState.confirmation) return;
    var form = event.currentTarget;
    var totp = String((form.elements.totp && form.elements.totp.value) || "").trim();
    if (!totp) return;
    var action = runtimeState.confirmation;
    runtimeState.busy = true;
    runtimeState.message = "正在验证并提交请求...";
    renderRuntimePanelInPlace();
    try {
      await postAdminJSON("/admin/api/security/reauth", { action: "recovery", totp: totp });
      await postAdminJSON("/admin/api/recovery/request", { action: action });
      runtimeState.confirmation = "";
      runtimeState.message = action === "rollback" ? "已提交已验证版本的试回滚请求。" : "已提交受控恢复请求。";
    } catch (err) {
      runtimeState.message = err && err.message ? err.message : "恢复请求失败";
    } finally {
      runtimeState.busy = false;
      await refreshRuntimePanel(true);
    }
  }

  async function refreshRuntimePanel(preserveMessage) {
    if (runtimeState.busy || runtimeState.confirmation) return;
    try {
      runtimeState.data = await fetchAdminJSON("/admin/api/runtime-status");
      if (!preserveMessage) runtimeState.message = "";
    } catch (err) {
      runtimeState.data = null;
      runtimeState.message = err && err.message ? err.message : "运行状态暂不可用";
    }
    if (currentPage() === "console") renderRuntimePanelInPlace();
  }

  function stopRuntimeRefresh() {
    if (runtimeState.refreshTimer) window.clearInterval(runtimeState.refreshTimer);
    runtimeState.refreshTimer = null;
  }

  function startRuntimeRefresh() {
    stopRuntimeRefresh();
    runtimeState.refreshTimer = window.setInterval(refreshRuntimePanel, 30000);
  }

  function renderConsole(data) {
    data = data || {};
    var summary = data.summary || {};
    var todos = data.todos || [];
    var health = data.health || [];
    var nextSteps = data.nextSteps || [];

    var body = [
      '<section class="console-grid">',
      renderRuntimePanel(runtimeState.data || {}),
      '<section class="metric-strip">',
      metricCard("群总数", formatNumber(summary.totalGroups), "后台群配置总量"),
      metricCard("健康群", formatNumber(summary.healthyGroups), "配置完整"),
      metricCard("待办", formatNumber(todos.length), "需要处理"),
      metricCard("平均健康分", formatNumber(summary.averageHealthScore), "可解释评分"),
      metricCard("定时任务", formatNumber(summary.pendingSchedules), "待发送"),
      '</section>',
      '<section class="panel-row">',
      '<article class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>待办中心</h2><span class="panel-meta">' + escapeHTML(formatNumber(todos.length)) + '</span></div>',
      renderActionList(todos, "暂无待处理事项"),
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>下一步建议</h2><span class="panel-meta">' + escapeHTML(formatNumber(nextSteps.length)) + '</span></div>',
      renderActionList(nextSteps, "暂无建议"),
      '</article>',
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>群健康状态</h2><span class="panel-meta">' + escapeHTML(formatNumber(health.length)) + '</span></div>',
      renderHealthRows(health),
      '</section>',
      '</section>'
    ].join("");

    renderShell({ title: "运营总控台", kicker: "运营总控台", activePage: "console" }, body);
    bindRuntimeEvents();
  }

  async function loadConsole() {
    renderLoading("运营总控台", "console");
    try {
      var results = await Promise.all([
        fetchAdminJSON("/admin/api/console"),
        fetchAdminJSON("/admin/api/runtime-status").catch(function (err) {
          runtimeState.message = err && err.message ? err.message : "运行状态暂不可用";
          return null;
        })
      ]);
      var data = results[0];
      runtimeState.data = results[1];
      renderConsole(data || {});
      startRuntimeRefresh();
    } catch (err) {
      renderError("运营总控台", "console", err && err.message ? err.message : "加载失败");
    }
  }

  function permissionDraftFor(target) {
    if (target === "owner") {
      return permissionsState.ownerDraft;
    }
    return permissionsState.operatorDraft;
  }

  function setPermissionMessage(message) {
    permissionsState.message = String(message || "");
    var node = document.getElementById("permissions-message");
    if (node) {
      node.textContent = permissionsState.message;
    }
  }

  function setPermissionDraft(target, userId, username) {
    var draft = permissionDraftFor(target);
    draft.userId = userId ? String(userId) : "";
    draft.username = username ? String(username) : "";
  }

  function syncPermissionForm(target) {
    var form = root.querySelector('.permissions-write-form[data-permission-target="' + target + '"]');
    var draft = permissionDraftFor(target);
    if (!form) {
      return;
    }
    var userInput = form.querySelector('input[name="userId"]');
    var usernameInput = form.querySelector('input[name="username"]');
    if (userInput) {
      userInput.value = draft.userId || "";
    }
    if (usernameInput) {
      usernameInput.value = draft.username || "";
    }
  }

  function clearPermissionDraft(target) {
    setPermissionDraft(target, "", "");
    syncPermissionForm(target);
  }

  function renderPermissionForm(target, action, buttonText) {
    var draft = permissionDraftFor(target);
    return [
      '<form class="filter-grid permissions-write-form" data-permission-target="' + escapeHTML(target) + '" data-permission-action="' + escapeHTML(action) + '">',
      '<label>用户ID<input name="userId" inputmode="numeric" value="' + escapeHTML(draft.userId || "") + '" required></label>',
      '<label>用户名<input name="username" value="' + escapeHTML(draft.username || "") + '" placeholder="可选"></label>',
      '<div class="filter-actions">',
      '<button type="submit">' + escapeHTML(buttonText) + '</button>',
      '<button type="button" class="ghost-btn permission-clear-btn" data-permission-target="' + escapeHTML(target) + '">清空</button>',
      '</div>',
      '</form>'
    ].join("");
  }

  function renderPermissionRows(rows, emptyText, options) {
    options = options || {};
    var editable = !!options.editable;
    if (!rows || rows.length === 0) {
      return '<div class="state-empty">' + escapeHTML(emptyText || "暂无权限人员") + '</div>';
    }
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th>用户</th><th>角色</th><th>范围</th><th>群</th>' + (editable ? '<th>操作</th>' : '') + '</tr></thead><tbody>'];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      html.push('<tr>');
      html.push('<td><div class="group-title">' + escapeHTML(row.username || "-") + '</div><div class="group-sub">' + escapeHTML(row.userId || "") + '</div></td>');
      html.push('<td>' + escapeHTML(permissionRoleLabel(row.role)) + '</td>');
      html.push('<td>' + escapeHTML(permissionScopeLabel(row.scope)) + '</td>');
      html.push('<td>' + escapeHTML(row.group || (row.chatId ? row.chatId : "全部群")) + '</td>');
      if (editable) {
        html.push('<td><div class="template-action-row"><button type="button" class="ghost-btn permission-edit-btn" data-permission-target="' + escapeHTML(options.target || "") + '" data-user-id="' + escapeHTML(row.userId || "") + '" data-username="' + escapeHTML(row.username || "") + '">编辑</button><button type="button" class="ghost-btn permission-remove-btn" data-permission-action="' + escapeHTML(options.removeAction || "") + '" data-user-id="' + escapeHTML(row.userId || "") + '" data-label="' + escapeHTML(permissionRoleLabel(row.role)) + '">删除</button></div></td>');
      }
      html.push('</tr>');
    }
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function permissionRoleLabel(role) {
    if (role === "global_owner") return "全局权限人";
    if (role === "global_operator") return "全局操作人";
    if (role === "group_operator") return "群操作人";
    return role || "-";
  }

  function permissionScopeLabel(scope) {
    if (scope === "all_groups") return "全部群";
    if (scope === "single_group") return "单群";
    return scope || "-";
  }

  function renderPermissionGroupOptions(groups, selectedChatId) {
    var html = ['<option value="">不选择群</option>'];
    for (var i = 0; i < (groups || []).length; i++) {
      var group = groups[i];
      var selected = String(group.chatId) === String(selectedChatId || "") ? " selected" : "";
      html.push('<option value="' + escapeHTML(group.chatId) + '"' + selected + '>' + escapeHTML((group.title || group.chatId) + " " + group.chatId) + '</option>');
    }
    return html.join("");
  }

  function renderPermissionsCenter(data) {
    data = data || {};
    var globalOwners = data.globalOwners || [];
    var globalOperators = data.globalOperators || [];
    var groupOperators = data.groupOperators || [];
    var groups = data.groups || [];
    var body = [
      '<section class="console-grid permissions-center">',
      '<section class="metric-strip">',
      metricCard("全局权限人", formatNumber(globalOwners.length), "可管理全局权限"),
      metricCard("全局操作人", formatNumber(globalOperators.length), "全部群可用"),
      metricCard("群操作人", formatNumber(groupOperators.length), "当前群范围"),
      metricCard("群总数", formatNumber(groups.length), "可授权群"),
      metricCard("管理方式", "保存即生效", "单管理员后台"),
      '</section>',
      '<div id="permissions-message" class="state-message">' + escapeHTML(permissionsState.message || "") + '</div>',
      '<section class="console-surface panel">',
      '<div class="panel-head"><h2>权限范围筛选</h2><span class="panel-meta">选择群后查看单群操作人</span></div>',
      '<form id="permissions-filter-form" class="filter-grid permissions-filter">',
      '<label>选择群<select name="chat_id">' + renderPermissionGroupOptions(groups, data.selectedChatId) + '</select></label>',
      '<div class="filter-actions"><button type="submit">查看</button></div>',
      '</form>',
      '</section>',
      '<section class="panel-row">',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>全局权限人</h2><span class="panel-meta">' + escapeHTML(globalOwners.length) + '</span></div>',
      renderPermissionForm("owner", "upsert_global_owner", "添加全局权限人"),
      renderPermissionRows(globalOwners, "暂无全局权限人", { editable: true, target: "owner", removeAction: "remove_global_owner" }),
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>全局操作人</h2><span class="panel-meta">' + escapeHTML(globalOperators.length) + '</span></div>',
      renderPermissionForm("operator", "upsert_global_operator", "添加全局操作人"),
      renderPermissionRows(globalOperators, "暂无全局操作人", { editable: true, target: "operator", removeAction: "remove_global_operator" }),
      '</article>',
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>群操作人</h2><span class="panel-meta">' + escapeHTML(groupOperators.length) + '</span></div>',
      renderPermissionRows(groupOperators, data.selectedChatId ? "当前群暂无操作人" : "请选择群后查看群操作人"),
      '</section>',
      '</section>'
    ].join("");
    renderShell({ title: "人员与权限中心", kicker: "运营总控台 / 人员与权限中心", activePage: "permissions" }, body);
    bindPermissionsCenterEvents();
  }

  function bindPermissionsCenterEvents() {
    var form = document.getElementById("permissions-filter-form");
    if (form) {
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        var chatID = String(form.chat_id.value || "").trim();
        history.pushState({}, "", chatID ? "/admin/permissions?chat_id=" + encodeURIComponent(chatID) : "/admin/permissions");
        loadPermissionsCenter();
      });
    }

    var writeForms = root.querySelectorAll(".permissions-write-form");
    for (var i = 0; i < writeForms.length; i++) {
      writeForms[i].addEventListener("submit", async function (event) {
        event.preventDefault();
        var target = String(this.getAttribute("data-permission-target") || "").trim();
        var action = String(this.getAttribute("data-permission-action") || "").trim();
        var userInput = this.querySelector('input[name="userId"]');
        var usernameInput = this.querySelector('input[name="username"]');
        var rawUserId = String(userInput && userInput.value || "").trim();
        var userId = Number(rawUserId);
        var username = String(usernameInput && usernameInput.value || "").trim();
        if (!rawUserId || !isFinite(userId) || userId <= 0 || String(Math.floor(userId)) !== rawUserId) {
          setPermissionMessage("用户ID格式错误");
          return;
        }
        setPermissionDraft(target, rawUserId, username);
        try {
          await postAdminJSON("/admin/api/permissions", {
            action: action,
            userId: userId,
            username: username
          });
          clearPermissionDraft(target);
          setPermissionMessage(action === "upsert_global_owner" ? "全局权限人已保存" : "全局操作人已保存");
          await loadPermissionsCenter();
        } catch (err) {
          setPermissionMessage(err && err.message ? err.message : "保存失败");
        }
      });
    }

    var clearButtons = root.querySelectorAll(".permission-clear-btn");
    for (var j = 0; j < clearButtons.length; j++) {
      clearButtons[j].addEventListener("click", function () {
        var target = String(this.getAttribute("data-permission-target") || "").trim();
        clearPermissionDraft(target);
        setPermissionMessage("");
      });
    }

    var editButtons = root.querySelectorAll(".permission-edit-btn");
    for (var k = 0; k < editButtons.length; k++) {
      editButtons[k].addEventListener("click", function () {
        var target = String(this.getAttribute("data-permission-target") || "").trim();
        var userId = String(this.getAttribute("data-user-id") || "").trim();
        var username = String(this.getAttribute("data-username") || "").trim();
        setPermissionDraft(target, userId, username);
        syncPermissionForm(target);
        setPermissionMessage("已载入记录，可直接保存");
      });
    }

    var removeButtons = root.querySelectorAll(".permission-remove-btn");
    for (var m = 0; m < removeButtons.length; m++) {
      removeButtons[m].addEventListener("click", async function () {
        var action = String(this.getAttribute("data-permission-action") || "").trim();
        var label = String(this.getAttribute("data-label") || "该记录").trim();
        var rawUserId = String(this.getAttribute("data-user-id") || "").trim();
        var userId = Number(rawUserId);
        if (!rawUserId || !isFinite(userId) || userId <= 0) {
          setPermissionMessage("用户ID格式错误");
          return;
        }
        if (!window.confirm("确认删除" + label + "？")) {
          return;
        }
        try {
          await postAdminJSON("/admin/api/permissions", {
            action: action,
            userId: userId
          });
          setPermissionMessage(label + "已删除");
          await loadPermissionsCenter();
        } catch (err) {
          setPermissionMessage(err && err.message ? err.message : "删除失败");
        }
      });
    }
  }

  async function loadPermissionsCenter() {
    renderLoading("人员与权限中心", "permissions");
    try {
      var params = new URLSearchParams(window.location.search || "");
      var chatID = String(params.get("chat_id") || "").trim();
      var data = await fetchAdminJSON("/admin/api/permissions" + (chatID ? "?chat_id=" + encodeURIComponent(chatID) : ""));
      renderPermissionsCenter(data || {});
    } catch (err) {
      renderError("人员与权限中心", "permissions", err && err.message ? err.message : "加载失败");
    }
  }

  function selectedSwitchChatIDs() {
    return Object.keys(switchState.selected)
      .filter(function (key) { return switchState.selected[key]; })
      .map(function (key) { return Number(key); })
      .filter(function (value) { return isFinite(value) && value !== 0; });
  }

  function switchFeatureLabel(feature) {
    if (feature === "bookkeeping") return "记账";
    if (feature === "ledger_card_hide_reversed") return "撤回记录从账单卡片隐藏";
    if (feature === "welcome") return "新成员欢迎";
    if (feature === "auto_reply") return "自动回复";
    if (feature === "at_broadcast") return "@群发";
    if (feature === "closing_reminder") return "下课提醒";
    if (feature === "closing_reminder_time") return "下课提醒时间";
    if (feature === "welcome_text") return "欢迎模板";
    return feature || "-";
  }

  function renderSwitchSummary(items) {
    if (!items || items.length === 0) {
      return '<div class="state-empty">暂无功能状态</div>';
    }
    var html = ['<div class="switch-summary-grid">'];
    for (var i = 0; i < items.length; i++) {
      var item = items[i];
      var total = Number(item.enabledCount || 0) + Number(item.disabledCount || 0);
      var coverage = total > 0 ? Math.round(Number(item.enabledCount || 0) * 100 / total) + "%" : "0%";
      var feature = String(item.feature || "");
      html.push('<button type="button" class="switch-summary-card' + (switchState.feature === feature ? ' is-active' : '') + '" data-switch-feature="' + escapeHTML(feature) + '">');
      html.push('<span class="switch-summary-card-title">' + escapeHTML(item.label || switchFeatureLabel(feature)) + '</span>');
      html.push('<span class="switch-summary-card-count"><strong>' + escapeHTML(item.enabledCount || 0) + '</strong> / ' + escapeHTML(total) + ' 群开启</span>');
      html.push('<span class="switch-summary-card-meta">关闭 ' + escapeHTML(item.disabledCount || 0) + ' · 覆盖率 ' + escapeHTML(coverage) + '</span>');
      html.push('<span class="switch-summary-card-action">查看具体群 →</span>');
      html.push('</button>');
    }
    html.push('</div>');
    return html.join("");
  }

  function renderSwitchTargetRows(groups) {
    if (!groups || groups.length === 0) {
      return '<div class="state-empty">暂无群组</div>';
    }
    var html = ['<div class="switch-target-list">'];
    for (var i = 0; i < groups.length; i++) {
      var group = groups[i];
      var id = String(group.chatId);
      var checked = switchState.selected[id] ? " checked" : "";
      html.push(
        '<label class="switch-target-item">',
        '<input type="checkbox" class="switch-target-check" value="' + escapeHTML(id) + '"' + checked + '>',
        '<span><strong>' + escapeHTML(group.title || group.chatId) + '</strong><small>' + escapeHTML(group.chatId) + ' · <span class="badge ' + (group.enabled ? 'badge-low' : 'badge-medium') + '">' + (group.enabled ? '开启' : '关闭') + '</span></small></span>',
        '</label>'
      );
    }
    html.push('</div>');
    return html.join("");
  }

  function renderSwitchDetail(detail) {
    if (!switchState.feature) return '<div class="state-empty">点击上方功能卡片查看具体群。</div>';
    if (!detail) return '<div class="state-empty">正在加载具体群状态...</div>';
    var groups = detail.groups || [];
    var html = ['<div class="switch-detail-head"><div><h3>' + escapeHTML(detail.label || switchFeatureLabel(switchState.feature)) + '</h3><p class="muted">开启 ' + escapeHTML(detail.enabledCount || 0) + ' · 关闭 ' + escapeHTML(detail.disabledCount || 0) + ' · 本次读取 ' + escapeHTML(detail.readAt || "-") + '</p></div><div class="switch-detail-actions"><button type="button" id="select-all-switch-targets" class="ghost-btn">全选本页</button><button type="button" id="clear-switch-targets" class="ghost-btn">清空选择</button></div></div>'];
    if (!groups.length) html.push('<div class="state-empty">没有符合条件的群。</div>');
    else html.push(renderSwitchTargetRows(groups));
    var page = Number(detail.page || 1);
    var pageSize = Number(detail.pageSize || 20);
    var total = Number(detail.total || 0);
    var lastPage = Math.max(1, Math.ceil(total / pageSize));
    html.push('<div class="switch-pagination"><button type="button" class="ghost-btn" id="switch-prev-page"' + (page <= 1 ? ' disabled' : '') + '>上一页</button><span>第 ' + page + ' / ' + lastPage + ' 页 · 共 ' + total + ' 群</span><button type="button" class="ghost-btn" id="switch-next-page"' + (page >= lastPage ? ' disabled' : '') + '>下一页</button></div>');
    return html.join("");
  }

  function renderSwitchPreview(preview) {
    if (!preview) {
      return '<div class="state-empty">尚未生成预演。</div>';
    }
    return [
      '<div class="receipt-summary">',
      '<span>匹配 ' + escapeHTML(preview.matchedCount || 0) + ' 个群</span>',
      '<span>变更 ' + escapeHTML((preview.affected || []).length) + ' 个</span>',
      '<span>跳过 ' + escapeHTML((preview.skipped || []).length) + ' 个</span>',
      '</div>',
      '<div class="receipt-columns">',
      '<div><h3>将要变更</h3>' + renderPreviewList(preview.affected, "暂无变更项") + '</div>',
      '<div><h3>跳过</h3>' + renderPreviewList(preview.skipped, "暂无跳过项") + '</div>',
      '</div>'
    ].join("");
  }

  function renderSwitchReceipt(receipt) {
    if (!receipt) {
      return '<div class="state-empty">执行后显示操作结果回执。</div>';
    }
    return [
      renderBatchReceipt(receipt),
      receipt.rollbackToken ? '<button type="button" id="rollback-switch-btn" class="ghost-btn rollback-btn">恢复上次变更</button>' : ''
    ].join("");
  }

  function renderSwitchCenter(summaryData, groupsData) {
    summaryData = summaryData || {};
    switchState.totalGroups = summaryData.totalGroups || 0;
    switchState.switches = summaryData.switches || [];
    switchState.groups = switchState.detail && switchState.detail.groups ? switchState.detail.groups : [];
    var selectedCount = selectedSwitchChatIDs().length;
    var body = [
      '<section class="console-grid switch-center">',
      '<section class="metric-strip">',
      metricCard("群总数", formatNumber(switchState.totalGroups), "当前管理范围"),
      metricCard("功能项", formatNumber(switchState.switches.length), "可统一控制"),
      metricCard("当前功能", switchState.feature ? switchFeatureLabel(switchState.feature) : "未选择", "点击功能卡片后加载"),
      metricCard("已选群", formatNumber(selectedCount), "仅选中群模式生效"),
      metricCard("执行方式", "预演后确认", "可恢复最近变更"),
      '</section>',
      '<div id="switch-unsaved-banner" class="unsaved-banner" style="' + (switchState.dirty ? "" : "display:none") + '">未保存：当前有尚未预演的开关草稿。</div>',
      '<article class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>功能状态</h2><span class="panel-meta">点击卡片查看具体群</span></div>',
      renderSwitchSummary(switchState.switches),
      '</article>',
      '<section class="panel-row">',
      '<article class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>具体群状态</h2><span class="panel-meta">' + (switchState.feature ? 'API 懒加载 · ' + escapeHTML(switchState.feature) : '尚未选择功能') + '</span></div>',
      (switchState.feature ? '<form id="switch-detail-filter" class="filter-grid"><label>搜索群名或 ID<input name="q" value="' + escapeHTML(switchState.detailFilters.q) + '" maxlength="80" placeholder="搜索群名或群 ID"></label><label>状态<select name="status"><option value="all"' + (switchState.detailFilters.status === 'all' ? ' selected' : '') + '>全部</option><option value="enabled"' + (switchState.detailFilters.status === 'enabled' ? ' selected' : '') + '>已开启</option><option value="disabled"' + (switchState.detailFilters.status === 'disabled' ? ' selected' : '') + '>已关闭</option></select></label><div class="filter-actions"><button type="submit">查询</button></div></form>' : ''),
      renderSwitchDetail(switchState.detail),
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>变更配置</h2><span class="panel-meta">操作前预演</span></div>',
      '<form id="switch-form" class="switch-form">',
      '<label>当前功能<input name="featureLabel" value="' + escapeHTML(switchState.feature ? switchFeatureLabel(switchState.feature) : '请先点击功能卡片') + '" readonly></label>',
      '<label id="switch-enabled-label">状态<select name="enabled"><option value="1">开启</option><option value="0">关闭</option></select></label>',
      '<fieldset class="segmented"><legend>目标范围</legend>',
      '<label><input type="radio" name="scope" value="all"' + (switchState.scope === "all" ? " checked" : "") + '><span>全部群</span></label>',
      '<label><input type="radio" name="scope" value="selected"' + (switchState.scope === "selected" ? " checked" : "") + '><span>选中群</span></label>',
      '</fieldset>',
      '<button type="submit"' + (switchState.feature ? '' : ' disabled') + '>操作前预演</button>',
      '</form>',
      '</article>',
      '</section>',
      '<section class="panel-row">',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>预演结果</h2><span class="panel-meta">确认前不写库</span></div>',
      renderSwitchPreview(switchState.preview),
      switchState.preview && switchState.preview.applyToken ? '<button type="button" id="apply-switch-btn" class="danger-btn">确认执行预演结果</button>' : '',
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>操作结果回执</h2><span class="panel-meta">成功 / 跳过 / 失败</span></div>',
      renderSwitchReceipt(switchState.receipt),
      '</article>',
      '</section>',
      '</section>'
    ].join("");
    renderShell({ title: "全局功能开关中心", kicker: "运营总控台 / 全局功能开关中心", activePage: "switches" }, body);
    bindSwitchCenterEvents();
  }

  function updateSwitchValueVisibility() {
    var enabledLabel = document.getElementById("switch-enabled-label");
    if (enabledLabel) enabledLabel.style.display = switchState.feature ? "grid" : "none";
  }

  function bindSwitchCenterEvents() {
    root.querySelectorAll(".switch-summary-card").forEach(function (button) {
      button.addEventListener("click", function () {
        switchState.feature = button.getAttribute("data-switch-feature") || "";
        switchState.detail = null;
        switchState.detailFilters = { q: "", status: "all", page: 1, pageSize: 20 };
        switchState.selected = {};
        switchState.preview = null;
        switchState.receipt = null;
        setSwitchDirty(false);
        renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches });
        loadSwitchFeature();
      });
    });
    var form = document.getElementById("switch-form");
    if (form) {
      form.addEventListener("input", function () { setSwitchDirty(true); });
      form.addEventListener("change", function () {
        switchState.scope = String(form.scope.value || "all");
        setSwitchDirty(true);
        updateSwitchValueVisibility();
      });
      form.addEventListener("submit", previewSwitchChange);
    }
    updateSwitchValueVisibility();

    var detailFilter = document.getElementById("switch-detail-filter");
    if (detailFilter) detailFilter.addEventListener("submit", function (event) {
      event.preventDefault();
      switchState.detailFilters.q = String(detailFilter.q.value || "").trim();
      switchState.detailFilters.status = String(detailFilter.status.value || "all");
      switchState.detailFilters.page = 1;
      loadSwitchFeature();
    });

    var checks = root.querySelectorAll(".switch-target-check");
    for (var i = 0; i < checks.length; i++) {
      checks[i].addEventListener("change", function () {
        switchState.selected[String(this.value)] = this.checked;
        switchState.preview = null;
        setSwitchDirty(true);
      });
    }
    var selectAll = document.getElementById("select-all-switch-targets");
    if (selectAll) {
      selectAll.addEventListener("click", function () {
        for (var i = 0; i < switchState.groups.length; i++) {
          switchState.selected[String(switchState.groups[i].chatId)] = true;
        }
        switchState.scope = "selected";
        switchState.preview = null;
        setSwitchDirty(true);
        renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches }, { groups: switchState.groups });
      });
    }
    var clear = document.getElementById("clear-switch-targets");
    if (clear) {
      clear.addEventListener("click", function () {
        switchState.selected = {};
        switchState.preview = null;
        setSwitchDirty(true);
        renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches }, { groups: switchState.groups });
      });
    }
    var previous = document.getElementById("switch-prev-page");
    if (previous) previous.addEventListener("click", function () {
      if (switchState.detailFilters.page > 1) {
        switchState.detailFilters.page--;
        loadSwitchFeature();
      }
    });
    var next = document.getElementById("switch-next-page");
    if (next) next.addEventListener("click", function () {
      if (switchState.detail && switchState.detail.page < Math.ceil(Number(switchState.detail.total || 0) / Number(switchState.detail.pageSize || 20))) {
        switchState.detailFilters.page++;
        loadSwitchFeature();
      }
    });
    var apply = document.getElementById("apply-switch-btn");
    if (apply) apply.addEventListener("click", applySwitchChange);
    var rollback = document.getElementById("rollback-switch-btn");
    if (rollback) rollback.addEventListener("click", rollbackSwitchChange);
  }

  async function loadSwitchCenter() {
    renderLoading("全局功能开关中心", "switches");
    try {
      var summary = await fetchAdminJSON("/admin/api/switches");
      renderSwitchCenter(summary || {});
    } catch (err) {
      renderError("全局功能开关中心", "switches", err && err.message ? err.message : "加载失败");
    }
  }

  async function loadSwitchFeature() {
    if (!switchState.feature) return;
    var requestID = ++switchState.detailRequest;
    var filters = switchState.detailFilters;
    var query = "?status=" + encodeURIComponent(filters.status) + "&q=" + encodeURIComponent(filters.q) + "&page=" + encodeURIComponent(filters.page) + "&pageSize=" + encodeURIComponent(filters.pageSize);
    try {
      var detail = await fetchAdminJSON("/admin/api/switches/" + encodeURIComponent(switchState.feature) + query);
      if (requestID !== switchState.detailRequest) return;
      switchState.detail = detail;
      switchState.groups = detail.groups || [];
      renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches });
    } catch (err) {
      if (requestID !== switchState.detailRequest) return;
      switchState.detail = null;
      renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches });
      renderError("全局功能开关中心", "switches", err && err.message ? err.message : "群状态加载失败");
    }
  }

  async function previewSwitchChange(event) {
    event.preventDefault();
    var form = event.target;
    var feature = String(switchState.feature || "").trim();
    if (!feature) {
      renderError("全局功能开关中心", "switches", "请先点击一个功能卡片");
      return;
    }
    var scope = String(form.scope.value || "all");
    var chatIDs = scope === "selected" ? selectedSwitchChatIDs() : [];
    if (scope === "selected" && chatIDs.length === 0) {
      renderError("全局功能开关中心", "switches", "请选择至少一个目标群");
      return;
    }
    var request = { feature: feature, chatIds: chatIDs };
    request.enabled = String(form.enabled.value || "0") === "1";
    try {
      switchState.scope = scope;
      switchState.preview = await postAdminJSON("/admin/api/switches/preview", request);
      switchState.receipt = null;
      setSwitchDirty(false);
      renderSwitchCenter({ totalGroups: switchState.totalGroups, switches: switchState.switches });
    } catch (err) {
      renderError("全局功能开关中心", "switches", err && err.message ? err.message : "预演失败");
    }
  }

  async function applySwitchChange() {
    if (!switchState.preview || !switchState.preview.applyToken) return;
    try {
      switchState.receipt = await postAdminJSON("/admin/api/switches/apply", { applyToken: switchState.preview.applyToken });
      switchState.preview = null;
      setSwitchDirty(false);
      await loadSwitchCenter();
    } catch (err) {
      renderError("全局功能开关中心", "switches", err && err.message ? err.message : "执行失败");
    }
  }

  async function rollbackSwitchChange() {
    if (!switchState.receipt || !switchState.receipt.rollbackToken) return;
    try {
      var token = switchState.receipt.rollbackToken;
      switchState.receipt = await postAdminJSON("/admin/api/switches/rollback", { rollbackToken: token });
      switchState.preview = null;
      setSwitchDirty(false);
      await loadSwitchCenter();
    } catch (err) {
      renderError("全局功能开关中心", "switches", err && err.message ? err.message : "恢复失败");
    }
  }

  function setBroadcastDirty(value) {
    broadcastState.dirty = !!value;
    var banner = document.getElementById("broadcast-unsaved-banner");
    if (banner) banner.style.display = broadcastState.dirty ? "block" : "none";
  }

  function selectedBroadcastChatIDs() {
    return Object.keys(broadcastState.selected)
      .filter(function (key) { return broadcastState.selected[key]; })
      .map(function (key) { return Number(key); })
      .filter(function (value) { return isFinite(value) && value !== 0; });
  }

  function renderGroupOptions(groups, selected, includeEmpty) {
    var html = [];
    if (includeEmpty) html.push('<option value="">不指定</option>');
    for (var i = 0; i < (groups || []).length; i++) {
      var group = groups[i];
      var isSelected = String(group.chatId) === String(selected || "") ? " selected" : "";
      html.push('<option value="' + escapeHTML(group.chatId) + '"' + isSelected + '>' + escapeHTML((group.title || group.chatId) + " · " + group.chatId) + '</option>');
    }
    return html.join("");
  }

  function renderBroadcastTargets(groups) {
    if (!groups || groups.length === 0) return '<div class="state-empty">暂无群组</div>';
    var html = ['<div class="broadcast-target-list">'];
    for (var i = 0; i < groups.length; i++) {
      var group = groups[i];
      var id = String(group.chatId);
      html.push(
        '<label class="switch-target-item">',
        '<input type="checkbox" class="broadcast-target-check" value="' + escapeHTML(id) + '"' + (broadcastState.selected[id] ? " checked" : "") + '>',
        '<span><strong>' + escapeHTML(group.title || group.chatId) + '</strong><small>' + escapeHTML(group.chatId) + ' · ' + escapeHTML(group.groupTags || "无标签") + '</small></span>',
        '</label>'
      );
    }
    html.push('</div>');
    return html.join("");
  }

  function renderBroadcastReceipt(receipt) {
    if (!receipt) return '<div class="state-empty">发送后显示操作结果回执。</div>';
    return [
      '<div class="receipt-summary">',
      '<span>目标 ' + escapeHTML(receipt.matchedCount || 0) + ' 个群</span>',
      '<span>成功 ' + escapeHTML(receipt.sentCount || 0) + ' 个</span>',
      '<span>失败 ' + escapeHTML(receipt.failedCount || 0) + ' 个</span>',
      '</div>',
      renderPreviewList(receipt.targets, "暂无目标")
    ].join("");
  }

  function renderBroadcastPreview(preview) {
    if (!preview) return '<div class="state-empty">尚未生成群发预演。</div>';
    var html = [
      '<div class="receipt-summary"><span>目标 ' + escapeHTML(preview.matchedCount || 0) + ' 个群</span><span>' + escapeHTML(preview.mediaType || "text") + '</span></div>',
      '<div class="message-preview">' + escapeHTML(preview.content || "[媒体消息]") + '</div>',
      '<div class="broadcast-preview-targets">'
    ];
    for (var i = 0; i < (preview.targets || []).length; i++) {
      var target = preview.targets[i];
      html.push('<span>' + escapeHTML(target.title || target.chatId) + '</span>');
    }
    html.push('</div>');
    return html.join("");
  }

  function renderBroadcastCenter(groupsData) {
    groupsData = groupsData || {};
    broadcastState.groups = groupsData.groups || broadcastState.groups || [];
    var selectedCount = selectedBroadcastChatIDs().length;
    var body = [
      '<section class="console-grid broadcast-center">',
      '<section class="metric-strip">',
      metricCard("可选群", formatNumber(broadcastState.groups.length), "当前管理范围"),
      metricCard("已选择", formatNumber(selectedCount), "本次发送目标"),
      metricCard("发送方式", "预演后确认", "确认前不发送"),
      metricCard("内容类型", "文字 / 媒体", "支持 FileID"),
      metricCard("结果", broadcastState.receipt ? formatNumber(broadcastState.receipt.sentCount || 0) : "-", "最近一次成功数"),
      '</section>',
      '<div id="broadcast-unsaved-banner" class="unsaved-banner" style="' + (broadcastState.dirty ? "" : "display:none") + '">未保存：当前群发内容尚未预演。</div>',
      '<section class="panel-row">',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>群发配置</h2><span class="panel-meta">编辑内容</span></div>',
      '<form id="broadcast-form" class="message-form">',
      '<label>来源群<select name="sourceChatId">' + renderGroupOptions(broadcastState.groups, broadcastState.draft.sourceChatId, true) + '</select></label>',
      '<label>媒体类型<select name="mediaType" id="broadcast-media-type"><option value="text"' + (broadcastState.draft.mediaType === "text" ? " selected" : "") + '>文字</option><option value="photo"' + (broadcastState.draft.mediaType === "photo" ? " selected" : "") + '>图片</option><option value="video"' + (broadcastState.draft.mediaType === "video" ? " selected" : "") + '>视频</option><option value="voice"' + (broadcastState.draft.mediaType === "voice" ? " selected" : "") + '>语音</option></select></label>',
      '<label id="broadcast-file-label" class="message-file-label">媒体 FileID<input name="mediaFileId" value="' + escapeHTML(broadcastState.draft.mediaFileId) + '" placeholder="Telegram FileID"></label>',
      '<label>指定身份<input name="identityName" value="' + escapeHTML(broadcastState.draft.identityName) + '" placeholder="可选身份名称"></label>',
      '<label class="message-content-label">内容<textarea name="content" maxlength="4096">' + escapeHTML(broadcastState.draft.content) + '</textarea></label>',
      '<button type="submit">群发预演</button>',
      '</form>',
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>预演结果</h2><span class="panel-meta">确认前不发送</span></div>',
      renderBroadcastPreview(broadcastState.preview),
      broadcastState.preview && broadcastState.preview.applyToken ? '<button type="button" id="apply-broadcast-btn" class="danger-btn">确认发送到目标群</button>' : '',
      '</article>',
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>选择目标群</h2><span class="panel-meta">已选择 ' + escapeHTML(selectedCount) + '</span></div>',
      '<div class="switch-target-actions"><button type="button" id="select-all-broadcast-targets" class="ghost-btn">全选</button><button type="button" id="clear-broadcast-targets" class="ghost-btn">清空</button></div>',
      renderBroadcastTargets(broadcastState.groups),
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>操作结果回执</h2><span class="panel-meta">成功 / 失败</span></div>',
      renderBroadcastReceipt(broadcastState.receipt),
      '</section>',
      '</section>'
    ].join("");
    renderShell({ title: "群发面板", kicker: "运营总控台 / 群发面板", activePage: "broadcast" }, body);
    bindBroadcastEvents();
  }

  function updateBroadcastMediaFields() {
    var form = document.getElementById("broadcast-form");
    var label = document.getElementById("broadcast-file-label");
    if (!form || !label) return;
    label.style.display = String(form.mediaType.value || "text") === "text" ? "none" : "grid";
  }

  function captureBroadcastDraft(form) {
    if (!form) return;
    broadcastState.draft = {
      sourceChatId: String(form.sourceChatId.value || ""),
      mediaType: String(form.mediaType.value || "text"),
      mediaFileId: String(form.mediaFileId.value || ""),
      identityName: String(form.identityName.value || ""),
      content: String(form.content.value || "")
    };
  }

  function bindBroadcastEvents() {
    var form = document.getElementById("broadcast-form");
    if (form) {
      form.addEventListener("input", function () { captureBroadcastDraft(form); setBroadcastDirty(true); });
      form.addEventListener("change", function () { captureBroadcastDraft(form); setBroadcastDirty(true); updateBroadcastMediaFields(); });
      form.addEventListener("submit", previewBroadcast);
    }
    updateBroadcastMediaFields();
    var checks = root.querySelectorAll(".broadcast-target-check");
    for (var i = 0; i < checks.length; i++) {
      checks[i].addEventListener("change", function () {
        broadcastState.selected[String(this.value)] = this.checked;
        broadcastState.preview = null;
        setBroadcastDirty(true);
      });
    }
    var selectAll = document.getElementById("select-all-broadcast-targets");
    if (selectAll) selectAll.addEventListener("click", function () {
      for (var i = 0; i < broadcastState.groups.length; i++) broadcastState.selected[String(broadcastState.groups[i].chatId)] = true;
      broadcastState.preview = null;
      setBroadcastDirty(true);
      renderBroadcastCenter({ groups: broadcastState.groups });
    });
    var clear = document.getElementById("clear-broadcast-targets");
    if (clear) clear.addEventListener("click", function () {
      broadcastState.selected = {};
      broadcastState.preview = null;
      setBroadcastDirty(true);
      renderBroadcastCenter({ groups: broadcastState.groups });
    });
    var apply = document.getElementById("apply-broadcast-btn");
    if (apply) apply.addEventListener("click", applyBroadcast);
  }

  async function loadBroadcastCenter() {
    renderLoading("群发面板", "broadcast");
    try {
      var groups = await fetchAdminJSON("/admin/api/groups");
      renderBroadcastCenter(groups || {});
    } catch (err) {
      renderError("群发面板", "broadcast", err && err.message ? err.message : "加载失败");
    }
  }

  async function previewBroadcast(event) {
    event.preventDefault();
    var form = event.target;
    captureBroadcastDraft(form);
    var chatIDs = selectedBroadcastChatIDs();
    if (chatIDs.length === 0) {
      renderError("群发面板", "broadcast", "请选择至少一个目标群");
      return;
    }
    try {
      broadcastState.preview = await postAdminJSON("/admin/api/broadcast/preview", {
        sourceChatId: Number(form.sourceChatId.value || 0),
        chatIds: chatIDs,
        identityName: String(form.identityName.value || "").trim(),
        mediaType: String(form.mediaType.value || "text"),
        mediaFileId: String(form.mediaFileId.value || "").trim(),
        content: String(form.content.value || "").trim()
      });
      broadcastState.receipt = null;
      setBroadcastDirty(false);
      renderBroadcastCenter({ groups: broadcastState.groups });
    } catch (err) {
      renderError("群发面板", "broadcast", err && err.message ? err.message : "群发预演失败");
    }
  }

  async function applyBroadcast() {
    if (!broadcastState.preview || !broadcastState.preview.applyToken) return;
    try {
      broadcastState.receipt = await postAdminJSON("/admin/api/broadcast/apply", { applyToken: broadcastState.preview.applyToken });
      broadcastState.preview = null;
      setBroadcastDirty(false);
      renderBroadcastCenter({ groups: broadcastState.groups });
    } catch (err) {
      renderError("群发面板", "broadcast", err && err.message ? err.message : "群发失败");
    }
  }

  function setScheduleDirty(value) {
    scheduleState.dirty = !!value;
    var banner = document.getElementById("schedule-unsaved-banner");
    if (banner) banner.style.display = scheduleState.dirty ? "block" : "none";
  }

  function scheduleStatusLabel(status) {
    if (status === "pending") return "待发送";
    if (status === "canceled") return "已暂停";
    if (status === "sent") return "已发送";
    if (status === "failed") return "失败";
    return status || "-";
  }

  function scheduleInputValue(raw) {
    var date = raw ? new Date(raw) : new Date(Date.now() + 10 * 60 * 1000);
    if (!isFinite(date.getTime())) return "";
    var local = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
    return local.toISOString().slice(0, 16);
  }

  function renderScheduleRows(rows) {
    if (!rows || rows.length === 0) return '<div class="state-empty">暂无定时任务</div>';
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th>任务</th><th>群</th><th>发送时间</th><th>状态</th><th>内容</th><th>操作</th></tr></thead><tbody>'];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      html.push('<tr>');
      html.push('<td>#' + escapeHTML(row.id) + '</td>');
      html.push('<td><div class="group-title">' + escapeHTML(row.groupTitle || row.chatId) + '</div><div class="group-sub">' + escapeHTML(row.chatId) + '</div></td>');
      html.push('<td>' + escapeHTML(row.sendAt || "-") + '</td>');
      html.push('<td><span class="' + (row.status === "pending" ? "badge badge-low" : "badge badge-medium") + '">' + escapeHTML(scheduleStatusLabel(row.status)) + '</span></td>');
      html.push('<td class="schedule-content">' + escapeHTML(row.content || "-") + '</td>');
      html.push('<td><div class="table-actions">');
      html.push('<button type="button" class="ghost-btn schedule-edit-btn" data-schedule-index="' + i + '">编辑</button>');
      if (row.status === "canceled") {
        html.push('<button type="button" class="ghost-btn schedule-action-btn" data-action="resume" data-schedule-index="' + i + '">恢复</button>');
      } else {
        html.push('<button type="button" class="ghost-btn schedule-action-btn" data-action="pause" data-schedule-index="' + i + '">暂停</button>');
      }
      html.push('<button type="button" class="ghost-btn schedule-action-btn" data-action="delete" data-schedule-index="' + i + '">删除</button>');
      html.push('</div></td></tr>');
    }
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderScheduleSnapshot(item, emptyText) {
    if (!item) return '<div class="state-empty">' + escapeHTML(emptyText || "无") + '</div>';
    return '<div class="schedule-snapshot"><strong>#' + escapeHTML(item.id || "新任务") + ' · ' + escapeHTML(item.groupTitle || item.chatId) + '</strong><span>' + escapeHTML(scheduleStatusLabel(item.status)) + ' · ' + escapeHTML(item.sendAt || "-") + '</span><p>' + escapeHTML(item.content || "-") + '</p></div>';
  }

  function renderSchedulePreview(preview) {
    if (!preview) return '<div class="state-empty">尚未生成定时任务预演。</div>';
    return [
      '<div class="receipt-summary"><span>动作 ' + escapeHTML(preview.action || "-") + '</span><span>目标 ' + escapeHTML(preview.matchedCount || 0) + ' 个群</span></div>',
      '<div class="receipt-columns">',
      '<div><h3>变更前</h3>' + renderScheduleSnapshot(preview.before, "新建任务") + '</div>',
      '<div><h3>变更后</h3>' + renderScheduleSnapshot(preview.after, "-") + '</div>',
      '</div>'
    ].join("");
  }

  function renderScheduleCenter(groupsData, schedulesData) {
    groupsData = groupsData || {};
    schedulesData = schedulesData || {};
    scheduleState.groups = groupsData.groups || scheduleState.groups || [];
    scheduleState.schedules = schedulesData.schedules || scheduleState.schedules || [];
    var editing = scheduleState.editing;
    var body = [
      '<section class="console-grid schedule-center">',
      '<section class="metric-strip">',
      metricCard("任务总数", formatNumber(scheduleState.schedules.length), "当前定时任务"),
      metricCard("待发送", formatNumber(scheduleState.schedules.filter(function (row) { return row.status === "pending"; }).length), "已排程"),
      metricCard("已暂停", formatNumber(scheduleState.schedules.filter(function (row) { return row.status === "canceled"; }).length), "可恢复"),
      metricCard("可选群", formatNumber(scheduleState.groups.length), "当前管理范围"),
      metricCard("执行方式", "预演后确认", "所有写操作一致"),
      '</section>',
      '<div id="schedule-unsaved-banner" class="unsaved-banner" style="' + (scheduleState.dirty ? "" : "display:none") + '">未保存：当前定时任务尚未预演。</div>',
      '<section class="panel-row">',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>' + (editing ? "编辑定时任务" : "新建定时任务") + '</h2><span class="panel-meta">' + (editing ? "#" + escapeHTML(editing.id) : "定时任务预演") + '</span></div>',
      '<form id="schedule-form" class="message-form">',
      '<label>目标群<select name="chatId">' + renderGroupOptions(scheduleState.groups, editing && editing.chatId, false) + '</select></label>',
      '<label>发送时间<input type="datetime-local" name="sendAt" value="' + escapeHTML(scheduleInputValue(editing && editing.sendAt)) + '"></label>',
      '<label>指定身份<input name="identityName" value="' + escapeHTML(editing && editing.identityName) + '"></label>',
      '<label>目标标签<input name="targetTags" value="' + escapeHTML(editing && editing.targetTags) + '"></label>',
      '<label class="message-content-label">内容<textarea name="content" maxlength="4096">' + escapeHTML(editing && editing.content) + '</textarea></label>',
      '<div class="form-actions"><button type="submit">定时任务预演</button>' + (editing ? '<button type="button" id="cancel-schedule-edit" class="ghost-btn">取消编辑</button>' : '') + '</div>',
      '</form>',
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>预演结果</h2><span class="panel-meta">确认前不写库</span></div>',
      renderSchedulePreview(scheduleState.preview),
      scheduleState.preview && scheduleState.preview.applyToken ? '<button type="button" id="apply-schedule-btn" class="danger-btn">确认执行定时任务操作</button>' : '',
      '</article>',
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>定时任务</h2><span class="panel-meta">' + escapeHTML(scheduleState.schedules.length) + '</span></div>',
      renderScheduleRows(scheduleState.schedules),
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>操作结果回执</h2><span class="panel-meta">最近一次</span></div>',
      scheduleState.receipt ? '<div class="receipt-summary"><span>动作 ' + escapeHTML(scheduleState.receipt.action) + '</span><span>任务 #' + escapeHTML(scheduleState.receipt.scheduleId) + '</span><span>' + escapeHTML(scheduleStatusLabel(scheduleState.receipt.status)) + '</span></div>' : '<div class="state-empty">执行后显示操作结果回执。</div>',
      '</section>',
      '</section>'
    ].join("");
    renderShell({ title: "定时群发", kicker: "运营总控台 / 定时群发", activePage: "schedules" }, body);
    bindScheduleEvents();
  }

  function bindScheduleEvents() {
    var form = document.getElementById("schedule-form");
    if (form) {
      form.addEventListener("input", function () { setScheduleDirty(true); });
      form.addEventListener("change", function () { setScheduleDirty(true); });
      form.addEventListener("submit", previewScheduleForm);
    }
    var editButtons = root.querySelectorAll(".schedule-edit-btn");
    for (var i = 0; i < editButtons.length; i++) editButtons[i].addEventListener("click", function () {
      scheduleState.editing = scheduleState.schedules[Number(this.getAttribute("data-schedule-index"))] || null;
      scheduleState.preview = null;
      setScheduleDirty(false);
      renderScheduleCenter({ groups: scheduleState.groups }, { schedules: scheduleState.schedules });
    });
    var actionButtons = root.querySelectorAll(".schedule-action-btn");
    for (var j = 0; j < actionButtons.length; j++) actionButtons[j].addEventListener("click", function () {
      var row = scheduleState.schedules[Number(this.getAttribute("data-schedule-index"))];
      if (row) previewScheduleAction(String(this.getAttribute("data-action") || ""), row);
    });
    var cancel = document.getElementById("cancel-schedule-edit");
    if (cancel) cancel.addEventListener("click", function () {
      scheduleState.editing = null;
      scheduleState.preview = null;
      setScheduleDirty(false);
      renderScheduleCenter({ groups: scheduleState.groups }, { schedules: scheduleState.schedules });
    });
    var apply = document.getElementById("apply-schedule-btn");
    if (apply) apply.addEventListener("click", applySchedule);
  }

  async function loadScheduleCenter() {
    renderLoading("定时群发", "schedules");
    try {
      var results = await Promise.all([fetchAdminJSON("/admin/api/groups"), fetchAdminJSON("/admin/api/schedules")]);
      renderScheduleCenter(results[0] || {}, results[1] || {});
    } catch (err) {
      renderError("定时群发", "schedules", err && err.message ? err.message : "加载失败");
    }
  }

  async function previewScheduleForm(event) {
    event.preventDefault();
    var form = event.target;
    var editing = scheduleState.editing;
    try {
      scheduleState.preview = await postAdminJSON("/admin/api/schedules/preview", {
        action: editing ? "update" : "create",
        chatId: Number(form.chatId.value || 0),
        scheduleId: editing ? Number(editing.id) : 0,
        sendAt: String(form.sendAt.value || ""),
        content: String(form.content.value || "").trim(),
        identityName: String(form.identityName.value || "").trim(),
        targetTags: String(form.targetTags.value || "").trim()
      });
      scheduleState.receipt = null;
      setScheduleDirty(false);
      renderScheduleCenter({ groups: scheduleState.groups }, { schedules: scheduleState.schedules });
    } catch (err) {
      renderError("定时群发", "schedules", err && err.message ? err.message : "定时任务预演失败");
    }
  }

  async function previewScheduleAction(action, row) {
    try {
      scheduleState.preview = await postAdminJSON("/admin/api/schedules/preview", { action: action, chatId: Number(row.chatId), scheduleId: Number(row.id) });
      scheduleState.receipt = null;
      setScheduleDirty(false);
      renderScheduleCenter({ groups: scheduleState.groups }, { schedules: scheduleState.schedules });
    } catch (err) {
      renderError("定时群发", "schedules", err && err.message ? err.message : "定时任务预演失败");
    }
  }

  async function applySchedule() {
    if (!scheduleState.preview || !scheduleState.preview.applyToken) return;
    try {
      scheduleState.receipt = await postAdminJSON("/admin/api/schedules/apply", { applyToken: scheduleState.preview.applyToken });
      scheduleState.preview = null;
      scheduleState.editing = null;
      setScheduleDirty(false);
      await loadScheduleCenter();
    } catch (err) {
      renderError("定时群发", "schedules", err && err.message ? err.message : "定时任务操作失败");
    }
  }

  function renderFilterValue(name) {
    return escapeHTML(groupState.filters[name] || "");
  }

  function renderSavedViews(views) {
    if (!views || views.length === 0) {
      return '<span class="state-empty">暂无常用视图</span>';
    }
    var html = ['<div class="view-list">'];
    for (var i = 0; i < views.length; i++) {
      html.push(
        '<button type="button" class="ghost-btn saved-view-btn" data-view-index="' + i + '">',
        escapeHTML(views[i].name || "未命名视图"),
        '</button>'
      );
    }
    html.push('</div>');
    return html.join("");
  }

  function renderGroupRows(rows) {
    if (!rows || rows.length === 0) {
      return '<div class="state-empty">暂无匹配群组</div>';
    }
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th><input type="checkbox" id="select-all-groups"></th><th>群</th><th>类型/标签</th><th>功能状态</th><th>费率/汇率</th><th>健康</th><th>操作</th></tr></thead><tbody>'];
    for (var i = 0; i < rows.length; i++) {
      var row = rows[i];
      var id = String(row.chatId);
      var checked = groupState.selected[id] ? " checked" : "";
      var actionURL = safeAdminHref(row.actionUrl) || "/admin/group?chat_id=" + encodeURIComponent(row.chatId);
      html.push('<tr>');
      html.push('<td><input type="checkbox" class="group-select" value="' + escapeHTML(id) + '"' + checked + '></td>');
      html.push('<td><div class="group-title">' + escapeHTML(row.title || row.chatId) + '</div><div class="group-sub">' + escapeHTML(row.chatId) + '</div></td>');
      html.push('<td><div>' + escapeHTML(row.groupType || "-") + '</div><div class="group-sub">' + escapeHTML(row.groupTags || "-") + '</div></td>');
      html.push('<td><div>记账：' + escapeHTML(switchLabel(row.bookkeepingEnabled)) + '</div><div class="group-sub">自动回复：' + escapeHTML(switchLabel(row.autoReplyEnabled)) + ' / 成员欢迎：' + escapeHTML(switchLabel(row.welcomeEnabled)) + '</div></td>');
      html.push('<td><div>入款：' + escapeHTML(row.feeRate || "0") + ' / ' + escapeHTML(row.customRate || "0") + '</div><div class="group-sub">下发：' + escapeHTML(row.payoutFeeRate || "0") + ' / ' + escapeHTML(row.payoutRate || "0") + '</div></td>');
      html.push('<td><span class="' + statusClass(row.healthStatus) + '">' + escapeHTML(row.healthScore) + ' ' + escapeHTML(statusLabel(row.healthStatus)) + '</span></td>');
      html.push('<td><a class="mini-step" href="' + escapeHTML(actionURL) + '">详情</a></td>');
      html.push('</tr>');
    }
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderPreviewList(items, emptyText) {
    if (!items || items.length === 0) {
      return '<div class="state-empty">' + escapeHTML(emptyText || "暂无") + '</div>';
    }
    var html = ['<ul class="receipt-list">'];
    for (var i = 0; i < items.length; i++) {
      var item = items[i];
      html.push(
        '<li>',
        '<strong>' + escapeHTML(item.title || item.chatId) + '</strong>',
        '<span class="group-sub">' + escapeHTML(item.chatId) + '</span>',
        item.reason ? '<div>' + escapeHTML(item.reason) + '</div>' : '<div>' + escapeHTML(item.before || "-") + ' -> ' + escapeHTML(item.after || "-") + '</div>',
        '</li>'
      );
    }
    html.push('</ul>');
    return html.join("");
  }

  function renderBatchPreview(preview) {
    if (!preview) {
      return '<div class="state-empty">选择群和操作后，可以先查看批量操作预演。</div>';
    }
    return [
      '<div class="receipt-summary">',
      '<span>匹配 ' + escapeHTML(preview.matchedCount || 0) + ' 个群</span>',
      '<span>将变更 ' + escapeHTML((preview.affected || []).length) + ' 个</span>',
      '<span>跳过 ' + escapeHTML((preview.skipped || []).length) + ' 个</span>',
      '</div>',
      '<div class="receipt-columns">',
      '<div><h3>将变更</h3>' + renderPreviewList(preview.affected, "暂无变更") + '</div>',
      '<div><h3>跳过</h3>' + renderPreviewList(preview.skipped, "暂无跳过") + '</div>',
      '</div>'
    ].join("");
  }

  function renderBatchReceipt(receipt) {
    if (!receipt) {
      return '<div class="state-empty">执行后这里会显示操作结果回执。</div>';
    }
    return [
      '<div class="receipt-summary">',
      '<span>匹配 ' + escapeHTML(receipt.matchedCount || 0) + ' 个群</span>',
      '<span>成功 ' + escapeHTML(receipt.updatedCount || 0) + ' 个</span>',
      '<span>跳过 ' + escapeHTML(receipt.skippedCount || 0) + ' 个</span>',
      '<span>失败 ' + escapeHTML(receipt.failedCount || 0) + ' 个</span>',
      '</div>',
      '<div class="receipt-columns">',
      '<div><h3>成功</h3>' + renderPreviewList(receipt.updated, "暂无成功项") + '</div>',
      '<div><h3>失败/跳过</h3>' + renderPreviewList((receipt.failed || []).concat(receipt.skipped || []), "暂无失败或跳过") + '</div>',
      '</div>'
    ].join("");
  }

  function renderGroupWorkbench(data) {
    data = data || {};
    groupState.groups = data.groups || [];
    groupState.views = data.views || [];
    groupState.total = data.total || groupState.groups.length;
    if (data.filters) {
      groupState.filters = {
        q: data.filters.q || "",
        tag: data.filters.tag || "",
        type: data.filters.type || "",
        status: data.filters.status || ""
      };
    }

    var selectedCount = selectedChatIDs().length;
    var body = [
      '<section class="console-grid group-workbench">',
      '<section class="metric-strip">',
      metricCard("当前结果", formatNumber(groupState.total), "筛选后的群组"),
      metricCard("已选择", formatNumber(selectedCount), "用于批量操作"),
      metricCard("常用视图", formatNumber(groupState.views.length), "可快速切换"),
      metricCard("可批量字段", "5", "类型、标签、自动回复、欢迎、记账"),
      metricCard("执行方式", "预演后确认", "降低误操作"),
      '</section>',
      '<div id="unsaved-banner" class="unsaved-banner" style="' + (groupState.dirty ? "" : "display:none") + '">未保存：当前有筛选或批量操作草稿。</div>',
      '<section class="console-surface panel">',
      '<div class="panel-head"><h2>筛选与常用视图</h2><span class="panel-meta">先筛选，再选择群</span></div>',
      '<form id="group-filter-form" class="filter-grid">',
      '<label>关键词<input name="q" value="' + renderFilterValue("q") + '" placeholder="群名 / 群ID / 地址"></label>',
      '<label>标签<input name="tag" value="' + renderFilterValue("tag") + '" placeholder="例如：夜班"></label>',
      '<label>类型<input name="type" value="' + renderFilterValue("type") + '" placeholder="例如：业务"></label>',
      '<label>状态<select name="status">',
      '<option value=""' + (groupState.filters.status === "" ? " selected" : "") + '>全部</option>',
      '<option value="enabled"' + (groupState.filters.status === "enabled" ? " selected" : "") + '>记账开启</option>',
      '<option value="disabled"' + (groupState.filters.status === "disabled" ? " selected" : "") + '>记账关闭</option>',
      '<option value="missing_address"' + (groupState.filters.status === "missing_address" ? " selected" : "") + '>缺下发地址</option>',
      '<option value="auto_reply_off"' + (groupState.filters.status === "auto_reply_off" ? " selected" : "") + '>自动回复关闭</option>',
      '</select></label>',
      '<div class="filter-actions"><button type="submit">应用筛选</button><button type="button" class="ghost-btn" id="reset-filters-btn">重置</button></div>',
      '</form>',
      '<div class="save-view-row"><input id="view-name-input" placeholder="保存为常用视图"><button type="button" id="save-view-btn">保存视图</button></div>',
      '<div class="saved-view-row">' + renderSavedViews(groupState.views) + '</div>',
      '</section>',
      '<section class="console-surface panel panel-wide">',
      '<div class="panel-head"><h2>群组列表</h2><span class="panel-meta">' + escapeHTML(groupState.total) + '</span></div>',
      renderGroupRows(groupState.groups),
      '</section>',
      '<section class="panel-row">',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>批量操作预演</h2><span class="panel-meta">二次确认前不写库</span></div>',
      '<form id="batch-form" class="batch-form">',
      '<label>字段<select name="field">',
      '<option value="group_type">群类型</option>',
      '<option value="group_tags">群标签</option>',
      '<option value="auto_reply_enabled">自动回复开关</option>',
      '<option value="welcome_enabled">新成员欢迎开关</option>',
      '<option value="bookkeeping_enabled">记账开关</option>',
      '</select></label>',
      '<label>值<input name="value" placeholder="类型/标签，或 1/0"></label>',
      '<button type="submit">批量操作预演</button>',
      '</form>',
      renderBatchPreview(groupState.preview),
      groupState.preview && groupState.preview.applyToken ? '<button type="button" id="apply-batch-btn" class="danger-btn">确认执行预演结果</button>' : '',
      '</article>',
      '<article class="console-surface panel">',
      '<div class="panel-head"><h2>操作结果回执</h2><span class="panel-meta">执行后可核对</span></div>',
      renderBatchReceipt(groupState.receipt),
      '</article>',
      '</section>',
      '</section>'
    ].join("");

    renderShell({ title: "群组工作台", kicker: "运营总控台 / 群组工作台", activePage: "groups" }, body);
    bindGroupWorkbenchEvents();
  }

  function groupFiltersFromForm(form) {
    return {
      q: String(form.q.value || "").trim(),
      tag: String(form.tag.value || "").trim(),
      type: String(form.type.value || "").trim(),
      status: String(form.status.value || "").trim()
    };
  }

  function bindGroupWorkbenchEvents() {
    var filterForm = document.getElementById("group-filter-form");
    if (filterForm) {
      filterForm.addEventListener("input", function () { setGroupDirty(true); });
      filterForm.addEventListener("change", function () { setGroupDirty(true); });
      filterForm.addEventListener("submit", function (event) {
        event.preventDefault();
        groupState.filters = groupFiltersFromForm(filterForm);
        groupState.preview = null;
        groupState.receipt = null;
        groupState.selected = {};
        setGroupDirty(false);
        loadGroupWorkbench();
      });
    }

    var reset = document.getElementById("reset-filters-btn");
    if (reset) {
      reset.addEventListener("click", function () {
        groupState.filters = { q: "", tag: "", type: "", status: "" };
        groupState.preview = null;
        groupState.receipt = null;
        groupState.selected = {};
        setGroupDirty(false);
        loadGroupWorkbench();
      });
    }

    var save = document.getElementById("save-view-btn");
    if (save) {
      save.addEventListener("click", saveGroupView);
    }

    var saved = root.querySelectorAll(".saved-view-btn");
    for (var i = 0; i < saved.length; i++) {
      saved[i].addEventListener("click", function () {
        var index = Number(this.getAttribute("data-view-index"));
        var view = groupState.views[index];
        if (!view) {
          return;
        }
        groupState.filters = {
          q: view.filters.q || "",
          tag: view.filters.tag || "",
          type: view.filters.type || "",
          status: view.filters.status || ""
        };
        groupState.selected = {};
        groupState.preview = null;
        groupState.receipt = null;
        setGroupDirty(false);
        loadGroupWorkbench();
      });
    }

    var selectAll = document.getElementById("select-all-groups");
    if (selectAll) {
      selectAll.addEventListener("change", function () {
        for (var i = 0; i < groupState.groups.length; i++) {
          groupState.selected[String(groupState.groups[i].chatId)] = selectAll.checked;
        }
        groupState.preview = null;
        renderGroupWorkbench({
          groups: groupState.groups,
          total: groupState.total,
          views: groupState.views,
          filters: groupState.filters
        });
      });
    }

    var checks = root.querySelectorAll(".group-select");
    for (var j = 0; j < checks.length; j++) {
      checks[j].addEventListener("change", function () {
        groupState.selected[String(this.value)] = this.checked;
        groupState.preview = null;
        renderGroupWorkbench({
          groups: groupState.groups,
          total: groupState.total,
          views: groupState.views,
          filters: groupState.filters
        });
      });
    }

    var batchForm = document.getElementById("batch-form");
    if (batchForm) {
      batchForm.addEventListener("input", function () { setGroupDirty(true); });
      batchForm.addEventListener("submit", previewGroupBatch);
    }

    var apply = document.getElementById("apply-batch-btn");
    if (apply) {
      apply.addEventListener("click", applyGroupBatch);
    }
  }

  function groupQueryString() {
    var params = new URLSearchParams();
    Object.keys(groupState.filters).forEach(function (key) {
      if (groupState.filters[key]) {
        params.set(key, groupState.filters[key]);
      }
    });
    var text = params.toString();
    return text ? "?" + text : "";
  }

  async function loadGroupWorkbench() {
    renderLoading("群组工作台", "groups");
    try {
      var data = await fetchAdminJSON("/admin/api/groups" + groupQueryString());
      renderGroupWorkbench(data || {});
    } catch (err) {
      renderError("群组工作台", "groups", err && err.message ? err.message : "加载失败");
    }
  }

  async function saveGroupView() {
    var input = document.getElementById("view-name-input");
    var name = input ? String(input.value || "").trim() : "";
    if (!name) {
      renderError("群组工作台", "groups", "请输入视图名称");
      return;
    }
    try {
      await postAdminJSON("/admin/api/group-views", {
        name: name,
        filters: groupState.filters
      });
      setGroupDirty(false);
      loadGroupWorkbench();
    } catch (err) {
      renderError("群组工作台", "groups", err && err.message ? err.message : "保存视图失败");
    }
  }

  async function previewGroupBatch(event) {
    event.preventDefault();
    var form = event.target;
    var field = String(form.field.value || "").trim();
    var value = String(form.value.value || "").trim();
    try {
      groupState.preview = await postAdminJSON("/admin/api/groups/batch-preview", {
        filters: groupState.filters,
        chatIds: selectedChatIDs(),
        action: { field: field, value: value }
      });
      groupState.receipt = null;
      setGroupDirty(false);
      renderGroupWorkbench({
        groups: groupState.groups,
        total: groupState.total,
        views: groupState.views,
        filters: groupState.filters
      });
    } catch (err) {
      renderError("群组工作台", "groups", err && err.message ? err.message : "预演失败");
    }
  }

  async function applyGroupBatch() {
    if (!groupState.preview || !groupState.preview.applyToken) {
      return;
    }
    try {
      groupState.receipt = await postAdminJSON("/admin/api/groups/batch-apply", {
        applyToken: groupState.preview.applyToken
      });
      groupState.preview = null;
      setGroupDirty(false);
      await loadGroupWorkbench();
      groupState.receipt = groupState.receipt || null;
    } catch (err) {
      renderError("群组工作台", "groups", err && err.message ? err.message : "执行失败");
    }
  }

  function templateGroupLabel(group) {
    return (group.title || group.chatId) + " " + group.chatId;
  }

  function renderTemplateGroupOptions() {
    var html = ['<option value="">选择群</option>'];
    for (var i = 0; i < templateState.groups.length; i++) {
      var group = templateState.groups[i];
      html.push('<option value="' + escapeHTML(group.chatId) + '"' + (String(group.chatId) === String(templateState.chatId) ? " selected" : "") + '>' + escapeHTML(templateGroupLabel(group)) + '</option>');
    }
    return html.join("");
  }

  function renderTemplatePreview(preview, emptyText) {
    if (!preview || !preview.text) {
      return '<div class="message-preview template-preview-empty">' + escapeHTML(emptyText || "输入内容后显示预览") + '</div>';
    }
    return '<div class="message-preview template-preview-text"><div class="group-sub">' + escapeHTML(preview.parseMode || "纯文本") + '</div><pre>' + escapeHTML(preview.text) + '</pre></div>';
  }

  function renderTemplateReceipt(receipt) {
    if (!receipt) {
      return '<div class="state-empty">确认执行后显示结果回执。</div>';
    }
    return '<div class="receipt-summary"><span>匹配 ' + escapeHTML(receipt.matchedCount || 0) + ' 个</span><span>成功 ' + escapeHTML(receipt.updatedCount || 0) + ' 个</span><span>跳过 ' + escapeHTML(receipt.skippedCount || 0) + ' 个</span><span>失败 ' + escapeHTML(receipt.failedCount || 0) + ' 个</span></div>';
  }

  function renderTemplateTargets() {
    var html = ['<div class="template-target-actions"><button type="button" class="ghost-btn" id="template-select-all">全选</button><button type="button" class="ghost-btn" id="template-clear-all">清空</button></div><div class="template-target-list">'];
    for (var i = 0; i < templateState.groups.length; i++) {
      var group = templateState.groups[i];
      var id = String(group.chatId);
      html.push('<label class="template-target-item"><input type="checkbox" class="template-target-check" value="' + escapeHTML(id) + '"' + (templateState.closingSelected[id] ? ' checked' : '') + '><span><strong>' + escapeHTML(group.title || id) + '</strong><small>' + escapeHTML(id) + '</small></span></label>');
    }
    html.push('</div>');
    return html.join("");
  }

  function renderAutoReplyRows(rules) {
    if (!rules || rules.length === 0) {
      return '<div class="state-empty">暂无自动回复规则</div>';
    }
    var html = ['<div class="table-wrap"><table class="group-table template-auto-table"><thead><tr><th>关键词</th><th>回复文案</th><th>状态</th><th>操作</th></tr></thead><tbody>'];
    for (var i = 0; i < rules.length; i++) {
      var rule = rules[i];
      html.push('<tr><td>' + escapeHTML(rule.keyword || "（兜底）") + '<div class="group-sub">ID ' + escapeHTML(rule.id) + '</div></td><td class="template-rule-content">' + escapeHTML(rule.content || "") + '</td><td><span class="badge ' + (rule.enabled ? 'badge-low' : 'badge-medium') + '">' + (rule.enabled ? '开启' : '停用') + '</span></td><td><div class="table-actions"><button type="button" class="ghost-btn template-auto-action" data-auto-action="edit" data-auto-id="' + escapeHTML(rule.id) + '">编辑</button><button type="button" class="ghost-btn template-auto-action" data-auto-action="toggle" data-auto-id="' + escapeHTML(rule.id) + '">' + (rule.enabled ? '停用' : '启用') + '</button><button type="button" class="ghost-btn template-auto-action" data-auto-action="delete" data-auto-id="' + escapeHTML(rule.id) + '">删除</button></div></td></tr>');
    }
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderAutoReplyPreview() {
    var preview = templateState.autoPreview;
    if (!preview) {
      return '<div class="state-empty">选择规则操作后，这里显示预演结果。</div>';
    }
    return '<div class="receipt-summary"><span>操作：' + escapeHTML(preview.action || "") + '</span><span>规则 ID：' + escapeHTML((preview.before && preview.before.id) || (preview.after && preview.after.id) || "新建") + '</span></div>' + renderTemplatePreview(preview.preview, "无回复文案") + (preview.applyToken ? '<button type="button" id="template-auto-apply" class="danger-btn">确认自动回复变更</button>' : '');
  }

  function renderClosingPatchFields() {
    var closing = (templateState.data && templateState.data.closingReminder) || {};
    var texts = closing.texts || {};
    var textFields = [
      { key: "normal", label: "正常文案" },
      { key: "pending", label: "待确认文案" },
      { key: "noData", label: "无数据文案" },
      { key: "review", label: "复核文案" },
      { key: "failed", label: "失败文案" }
    ];
    var html = ['<div class="template-closing-fields"><label>提醒开关<select name="closing-enabled"><option value="keep">保持不变</option><option value="true">开启</option><option value="false">关闭</option></select></label><label>提醒发送时间<input name="closing-reminder-time" type="time" placeholder="保持不变"></label><label>U 差额阈值<input name="closing-threshold" type="number" min="0.000001" step="0.01" placeholder="保持不变"></label>'];
    for (var i = 0; i < textFields.length; i++) {
      var field = textFields[i];
      html.push('<label class="template-text-patch"><span><input type="checkbox" class="closing-text-toggle" data-closing-field="' + field.key + '"> 修改' + escapeHTML(field.label) + '</span><textarea data-closing-text="' + field.key + '" maxlength="2000" placeholder="勾选后可清空以恢复默认文案">' + escapeHTML(texts[field.key] || "") + '</textarea></label>');
    }
    html.push('</div>');
    return html.join("");
  }

  function renderClosingPreview() {
    var preview = templateState.closingPreview;
    if (!preview) {
      return '<div class="state-empty">选择目标群和字段后查看影响范围。</div>';
    }
    var affected = preview.affected || [];
    var failed = preview.failed || [];
    var html = ['<div class="receipt-summary"><span>匹配 ' + escapeHTML(preview.matchedCount || 0) + ' 个</span><span>将变更 ' + escapeHTML(affected.length) + ' 个</span><span>跳过 ' + escapeHTML((preview.skipped || []).length) + ' 个</span></div>'];
    if (affected.length) {
      html.push('<ul class="receipt-list">');
      for (var i = 0; i < affected.length; i++) {
        var item = affected[i];
        html.push('<li><strong>' + escapeHTML(item.title || item.chatId) + '</strong><div class="group-sub">字段：' + escapeHTML((item.changedFields || []).join("、")) + '</div><div>实际触发：' + escapeHTML(item.afterEffectiveRunAt || "-") + '（日切 ' + escapeHTML(item.dayCutTime || "-") + '）</div></li>');
      }
      html.push('</ul>');
    }
    if (failed.length) {
      html.push('<ul class="receipt-list template-failed-list">');
      for (var j = 0; j < failed.length; j++) {
        html.push('<li><strong>' + escapeHTML(failed[j].title || failed[j].chatId) + '</strong><div>' + escapeHTML(failed[j].reason || "失败") + '</div></li>');
      }
      html.push('</ul>');
    }
    if (preview.applyToken) {
      html.push('<button type="button" id="template-closing-apply" class="danger-btn">确认下课提醒变更</button>');
    }
    return html.join("");
  }

  function renderTemplatesCenter() {
    var data = templateState.data || {};
    var welcome = data.welcome || templateState.welcomeDraft;
    var auto = data.autoReply || { enabled: false, rules: [] };
    var closing = data.closingReminder || {};
    var sample = templateState.welcomeDraft.sample || {};
    var editing = templateState.autoEditing || { keyword: "", content: "" };
    var variables = data.variables || [];
    var welcomeChecked = templateState.welcomeDraft.enabled ? " checked" : "";
    var welcomeApplyHTML = templateState.welcomePreview && templateState.welcomePreview.applyToken ? '<button type="button" id="template-welcome-apply" class="danger-btn">确认欢迎语变更</button>' : "";
    var autoActionLabel = editing.id ? "预演更新" : "预演新增";
    var autoReceiptHTML = templateState.autoReceipt ? renderTemplateReceipt(templateState.autoReceipt) : "";
    var closingReceiptHTML = templateState.closingReceipt ? renderTemplateReceipt(templateState.closingReceipt) : "";
    var body = [
      '<section class="console-grid templates-center">',
      '<section class="metric-strip">',
      metricCard("当前群", templateState.chatId || "-", data.title || "选择群"),
      metricCard("成员欢迎", welcome.enabled ? "开启" : "关闭", "单群模板"),
      metricCard("自动回复规则", formatNumber((auto.rules || []).length), auto.enabled ? "总开关开启" : "总开关关闭"),
      metricCard("下课提醒", closing.enabled ? "开启" : "关闭", closing.effectiveRunAt ? "实际触发 " + closing.effectiveRunAt : "未配置"),
      metricCard("变更方式", "预演后确认", "不会自动发群"),
      '</section>',
      '<div id="template-unsaved-banner" class="unsaved-banner" style="' + (templateState.dirty ? "" : "display:none") + '">未保存：当前自动化配置尚未预演。</div>',
      '<section class="console-surface panel template-group-picker"><div class="panel-head"><h2>群与配置范围</h2><span class="panel-meta">先选群，再编辑模板</span></div><label>当前群<select id="template-group-select">' + renderTemplateGroupOptions() + '</select></label><div class="group-sub">账期日切：' + escapeHTML(closing.dayCutTime || "-") + '；提醒实际触发：' + escapeHTML(closing.effectiveRunAt || "-") + '</div></section>',
      '<section class="panel-row template-top-row">',
      '<article class="console-surface panel"><div class="panel-head"><h2>新成员欢迎语</h2><span class="panel-meta">实时预览，不会发群</span></div>',
      '<form id="template-welcome-form" class="template-form"><label class="template-inline-check"><input name="welcome-enabled" type="checkbox"' + welcomeChecked + '> 开启新成员欢迎</label><label>欢迎模板<textarea name="welcome-template" maxlength="2000">' + escapeHTML(templateState.welcomeDraft.template) + '</textarea></label><div class="template-variable-row"><span class="group-sub">变量</span>'];
    for (var v = 0; v < variables.length; v++) {
      body.push('<button type="button" class="ghost-btn template-variable-btn" data-token="' + escapeHTML(variables[v].token) + '">' + escapeHTML(variables[v].label || variables[v].token) + '</button>');
    }
    body.push('</div><div class="template-sample-grid"><label>示例用户 ID<input name="welcome-user-id" value="' + escapeHTML(sample.userId || 10001) + '"></label><label>示例名字<input name="welcome-first-name" value="' + escapeHTML(sample.firstName || "新成员") + '"></label></div><div class="template-action-row"><button type="button" id="template-welcome-render">实时预览</button><button type="submit">操作预演</button></div></form>', '<div id="template-welcome-live-preview">' + renderTemplatePreview(templateState.welcomeRender && templateState.welcomeRender.preview, "输入欢迎模板后显示实时预览") + '</div>', welcomeApplyHTML, '</article>');
    body.push(
      '<article class="console-surface panel"><div class="panel-head"><h2>自动回复</h2><span class="panel-meta">按规则 ID 管理</span></div>',
      '<form id="template-auto-form" class="template-form"><input type="hidden" name="rule-id" value="' + escapeHTML(editing.id || "") + '"><label>关键词（可为空作为兜底）<input name="auto-keyword" maxlength="64" value="' + escapeHTML(editing.keyword || "") + '"></label><label>回复文案<textarea name="auto-content" maxlength="2000">' + escapeHTML(editing.content || "") + '</textarea></label><div class="template-action-row"><button type="submit">' + autoActionLabel + '</button><button type="button" class="ghost-btn" id="template-auto-new">清空</button></div></form>', renderAutoReplyRows(auto.rules), '<div class="template-preview-block">' + renderAutoReplyPreview() + '</div>', autoReceiptHTML, '</article>',
      '</section>',
      '<section class="console-surface panel panel-wide template-closing-panel"><div class="panel-head"><h2>下课提醒批量配置</h2><span class="panel-meta">日切时间只读，不随批量变更</span></div><div class="template-closing-layout"><div><h3>目标群</h3>' + renderTemplateTargets() + '</div><form id="template-closing-form" class="template-form"><div class="group-sub">已选 ' + escapeHTML(Object.keys(templateState.closingSelected).filter(function (key) { return templateState.closingSelected[key]; }).length) + ' 个群</div>' + renderClosingPatchFields() + '<div class="template-action-row"><button type="submit">下课提醒预演</button></div></form></div><div class="template-preview-block">' + renderClosingPreview() + '</div>' + closingReceiptHTML + '</section>',
      '</section>'
    );
    body = body.join("");
    renderShell({ title: "自动化管理", kicker: "运营总控台 / 自动化管理", activePage: "templates" }, body);
    bindTemplatesCenterEvents();
  }

  function templateWelcomeRequest(form) {
    var userID = Number(form["welcome-user-id"].value || 10001);
    if (!isFinite(userID) || userID === 0) userID = 10001;
    return {
      kind: "welcome",
      chatId: Number(templateState.chatId),
      enabled: !!form["welcome-enabled"].checked,
      template: String(form["welcome-template"].value || ""),
      sample: { userId: userID, firstName: String(form["welcome-first-name"].value || "新成员") }
    };
  }

  function syncWelcomeDraft(form) {
    var request = templateWelcomeRequest(form);
    templateState.welcomeDraft = { enabled: request.enabled, template: request.template, sample: request.sample };
    return request;
  }

  function scheduleWelcomeRender() {
    if (templateState.welcomeRenderTimer) clearTimeout(templateState.welcomeRenderTimer);
    templateState.welcomeRenderTimer = setTimeout(async function () {
      var form = document.getElementById("template-welcome-form");
      if (!form) return;
      var request = syncWelcomeDraft(form);
      try {
        templateState.welcomeRender = await postAdminJSON("/admin/api/templates/render", request);
        var node = document.getElementById("template-welcome-live-preview");
        if (node) node.innerHTML = renderTemplatePreview(templateState.welcomeRender.preview, "输入欢迎模板后显示实时预览");
      } catch (err) {
        var nodeError = document.getElementById("template-welcome-live-preview");
        if (nodeError) nodeError.innerHTML = '<div class="state-error">' + escapeHTML(err && err.message ? err.message : "预览失败") + '</div>';
      }
    }, 260);
  }

  async function previewWelcomeTemplate(event) {
    event.preventDefault();
    var form = event.target;
    var request = syncWelcomeDraft(form);
    try {
      templateState.welcomePreview = await postAdminJSON("/admin/api/templates/preview", request);
      setTemplateDirty(false);
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "欢迎语预演失败");
    }
  }

  async function applyTemplateToken(kind) {
    var state = kind === "welcome" ? templateState.welcomePreview : (kind === "auto" ? templateState.autoPreview : templateState.closingPreview);
    if (!state || !state.applyToken) return;
    try {
      var receipt = await postAdminJSON("/admin/api/templates/apply", { applyToken: state.applyToken });
      if (kind === "welcome") templateState.welcomePreview = null;
      if (kind === "auto") { templateState.autoReceipt = receipt; templateState.autoPreview = null; templateState.autoEditing = null; }
      if (kind === "closing") { templateState.closingReceipt = receipt; templateState.closingPreview = null; }
      setTemplateDirty(false);
      await loadTemplatesCenter(true);
      if (kind === "auto") templateState.autoReceipt = receipt;
      if (kind === "closing") templateState.closingReceipt = receipt;
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "执行失败");
    }
  }

  async function previewAutoReplyForm(event) {
    event.preventDefault();
    var form = event.target;
    var ruleID = Number(form["rule-id"].value || 0);
    var body = { action: ruleID ? "update" : "create", chatId: Number(templateState.chatId), ruleId: ruleID || undefined, keyword: String(form["auto-keyword"].value || ""), content: String(form["auto-content"].value || "") };
    try {
      templateState.autoPreview = await postAdminJSON("/admin/api/templates/auto-replies/preview", body);
      templateState.autoReceipt = null;
      setTemplateDirty(false);
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "自动回复预演失败");
    }
  }

  async function previewAutoReplyAction(action, rule) {
    if (!rule) return;
    try {
      templateState.autoPreview = await postAdminJSON("/admin/api/templates/auto-replies/preview", { action: action, chatId: Number(templateState.chatId), ruleId: Number(rule.id), enabled: action === "toggle" ? !rule.enabled : undefined });
      templateState.autoReceipt = null;
      setTemplateStateDirty(false);
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "自动回复预演失败");
    }
  }

  function setTemplateStateDirty(value) {
    setTemplateDirty(value);
  }

  function closingPatchFromForm(form) {
    var patch = { texts: {} };
    var enabled = String(form["closing-enabled"].value || "keep");
    if (enabled === "true" || enabled === "false") patch.enabled = enabled === "true";
    var reminderTime = String(form["closing-reminder-time"].value || "").trim();
    if (reminderTime) patch.reminderTime = reminderTime;
    var threshold = String(form["closing-threshold"].value || "").trim();
    if (threshold) patch.thresholdU = Number(threshold);
    var toggles = form.querySelectorAll(".closing-text-toggle");
    for (var i = 0; i < toggles.length; i++) {
      if (!toggles[i].checked) continue;
      var key = toggles[i].getAttribute("data-closing-field");
      var input = form.querySelector('[data-closing-text="' + key + '"]');
      patch.texts[key] = input ? String(input.value || "") : "";
    }
    if (Object.keys(patch.texts).length === 0) delete patch.texts;
    return { chatIds: Object.keys(templateState.closingSelected).filter(function (key) { return templateState.closingSelected[key]; }).map(function (key) { return Number(key); }), patch: patch };
  }

  async function previewClosingReminder(event) {
    event.preventDefault();
    var form = event.target;
    var request = closingPatchFromForm(form);
    if (!request.chatIds.length) {
      renderError("自动化管理", "templates", "请选择至少一个下课提醒目标群");
      return;
    }
    try {
      templateState.closingPreview = await postAdminJSON("/admin/api/templates/closing-reminders/preview", request);
      templateState.closingReceipt = null;
      setTemplateDirty(false);
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "下课提醒预演失败");
    }
  }

  function bindTemplatesCenterEvents() {
    var select = document.getElementById("template-group-select");
    if (select) select.addEventListener("change", function () {
      var chatID = String(this.value || "");
      history.pushState({}, "", chatID ? "/admin/templates?chat_id=" + encodeURIComponent(chatID) : "/admin/templates");
      loadTemplatesCenter();
    });
    var welcomeForm = document.getElementById("template-welcome-form");
    if (welcomeForm) {
      welcomeForm.addEventListener("input", function () { syncWelcomeDraft(welcomeForm); setTemplateDirty(true); scheduleWelcomeRender(); });
      welcomeForm.addEventListener("change", function () { syncWelcomeDraft(welcomeForm); setTemplateDirty(true); scheduleWelcomeRender(); });
      welcomeForm.addEventListener("submit", previewWelcomeTemplate);
    }
    var welcomeRender = document.getElementById("template-welcome-render");
    if (welcomeRender) welcomeRender.addEventListener("click", scheduleWelcomeRender);
    var variableButtons = root.querySelectorAll(".template-variable-btn");
    for (var i = 0; i < variableButtons.length; i++) variableButtons[i].addEventListener("click", function () {
      var textarea = document.querySelector('#template-welcome-form textarea[name="welcome-template"]');
      if (!textarea) return;
      var token = this.getAttribute("data-token") || "";
      var start = textarea.selectionStart || textarea.value.length;
      var end = textarea.selectionEnd || start;
      textarea.value = textarea.value.slice(0, start) + token + textarea.value.slice(end);
      textarea.focus();
      textarea.selectionStart = textarea.selectionEnd = start + token.length;
      syncWelcomeDraft(welcomeForm); setTemplateDirty(true); scheduleWelcomeRender();
    });
    var welcomeApply = document.getElementById("template-welcome-apply");
    if (welcomeApply) welcomeApply.addEventListener("click", function () { applyTemplateToken("welcome"); });

    var autoForm = document.getElementById("template-auto-form");
    if (autoForm) { autoForm.addEventListener("input", function () { setTemplateDirty(true); }); autoForm.addEventListener("submit", previewAutoReplyForm); }
    var autoNew = document.getElementById("template-auto-new");
    if (autoNew) autoNew.addEventListener("click", function () { templateState.autoEditing = null; templateState.autoPreview = null; renderTemplatesCenter(); });
    var autoActions = root.querySelectorAll(".template-auto-action");
    for (var j = 0; j < autoActions.length; j++) autoActions[j].addEventListener("click", function () {
      var id = Number(this.getAttribute("data-auto-id"));
      var action = this.getAttribute("data-auto-action");
      var rules = (templateState.data && templateState.data.autoReply && templateState.data.autoReply.rules) || [];
      var rule = rules.find(function (item) { return Number(item.id) === id; });
      if (action === "edit") { templateState.autoEditing = rule || null; renderTemplatesCenter(); return; }
      previewAutoReplyAction(action, rule);
    });
    var autoApply = document.getElementById("template-auto-apply");
    if (autoApply) autoApply.addEventListener("click", function () { applyTemplateToken("auto"); });

    var targetChecks = root.querySelectorAll(".template-target-check");
    for (var k = 0; k < targetChecks.length; k++) targetChecks[k].addEventListener("change", function () { templateState.closingSelected[String(this.value)] = this.checked; setTemplateDirty(true); });
    var selectAll = document.getElementById("template-select-all");
    if (selectAll) selectAll.addEventListener("click", function () { for (var i = 0; i < templateState.groups.length; i++) templateState.closingSelected[String(templateState.groups[i].chatId)] = true; renderTemplatesCenter(); });
    var clearAll = document.getElementById("template-clear-all");
    if (clearAll) clearAll.addEventListener("click", function () { templateState.closingSelected = {}; renderTemplatesCenter(); });
    var closingForm = document.getElementById("template-closing-form");
    if (closingForm) { closingForm.addEventListener("input", function () { setTemplateDirty(true); }); closingForm.addEventListener("change", function () { setTemplateDirty(true); }); closingForm.addEventListener("submit", previewClosingReminder); }
    var closingApply = document.getElementById("template-closing-apply");
    if (closingApply) closingApply.addEventListener("click", function () { applyTemplateToken("closing"); });
  }

  async function loadTemplatesCenter(preserveReceipt) {
    renderLoading("自动化管理", "templates");
    try {
      var groupsData = await fetchAdminJSON("/admin/api/groups");
      templateState.groups = groupsData.groups || [];
      var params = new URLSearchParams(window.location.search || "");
      var requested = String(params.get("chat_id") || templateState.chatId || "");
      if (!requested || !templateState.groups.some(function (group) { return String(group.chatId) === requested; })) requested = templateState.groups.length ? String(templateState.groups[0].chatId) : "";
      templateState.chatId = requested;
      if (!requested) { templateState.data = { welcome: {}, autoReply: { enabled: false, rules: [] }, closingReminder: {} }; renderTemplatesCenter(); return; }
      templateState.data = await fetchAdminJSON("/admin/api/templates?chat_id=" + encodeURIComponent(requested));
      templateState.welcomeDraft = { enabled: !!(templateState.data.welcome && templateState.data.welcome.enabled), template: (templateState.data.welcome && templateState.data.welcome.template) || "", sample: { userId: 10001, firstName: "新成员", username: "" } };
      templateState.welcomeRender = null;
      if (!preserveReceipt) { templateState.autoReceipt = null; templateState.closingReceipt = null; }
      if (!Object.keys(templateState.closingSelected).length) templateState.closingSelected[requested] = true;
      templateState.dirty = false;
      renderTemplatesCenter();
    } catch (err) {
      renderError("自动化管理", "templates", err && err.message ? err.message : "加载失败");
    }
  }

  function parseJSONText(value, fallback) {
    try {
      return JSON.parse(String(value || ""));
    } catch (err) {
      return fallback;
    }
  }

  function renderSnapshotRows(items) {
    if (!items || !items.length) return '<div class="state-empty">暂无变更快照</div>';
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th>动作</th><th>管理员</th><th>状态</th><th>时间</th><th>群明细</th></tr></thead><tbody>'];
    items.forEach(function (item) {
      var summary = parseJSONText(item.summaryJson, {});
      var failed = summary.failedCount || 0;
      html.push('<tr><td><strong>' + escapeHTML(item.action || "-") + '</strong><div class="group-sub">快照 #' + escapeHTML(item.id) + '</div></td><td>' + escapeHTML(item.ownerUsername || "-") + '</td><td><span class="' + statusClass(item.status === "completed" ? "healthy" : (item.status === "partial" ? "attention" : "risk")) + '">' + escapeHTML(item.status || "-") + '</span><div class="group-sub">失败 ' + escapeHTML(failed) + '</div></td><td>' + escapeHTML(item.createdAt || "-") + '</td><td><ul class="reason-list">' + (item.items || []).map(function (detail) { return '<li>' + escapeHTML(detail.chatId) + '：' + escapeHTML(detail.status || "-") + (detail.failureReason ? '，' + escapeHTML(detail.failureReason) : '') + '</li>'; }).join("") + '</ul></td></tr>');
    });
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderSnapshotsCenter(data) {
    closeoutState.snapshots = data.items || [];
    var items = closeoutState.snapshots;
    var body = ['<section class="console-grid closeout-center">', '<section class="metric-strip">', metricCard("快照总数", formatNumber(items.length), "当前单管理员"), metricCard("最近动作", items.length ? items[0].action : "-", "倒序记录"), metricCard("失败明细", formatNumber(items.reduce(function (sum, item) { return sum + (item.items || []).filter(function (detail) { return detail.status === "failed"; }).length; }, 0)), "可追溯"), '</section>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>变更快照</h2><span class="panel-meta">只读审计记录</span></div>', renderSnapshotRows(items), '</section>', '</section>'];
    renderShell({ title: "变更快照", kicker: "收尾中心 / 变更快照", activePage: "snapshots" }, body.join(""));
  }

  function templateKindLabel(kind) {
    if (kind === "welcome") return "欢迎语";
    if (kind === "auto_reply") return "自动回复";
    if (kind === "closing_reminder") return "下课提醒";
    if (kind === "broadcast") return "群发";
    return kind || "-";
  }

  function renderTemplateLibraryRows(items) {
    if (!items || !items.length) return '<div class="state-empty">暂无模板版本</div>';
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th>模板</th><th>类型</th><th>版本</th><th>说明</th><th>创建人</th><th>操作</th></tr></thead><tbody>'];
    items.forEach(function (item) {
      html.push('<tr><td><strong>' + escapeHTML(item.name || "-") + '</strong><div class="group-sub">ID ' + escapeHTML(item.id) + '</div></td><td>' + escapeHTML(templateKindLabel(item.kind)) + '</td><td>v' + escapeHTML(item.version) + '</td><td class="template-rule-content">' + escapeHTML(item.description || "-") + '</td><td>' + escapeHTML(item.ownerUsername || "-") + '</td><td><button type="button" class="ghost-btn template-library-preview" data-template-id="' + escapeHTML(item.id) + '">预览</button></td></tr>');
    });
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderTemplateLibraryCenter(data) {
    closeoutState.library = data.items || [];
    var items = closeoutState.library;
    var body = ['<section class="console-grid closeout-center">', '<section class="metric-strip">', metricCard("模板版本", formatNumber(items.length), "不可覆盖旧版本"), metricCard("模板类型", formatNumber(items.filter(function (item, index, array) { return array.findIndex(function (other) { return other.kind === item.kind; }) === index; }).length), "当前允许类型"), metricCard("最新版本", items.length ? "v" + items[0].version : "-", "按记录倒序"), '</section>', '<div id="closeout-unsaved-banner" class="unsaved-banner" style="' + (closeoutState.dirty ? "" : "display:none") + '">未保存：当前模板预览草稿尚未提交。</div>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>模板库</h2><span class="panel-meta">保存后生成新版本</span></div>', renderTemplateLibraryRows(items), '</section>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>模板预览</h2><span class="panel-meta">只读，不写入群配置</span></div><form id="template-library-preview-form" class="filter-grid"><label>模板版本<select name="templateId">' + (items.length ? items.map(function (item) { return '<option value="' + escapeHTML(item.id) + '">' + escapeHTML(item.name + " v" + item.version) + '</option>'; }).join("") : '<option value="">暂无模板</option>') + '</select></label><label>群 ID<input name="chatId" type="number" placeholder="目标群 ID"></label><label>示例名字<input name="firstName" value="新成员"></label><div class="filter-actions"><button type="submit"' + (items.length ? "" : " disabled") + '>模板预览</button></div></form><div class="template-preview-block">' + (closeoutState.libraryPreview ? renderTemplatePreview(closeoutState.libraryPreview.preview, "暂无预览") : '<div class="state-empty">选择版本和群后显示预览。</div>') + '</div></section>', '</section>'];
    renderShell({ title: "模板库", kicker: "收尾中心 / 模板库", activePage: "template-library" }, body.join(""));
    bindTemplateLibraryEvents();
  }

  function bindTemplateLibraryEvents() {
    var form = document.getElementById("template-library-preview-form");
    if (form) {
      form.addEventListener("input", function () { setCloseoutDirty(true); });
      form.addEventListener("change", function () { setCloseoutDirty(true); });
      form.addEventListener("submit", async function (event) {
        event.preventDefault();
        try {
          closeoutState.libraryPreview = await postAdminJSON("/admin/api/template-library/preview", { templateId: Number(form.templateId.value), chatId: Number(form.chatId.value), sample: { firstName: String(form.firstName.value || "新成员") } });
          setCloseoutDirty(false);
          renderTemplateLibraryCenter({ items: closeoutState.library });
        } catch (err) {
          renderError("模板库", "template-library", err && err.message ? err.message : "模板预览失败");
        }
      });
    }
    var buttons = root.querySelectorAll(".template-library-preview");
    for (var i = 0; i < buttons.length; i++) buttons[i].addEventListener("click", function () {
      var formNode = document.getElementById("template-library-preview-form");
      if (formNode) formNode.templateId.value = this.getAttribute("data-template-id") || "";
      if (formNode) formNode.scrollIntoView({ behavior: "smooth", block: "center" });
    });
  }

  function renderRiskRadarCenter(data) {
    closeoutState.risk = data.items || [];
    var items = closeoutState.risk;
    var body = ['<section class="console-grid closeout-center">', '<section class="metric-strip">', metricCard("风险项", formatNumber(items.length), "只读扫描"), metricCard("高风险", formatNumber(items.filter(function (item) { return item.severity === "high"; }).length), "优先处理"), metricCard("中风险", formatNumber(items.filter(function (item) { return item.severity === "medium" || item.severity === "normal"; }).length), "持续关注"), '</section>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>权限风险雷达</h2><span class="panel-meta">只读，不自动修改权限</span></div>', renderActionList(items, "暂无权限风险"), '</section>', '</section>'];
    renderShell({ title: "权限风险雷达", kicker: "收尾中心 / 权限风险雷达", activePage: "risk-radar" }, body.join(""));
  }

  function renderInsightsCenter(data) {
    closeoutState.insights = data || { stats: {}, recommendations: [] };
    var stats = closeoutState.insights.stats || {};
    var body = ['<section class="console-grid closeout-center">', '<section class="metric-strip">', metricCard("群总数", formatNumber(stats.totalGroups), "当前管理范围"), metricCard("健康群", formatNumber(stats.healthyGroups), "配置完整"), metricCard("平均健康分", formatNumber(stats.averageHealthScore), "可解释评分"), metricCard("待发送任务", formatNumber(stats.pendingSchedules), "定时任务"), metricCard("失败任务", formatNumber(stats.failedTasks), "需要复核"), '</section>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>运营建议</h2><span class="panel-meta">只读建议，不自动执行</span></div>', renderActionList(closeoutState.insights.recommendations || [], "暂无运营建议"), '</section>', '</section>'];
    renderShell({ title: "运营洞察", kicker: "收尾中心 / 运营洞察", activePage: "insights" }, body.join(""));
  }

  function renderRetryRows(items) {
    if (!items || !items.length) return '<div class="state-empty">暂无失败项</div>';
    var html = ['<div class="table-wrap"><table class="group-table"><thead><tr><th></th><th>失败类型</th><th>群</th><th>原因</th><th>尝试次数</th><th>时间</th></tr></thead><tbody>'];
    items.forEach(function (item) {
      var checked = closeoutState.selectedRetries[String(item.id)] ? " checked" : "";
      html.push('<tr><td><input type="checkbox" class="retry-item-check" value="' + escapeHTML(item.id) + '"' + checked + '></td><td>' + escapeHTML(item.kind || "-") + '<div class="group-sub">' + escapeHTML(item.operationId || "-") + '</div></td><td>' + escapeHTML(item.chatId || "-") + '</td><td>' + escapeHTML(item.failureReason || "-") + '</td><td>' + escapeHTML(item.attemptCount || 0) + '</td><td>' + escapeHTML(item.updatedAt || item.createdAt || "-") + '</td></tr>');
    });
    html.push('</tbody></table></div>');
    return html.join("");
  }

  function renderRetryPreview(preview) {
    if (!preview) return '<div class="state-empty">选择失败项后先预演。</div>';
    var html = ['<div class="receipt-summary"><span>影响 ' + escapeHTML((preview.affected || []).length) + ' 项</span><span>跳过 ' + escapeHTML((preview.skipped || []).length) + ' 项</span><span>失败 ' + escapeHTML((preview.failed || []).length) + ' 项</span></div>'];
    if (preview.failed && preview.failed.length) html.push('<ul class="reason-list">' + preview.failed.map(function (item) { return '<li>' + escapeHTML(item.id) + '：' + escapeHTML(item.reason || "失败") + '</li>'; }).join("") + '</ul>');
    if (preview.applyToken) html.push('<button type="button" id="retry-apply-btn" class="danger-btn">确认重试失败项</button>');
    return html.join("");
  }

  function renderRetryCenter(data) {
    closeoutState.retries = data.items || [];
    var items = closeoutState.retries;
    var body = ['<section class="console-grid closeout-center">', '<section class="metric-strip">', metricCard("失败项", formatNumber(items.length), "仅显示 failed"), metricCard("已选", formatNumber(Object.keys(closeoutState.selectedRetries).filter(function (key) { return closeoutState.selectedRetries[key]; }).length), "待预演"), metricCard("重试方式", "预演后确认", "单管理员令牌"), '</section>', '<div id="closeout-unsaved-banner" class="unsaved-banner" style="' + (closeoutState.dirty ? "" : "display:none") + '">未保存：当前失败项选择尚未预演。</div>', '<section class="console-surface panel panel-wide"><div class="panel-head"><h2>失败重试中心</h2><span class="panel-meta">配置冲突会保留失败原因</span></div>', renderRetryRows(items), '<div class="template-action-row"><button type="button" id="retry-preview-btn">失败项预演</button><button type="button" class="ghost-btn" id="retry-clear-btn">清空选择</button></div><div class="template-preview-block">' + renderRetryPreview(closeoutState.retryPreview) + '</div>' + (closeoutState.retryReceipt ? renderTemplateReceipt(closeoutState.retryReceipt) : '') + '</section>', '</section>'];
    renderShell({ title: "失败重试中心", kicker: "收尾中心 / 失败重试中心", activePage: "retries" }, body.join(""));
    bindRetryCenterEvents();
  }

  function bindRetryCenterEvents() {
    var checks = root.querySelectorAll(".retry-item-check");
    for (var i = 0; i < checks.length; i++) checks[i].addEventListener("change", function () { closeoutState.selectedRetries[String(this.value)] = this.checked; closeoutState.retryPreview = null; setCloseoutDirty(true); });
    var previewButton = document.getElementById("retry-preview-btn");
    if (previewButton) previewButton.addEventListener("click", async function () {
      var ids = Object.keys(closeoutState.selectedRetries).filter(function (key) { return closeoutState.selectedRetries[key]; }).map(Number);
      if (!ids.length) return;
      try {
        closeoutState.retryPreview = await postAdminJSON("/admin/api/retries/preview", { ids: ids });
        setCloseoutDirty(false);
        renderRetryCenter({ items: closeoutState.retries });
      } catch (err) { renderError("失败重试中心", "retries", err && err.message ? err.message : "重试预演失败"); }
    });
    var clearButton = document.getElementById("retry-clear-btn");
    if (clearButton) clearButton.addEventListener("click", function () { closeoutState.selectedRetries = {}; closeoutState.retryPreview = null; setCloseoutDirty(false); renderRetryCenter({ items: closeoutState.retries }); });
    var applyButton = document.getElementById("retry-apply-btn");
    if (applyButton) applyButton.addEventListener("click", async function () {
      if (!closeoutState.retryPreview || !closeoutState.retryPreview.applyToken) return;
      try {
        closeoutState.retryReceipt = await postAdminJSON("/admin/api/retries/apply", { applyToken: closeoutState.retryPreview.applyToken });
        closeoutState.retryPreview = null;
        closeoutState.selectedRetries = {};
        setCloseoutDirty(false);
        await loadRetryCenter(true);
      } catch (err) { renderError("失败重试中心", "retries", err && err.message ? err.message : "重试执行失败"); }
    });
  }

  async function loadSnapshotsCenter() {
    renderLoading("变更快照", "snapshots");
    try { renderSnapshotsCenter(await fetchAdminJSON("/admin/api/snapshots")); } catch (err) { renderError("变更快照", "snapshots", err && err.message ? err.message : "加载失败"); }
  }

  async function loadTemplateLibraryCenter() {
    renderLoading("模板库", "template-library");
    try { renderTemplateLibraryCenter(await fetchAdminJSON("/admin/api/template-library")); } catch (err) { renderError("模板库", "template-library", err && err.message ? err.message : "加载失败"); }
  }

  async function loadRiskRadarCenter() {
    renderLoading("权限风险雷达", "risk-radar");
    try { renderRiskRadarCenter(await fetchAdminJSON("/admin/api/risk-radar")); } catch (err) { renderError("权限风险雷达", "risk-radar", err && err.message ? err.message : "加载失败"); }
  }

  async function loadInsightsCenter() {
    renderLoading("运营洞察", "insights");
    try { renderInsightsCenter(await fetchAdminJSON("/admin/api/insights")); } catch (err) { renderError("运营洞察", "insights", err && err.message ? err.message : "加载失败"); }
  }

  async function loadRetryCenter(preserveReceipt) {
    renderLoading("失败重试中心", "retries");
    try { var data = await fetchAdminJSON("/admin/api/retries?status=failed"); if (!preserveReceipt) closeoutState.retryReceipt = null; renderRetryCenter(data); } catch (err) { renderError("失败重试中心", "retries", err && err.message ? err.message : "加载失败"); }
  }

  function renderSetupPage() {
    root.innerHTML = '<main class="setup-page"><section class="setup-panel"><div class="page-kicker">后台安全</div><h1>首次安全初始化</h1><p class="muted">使用预置初始化口令绑定 Google Authenticator。</p><form id="setup-start-form" class="setup-form"><label>初始化口令<input name="setupToken" type="password" autocomplete="off" required></label><label>管理员账号<input name="username" autocomplete="username" required></label><label>管理员密码<input name="password" type="password" autocomplete="new-password" required></label><button type="submit">开始初始化</button></form><section id="setup-confirm" class="setup-confirm" hidden><h2>扫码确认</h2><img id="setup-qr" class="setup-qr" alt="TOTP 二维码"><form id="setup-complete-form" class="setup-form"><label>验证码<input name="totp" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required></label><button type="submit">完成绑定</button></form></section><div id="setup-message" class="state-message" role="status">' + escapeHTML(setupState.message || "") + '</div></section></main>';
    var startForm = document.getElementById("setup-start-form");
    var confirm = document.getElementById("setup-confirm");
    var message = document.getElementById("setup-message");
    if (setupState.started && confirm) { confirm.hidden = false; var qr = document.getElementById("setup-qr"); if (qr) qr.src = "/admin/api/setup/qr"; }
    if (startForm) startForm.addEventListener("submit", async function (event) {
      event.preventDefault(); message.textContent = "正在建立初始化会话...";
      try {
        var resp = await fetch("/admin/api/setup/start", { method: "POST", credentials: "same-origin", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: JSON.stringify({ setupToken: String(startForm.setupToken.value || ""), username: String(startForm.username.value || ""), password: String(startForm.password.value || "") }) });
        var data = await resp.json(); if (!resp.ok || !data.ok) throw new Error(data.error || "初始化口令无效");
        setupState.started = true; setupState.csrfToken = String((data.data && data.data.csrfToken) || ""); setupState.message = "请扫码后输入当前验证码。"; renderSetupPage();
      } catch (err) { setupState.message = err && err.message ? err.message : "初始化失败"; renderSetupPage(); }
    });
    var completeForm = document.getElementById("setup-complete-form");
    if (completeForm) completeForm.addEventListener("submit", async function (event) {
      event.preventDefault(); message.textContent = "正在确认验证码...";
      try {
        var completeResp = await fetch("/admin/api/setup/complete", { method: "POST", credentials: "same-origin", headers: { Accept: "application/json", "Content-Type": "application/json", "X-CSRF-Token": setupState.csrfToken }, body: JSON.stringify({ totp: String(completeForm.totp.value || "") }) });
        var completeData = await completeResp.json(); if (!completeResp.ok || !completeData.ok) throw new Error(completeData.error || "验证码无效");
        setupState.started = false; setupState.message = "初始化完成，请返回后台登录。"; renderSetupPage();
      } catch (err) { setupState.message = err && err.message ? err.message : "确认失败"; renderSetupPage(); }
    });
  }

  function formatSecurityTime(value) {
    if (!value) return "长期有效";
    var parsed = new Date(value); return isNaN(parsed.getTime()) ? String(value) : parsed.toLocaleString();
  }

  function renderSecuritySessions(items) {
    if (!items || !items.length) return '<div class="state-empty">暂无设备会话</div>';
    var html = ['<div class="table-wrap"><table class="group-table security-session-table"><thead><tr><th>设备</th><th>角色</th><th>最近活动</th><th>过期时间</th><th>操作</th></tr></thead><tbody>'];
    items.forEach(function (item) {
      var role = item.isPrimary ? '<span class="badge badge-low">主设备</span>' : '<span class="badge badge-medium">次设备</span>';
      var current = item.isCurrent ? '<span class="badge badge-low">当前</span>' : '';
      var actions = '';
      if (!item.isPrimary && securityState.reauthScopes.sessions) actions = '<input class="session-expiry-input" data-handle="' + escapeHTML(item.sessionHandle) + '" type="datetime-local"><button type="button" class="ghost-btn session-expiry-btn" data-handle="' + escapeHTML(item.sessionHandle) + '">更新过期</button><button type="button" class="danger-btn session-revoke-btn" data-handle="' + escapeHTML(item.sessionHandle) + '">撤销</button>';
      html.push('<tr><td><strong>' + escapeHTML(item.userAgent || "未知设备") + '</strong><div class="group-sub">' + escapeHTML(item.remoteAddr || "未知地址") + '</div></td><td>' + role + ' ' + current + '</td><td>' + escapeHTML(formatSecurityTime(item.lastSeenAt)) + '</td><td>' + escapeHTML(formatSecurityTime(item.expiresAt)) + '</td><td>' + actions + '</td></tr>');
    });
    html.push('</tbody></table></div>'); return html.join("");
  }

  function renderDeviceBindingPanel(primary) {
    var binding = securityState.binding;
    if (!primary || !securityState.reauthScopes.sessions) return '';
    if (!binding.active) return '<section class="console-surface panel"><div class="panel-head"><h2>绑定新设备</h2><span class="panel-meta">主设备确认后才会登录</span></div><p class="muted">在新手机或电脑浏览器扫码，主设备确认后会创建次设备会话。</p><button type="button" id="security-start-binding">生成绑定二维码</button></section>';
    var pending = binding.pending || [];
    var rows = pending.length ? pending.map(function (item) {
      var action = item.status === "claimed" ? '<button type="button" class="ghost-btn binding-approve-btn" data-binding-id="' + escapeHTML(item.bindingId) + '">允许绑定</button><button type="button" class="danger-btn binding-reject-btn" data-binding-id="' + escapeHTML(item.bindingId) + '">拒绝</button>' : '<span class="badge badge-medium">' + escapeHTML(item.status || "等待") + '</span>';
      return '<div class="device-binding-pending"><div><strong>' + escapeHTML(item.device || "未知设备") + '</strong><small>' + escapeHTML(item.remoteAddr || "未知地址") + ' · ' + escapeHTML(formatSecurityTime(item.claimedAt)) + '</small></div><div>' + action + '</div></div>';
    }).join('') : '<div class="state-empty">等待新设备扫码...</div>';
    return '<section class="console-surface panel device-binding-panel"><div class="panel-head"><h2>绑定新设备</h2><span class="panel-meta">有效期至 ' + escapeHTML(formatSecurityTime(binding.expiresAt)) + '</span></div><div class="device-binding-layout"><img class="setup-qr" src="' + escapeHTML(binding.imageURL) + '" alt="新设备绑定二维码"><div><p class="muted">请用新手机或电脑扫码，主设备看到设备信息后点击允许。</p><button type="button" id="security-cancel-binding" class="ghost-btn">取消绑定</button></div></div><div class="device-binding-pending-list">' + rows + '</div></section>';
  }

  function renderSecurityPage() {
    var primary = securityState.sessions.some(function (item) { return item.isPrimary && item.isCurrent; });
    var body = '<section class="console-grid security-page"><section class="console-surface panel"><div class="panel-head"><h2>安全设置</h2><span class="panel-meta">单管理员账号</span></div><form id="security-reauth-form" class="filter-grid"><label>重新认证用途<select name="action"><option value="sessions">设备会话管理</option><option value="qr">查看 TOTP 二维码</option></select></label><label>Google Authenticator 验证码<input name="totp" inputmode="numeric" maxlength="6" autocomplete="one-time-code" required></label><div class="filter-actions"><button type="submit">重新认证</button></div></form><div id="security-message" class="state-message">' + escapeHTML(securityState.message || "") + '</div>' + (securityState.qrLoaded ? '<img class="setup-qr" src="/admin/api/security/qr" alt="TOTP 二维码">' : '') + '</section>' + renderDeviceBindingPanel(primary) + '<section class="console-surface panel"><div class="panel-head"><h2>设备会话</h2><span class="panel-meta">主设备可管理次设备</span></div>' + renderSecuritySessions(securityState.sessions) + '<div class="template-action-row">' + (primary && securityState.reauthScopes.sessions ? '<button type="button" id="security-revoke-all" class="danger-btn">退出其他设备</button><button type="button" id="security-transfer-primary" class="ghost-btn">转移主设备</button>' : '') + '<button type="button" id="security-revoke-current" class="danger-btn">注销当前设备</button></div></section></section>';
    renderShell({ title: "安全设置", kicker: "后台 / 安全设置", activePage: "security" }, body); bindSecurityEvents();
  }

  async function loadSecurityPage() {
    renderLoading("安全设置", "security");
    try { await getAdminCSRFToken(); var data = await fetchAdminJSON("/admin/api/sessions"); securityState.sessions = data.items || []; renderSecurityPage(); } catch (err) { renderError("安全设置", "security", err && err.message ? err.message : "加载失败"); }
  }

  async function refreshDeviceBindingPending() {
    if (!securityState.binding.active) return;
    try {
      var data = await fetchAdminJSON("/admin/api/device-bindings/pending");
      securityState.binding.pending = data.items || [];
      renderSecurityPage();
    } catch (err) {
      securityState.message = err && err.message ? err.message : "绑定状态加载失败";
      renderSecurityPage();
    }
  }

  async function startDeviceBinding() {
    try {
      var csrf = await getAdminCSRFToken();
      var response = await fetch("/admin/api/device-bindings/start", { method: "POST", credentials: "same-origin", headers: { Accept: "image/png", "X-CSRF-Token": csrf } });
      if (!response.ok) throw new Error("绑定二维码生成失败");
      var blob = await response.blob();
      if (securityState.binding.imageURL) URL.revokeObjectURL(securityState.binding.imageURL);
      securityState.binding.imageURL = URL.createObjectURL(blob);
      securityState.binding.bindingId = response.headers.get("X-Admin-Binding-ID") || "";
      securityState.binding.expiresAt = response.headers.get("X-Admin-Binding-Expires-At") || "";
      securityState.binding.active = true;
      securityState.binding.pending = [];
      securityState.message = "二维码已生成，请等待新设备扫码。";
      if (securityState.binding.timer) clearInterval(securityState.binding.timer);
      securityState.binding.timer = setInterval(refreshDeviceBindingPending, 2000);
      renderSecurityPage();
    } catch (err) {
      securityState.message = err && err.message ? err.message : "绑定二维码生成失败";
      renderSecurityPage();
    }
  }

  function stopDeviceBinding() {
    if (securityState.binding.timer) clearInterval(securityState.binding.timer);
    securityState.binding.timer = null;
    if (securityState.binding.imageURL) URL.revokeObjectURL(securityState.binding.imageURL);
    securityState.binding = { active: false, bindingId: "", expiresAt: "", imageURL: "", pending: [], timer: null };
    renderSecurityPage();
  }

  function bindSecurityEvents() {
    var form = document.getElementById("security-reauth-form");
    if (form) form.addEventListener("submit", async function (event) {
      event.preventDefault();
      try { var action = String(form.action.value || "sessions"); await postAdminJSON("/admin/api/security/reauth", { action: action, totp: String(form.totp.value || "") }); securityState.reauthScopes[action] = true; securityState.message = "重新认证成功"; if (action === "qr") securityState.qrLoaded = true; if (action === "sessions") { var data = await fetchAdminJSON("/admin/api/sessions"); securityState.sessions = data.items || []; } renderSecurityPage(); } catch (err) { securityState.message = err && err.message ? err.message : "重新认证失败"; renderSecurityPage(); }
    });
    root.querySelectorAll(".session-expiry-btn").forEach(function (button) { button.addEventListener("click", async function () { var handle = button.getAttribute("data-handle") || ""; var input = null; root.querySelectorAll(".session-expiry-input").forEach(function (node) { if (node.getAttribute("data-handle") === handle) input = node; }); if (!input || !input.value) return; try { await postAdminJSON("/admin/api/sessions/expiry", { sessionHandle: handle, expiresAt: new Date(input.value).toISOString() }); await loadSecurityPage(); } catch (err) { securityState.message = err.message || "更新失败"; renderSecurityPage(); } }); });
    root.querySelectorAll(".session-revoke-btn").forEach(function (button) { button.addEventListener("click", async function () { try { await postAdminJSON("/admin/api/sessions/revoke", { sessionHandle: button.getAttribute("data-handle") || "" }); await loadSecurityPage(); } catch (err) { securityState.message = err.message || "撤销失败"; renderSecurityPage(); } }); });
    var startBinding = document.getElementById("security-start-binding"); if (startBinding) startBinding.addEventListener("click", startDeviceBinding);
    var cancelBinding = document.getElementById("security-cancel-binding"); if (cancelBinding) cancelBinding.addEventListener("click", stopDeviceBinding);
    root.querySelectorAll(".binding-approve-btn").forEach(function (button) { button.addEventListener("click", async function () { try { await postAdminJSON("/admin/api/device-bindings/approve", { bindingId: button.getAttribute("data-binding-id") || "", approved: true }); await refreshDeviceBindingPending(); await loadSecurityPage(); } catch (err) { securityState.message = err.message || "允许绑定失败"; renderSecurityPage(); } }); });
    root.querySelectorAll(".binding-reject-btn").forEach(function (button) { button.addEventListener("click", async function () { try { await postAdminJSON("/admin/api/device-bindings/approve", { bindingId: button.getAttribute("data-binding-id") || "", approved: false }); await refreshDeviceBindingPending(); } catch (err) { securityState.message = err.message || "拒绝绑定失败"; renderSecurityPage(); } }); });
    var revokeAll = document.getElementById("security-revoke-all"); if (revokeAll) revokeAll.addEventListener("click", async function () { try { await postAdminJSON("/admin/api/sessions/revoke-all-others", {}); await loadSecurityPage(); } catch (err) { securityState.message = err.message || "退出失败"; renderSecurityPage(); } });
    var revokeCurrent = document.getElementById("security-revoke-current"); if (revokeCurrent) revokeCurrent.addEventListener("click", async function () { try { await postAdminJSON("/admin/api/sessions/revoke-current", {}); window.location.href = "/admin"; } catch (err) { securityState.message = err.message || "注销失败"; renderSecurityPage(); } });
    var transfer = document.getElementById("security-transfer-primary"); if (transfer) transfer.addEventListener("click", async function () { var target = securityState.sessions.find(function (item) { return !item.isPrimary; }); if (!target) return; try { await postAdminJSON("/admin/api/sessions/transfer-primary", { sessionHandle: target.sessionHandle }); window.location.href = "/admin"; } catch (err) { securityState.message = err.message || "转移失败"; renderSecurityPage(); } });
  }

  function renderDeviceBindPage() {
    root.innerHTML = '<main class="setup-page"><section class="setup-panel"><div class="page-kicker">后台安全</div><h1>绑定新设备</h1><p id="device-bind-message" class="state-message">正在验证绑定请求...</p><div id="device-bind-detail" class="muted"></div><button type="button" id="device-bind-complete" hidden>完成绑定并进入后台</button><a class="ghost-btn" href="/admin/login">改用账号密码登录</a></section></main>';
    var message = document.getElementById("device-bind-message");
    var detail = document.getElementById("device-bind-detail");
    var complete = document.getElementById("device-bind-complete");
    var token = "";
    try { token = new URLSearchParams((window.location.hash || "").replace(/^#/, "")).get("token") || ""; } catch (_) { token = ""; }
    if (!token) { message.textContent = "绑定请求无效或已过期。"; return; }
    fetch("/admin/api/device-bindings/claim", { method: "POST", credentials: "same-origin", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: JSON.stringify({ token: token }) }).then(function (response) {
      return response.json().then(function (payload) { if (!response.ok || !payload.ok) throw new Error(payload.error || "绑定请求无效或已过期"); return payload.data || {}; });
    }).then(function (claimed) {
      history.replaceState({}, "", "/admin/device-bind");
      message.textContent = "已登记当前设备，等待主设备允许绑定。";
      detail.textContent = claimed.bindingId ? "绑定编号：" + claimed.bindingId : "";
      var poll = setInterval(function () {
        fetch("/admin/api/device-bindings/status", { credentials: "same-origin", headers: { Accept: "application/json" } }).then(function (response) { return response.json(); }).then(function (payload) {
          if (!payload || !payload.ok) return;
          var status = payload.data && payload.data.status;
          if (status === "approved") { message.textContent = "主设备已允许绑定，请完成登录。"; complete.hidden = false; clearInterval(poll); }
          if (status === "rejected" || status === "expired") { message.textContent = status === "rejected" ? "主设备已拒绝本次绑定。" : "绑定请求已过期。"; clearInterval(poll); }
        }).catch(function () {});
      }, 2000);
      complete.addEventListener("click", function () {
        complete.disabled = true;
        fetch("/admin/api/device-bindings/complete", { method: "POST", credentials: "same-origin", headers: { Accept: "application/json", "Content-Type": "application/json" }, body: "{}" }).then(function (response) { return response.json().then(function (payload) { if (!response.ok || !payload.ok) throw new Error(payload.error || "绑定失败"); return payload; }); }).then(function () { window.location.href = "/admin"; }).catch(function (err) { complete.disabled = false; message.textContent = err && err.message ? err.message : "绑定失败"; });
      });
    }).catch(function (err) { message.textContent = err && err.message ? err.message : "绑定请求无效或已过期"; });
  }

  function route() {
    var nextPath = window.location.pathname + (window.location.search || "");
    if (nextPath !== lastRoutePath && hasDirtyState()) {
      if (!window.confirm("当前有未保存的后台操作草稿，确认离开？")) {
        history.pushState({}, "", lastRoutePath);
        return;
      }
    }
    lastRoutePath = nextPath;
    if (currentPage() !== "console") stopRuntimeRefresh();
    if (currentPage() === "device-bind") {
      renderDeviceBindPage();
      return;
    }
    if (currentPage() === "setup") {
      renderSetupPage();
      return;
    }
    if (currentPage() === "security") {
      loadSecurityPage();
      return;
    }
    if (currentPage() === "groups") {
      loadGroupWorkbench();
      return;
    }
    if (currentPage() === "permissions") {
      loadPermissionsCenter();
      return;
    }
    if (currentPage() === "switches") {
      loadSwitchCenter();
      return;
    }
    if (currentPage() === "broadcast") {
      loadBroadcastCenter();
      return;
    }
    if (currentPage() === "schedules") {
      loadScheduleCenter();
      return;
    }
    if (currentPage() === "templates") {
      loadTemplatesCenter();
      return;
    }
    if (currentPage() === "snapshots") {
      loadSnapshotsCenter();
      return;
    }
    if (currentPage() === "template-library") {
      loadTemplateLibraryCenter();
      return;
    }
    if (currentPage() === "risk-radar") {
      loadRiskRadarCenter();
      return;
    }
    if (currentPage() === "insights") {
      loadInsightsCenter();
      return;
    }
    if (currentPage() === "retries") {
      loadRetryCenter();
      return;
    }
    loadConsole();
  }

  window.addEventListener("popstate", route);
  route();
})();
