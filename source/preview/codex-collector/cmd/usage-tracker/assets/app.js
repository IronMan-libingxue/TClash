"use strict";

const content = document.querySelector("#content");
const tabs = document.querySelector("#tabs");
const status = document.querySelector("#notice");
const filters = { cache: "1" };
const counts = new Intl.NumberFormat("zh-CN");
const dollars = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 2 });
const ultrafastLabel = "⚡⚡ Ultrafast";
const shortDate = new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });
const longDate = new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
const beijingDate = new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "Asia/Shanghai" });
let page = "overview";
let current;
let loading = false;
let serial = 0;
let noticeTimer;
let priorOverviewAccount = null;
let deviceMenu;
const expandedRequests = new Set();

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = text;
  return node;
}

function button(label, click, primary = false) {
  const node = el("button", primary ? "btn primary" : "btn", label);
  node.type = "button";
  node.addEventListener("click", click);
  return node;
}

function money(value) {
  return value == null ? "—" : "≈" + dollars.format(value);
}

function percent(value) {
  return value == null ? "—" : new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value) + "%";
}

function exists(date) {
  return date && !date.startsWith("0001-");
}

function dateText(date, compact = false) {
  if (!exists(date)) return "尚未读取";
  return (compact ? shortDate : longDate).format(new Date(date));
}

function show(message, error = false) {
  clearTimeout(noticeTimer);
  status.textContent = message;
  status.dataset.error = String(error);
  status.hidden = false;
  noticeTimer = setTimeout(() => { status.hidden = true; }, 5500);
}

async function load(onlyChanged = false) {
  const request = ++serial;
  try {
    const q = new URLSearchParams(Object.entries(filters).filter(([, value]) => value !== ""));
    const response = await fetch("/api/view?" + q, { cache: "no-store" });
    if (!response.ok) throw new Error("无法读取用量");
    const result = await response.json();
    // A live model-speed update must not replace an open selector or a form
    // the user is editing. An explicit selection calls load() without this guard.
    if (onlyChanged && (deviceMenu || (content.contains(document.activeElement) &&
        ["SELECT", "INPUT", "TEXTAREA"].includes(document.activeElement.tagName)))) return;
    if (request === serial && (!onlyChanged || JSON.stringify(result) !== JSON.stringify(current))) {
      current = result;
      draw();
    }
  } catch (err) {
    if (request !== serial) return;
    if (!current) content.replaceChildren(el("div", "empty warning", err.message));
    show(err.message, true);
  } finally {
    content.setAttribute("aria-busy", "false");
  }
}

async function action(name, body = {}, message = "已更新", after, reload = true) {
  if (loading) return false;
  loading = true;
  show("正在处理…");
  try {
    const response = await fetch("/api/action/" + name, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "操作失败");
    if (after) after();
    if (reload) await load();
    show(message);
    return true;
  } catch (err) {
    show(err.message, true);
    return false;
  } finally {
    loading = false;
  }
}

function go(next) {
  const previous = page;
  closeDeviceMenu();
  page = next;
  if (next === "overview" && priorOverviewAccount !== null) {
    if (filters.account === "all") filters.account = priorOverviewAccount;
    priorOverviewAccount = null;
  }
  if (next === "overview" && (filters.device || filters.project || filters.model || filters.session)) {
    filters.device = filters.project = filters.model = filters.session = filters.page = "";
    void load();
    return;
  }
  if (previous === "settings" && next !== previous) void load();
  else draw();
}

function choose(name, value) {
  if (name === "account" && page === "detail") priorOverviewAccount = null;
  filters[name] = value;
  filters.page = "";
  if (name === "account") filters.window = "";
  if (name === "account" || name === "device" || name === "window") {
    for (const field of ["project", "model", "session"]) filters[field] = "";
  }
  return load();
}

function heading(title, description, actions = []) {
  const head = el("div", "heading");
  const copy = el("div");
  copy.append(el("h1", "", title));
  if (description) copy.append(el("p", "sub", description));
  const links = el("div", "actions");
  links.append(...actions);
  head.append(copy, links);
  return head;
}

function field(label, control) {
  const box = el("label", "field");
  box.append(el("span", "", label), control);
  return box;
}

function picker(name, label, options, selected) {
  const control = el("select");
  for (const option of options) {
    const row = el("option", "", option.name);
    row.value = option.id;
    control.append(row);
  }
  control.value = selected || "";
  control.addEventListener("change", () => choose(name, control.value));
  return field(label, control);
}

function usageControls(v, detail = false) {
  const controls = el("div", "controls");
  if (v.role === "a") controls.append(picker("account", "账号", v.accounts || [], v.account));
  const windows = [{ id: "all", name: "全部已读记录" }];
  for (const w of v.windows || []) {
    windows.push({ id: w.id, name: (w.current ? "本周期 · " : "") + dateText(w.start, true) + " – " + dateText(w.cutoff || w.end, true) });
  }
  controls.append(picker("window", "重置周期", windows, v.window));
  if (detail) controls.append(picker("device", "设备", v.devices, v.device));
  if (detail) {
    for (const [id, name, groups] of [
      ["project", "项目", v.projects], ["model", "模型", v.models], ["session", "会话", v.sessions],
    ]) {
      const options = [{ id: "", name: "全部" + name }, ...(groups || [])];
      if (filters[id] && !options.some(x => x.id === filters[id])) {
        options.push({ id: filters[id], name: "已选" + name });
      }
      controls.append(picker(id, name, options, filters[id]));
    }
  }
  const checkbox = el("input");
  checkbox.type = "checkbox";
  checkbox.checked = v.cache;
  checkbox.addEventListener("change", () => choose("cache", checkbox.checked ? "1" : "0"));
  const check = el("label", "check");
  check.append(checkbox, document.createTextNode("含缓存 Token"));
  controls.append(check);
  return controls;
}

function amount(g, cache) {
  return g.input + g.output + (cache ? g.cache : 0);
}

function tokenText(n, factor = 1) {
  return counts.format(Math.round(n * factor));
}

function estimatedMoney(g, factor = 1) {
  return money(g.count > g.unpriced ? g.usd * factor : null);
}

function estimate(g, v) {
  return deviceEstimate(g, v, v.displayFactor);
}

function deviceEstimate(g, v, factor = 1) {
  if (!v.quota || !v.rule?.budget || !g || g.count <= g.unpriced) return null;
  return (g.usd * factor) / v.rule.budget * 100;
}

function scaleText(factor) {
  return "设定倍率 ×" + factor;
}

function usageTrack(width) {
  const track = el("span", "track");
  if (width != null) {
    const fill = el("i");
    fill.style.width = width + "%";
    track.append(fill);
  }
  return track;
}

function quotaProgress(label, share) {
  const width = share == null ? null : Math.min(100, Math.max(0, share));
  const track = usageTrack(width);
  track.classList.add("quota-track");
  if (width == null) {
    track.setAttribute("aria-hidden", "true");
  } else {
    track.setAttribute("role", "progressbar");
    track.setAttribute("aria-label", label);
    track.setAttribute("aria-valuenow", String(width));
    track.setAttribute("aria-valuetext", percent(share));
  }
  return track;
}

function kpi(label, value, note, fill) {
  const box = el("section", "card kpi");
  box.append(el("div", "sub", label), el("div", "value", value));
  if (fill != null) {
    const bar = el("progress");
    bar.max = 100;
    bar.value = fill;
    bar.setAttribute("aria-label", label);
    box.append(bar);
  }
  if (note) box.append(el("div", "foot", note));
  return box;
}

function charts(v) {
  const grid = el("div", "twocol");
  for (const [key, name, rows] of [
    ["project", "项目", v.role === "a" ? v.projects : v.displayProjects],
    ["model", "模型", v.role === "a" ? v.models : v.displayModels],
  ]) {
    const card = el("section", "card chart " + key);
    const header = el("div", "chart-head");
    header.append(el("h2", "", name));
    if (v.quota || v.role === "b") header.append(el("span", "muted", "Token · 参考美元 · 预算占比"));
    card.append(header);
    if (!rows?.length) {
      card.append(el("div", "empty", "该范围没有用量记录"));
      grid.append(card);
      continue;
    }
    const group = el("div", "chart-list");
    const tokens = g => v.role === "a" ? amount(g, v.cache) : g.tokens;
    const max = Math.max(1, ...rows.map(tokens));
    for (const row of rows) {
      const clickable = v.role === "a";
      const line = el(clickable ? "button" : "div", "bar-row");
      if (clickable) {
        line.type = "button";
        line.addEventListener("click", () => {
          page = "detail";
          filters.project = filters.model = filters.session = "";
          choose(key, row.id);
        });
      }
      const title = el("div", "bar-label");
      const numbers = el("div", "item-values");
      const dollar = v.role === "a" && row.count === row.unpriced ? null : row.usd;
      const share = v.role === "a" ? estimate(row, v) : row.percent;
      numbers.append(el("span", "num", counts.format(tokens(row))), el("span", "money", money(dollar)));
      if (key === "model" || share != null) numbers.append(el("span", "", percent(share)));
      if (row.unpriced) numbers.title = "部分请求缺少定价";
      title.append(el("span", "item-name", row.name), numbers);
      const track = key === "model" ? quotaProgress(row.name + " 自定义预算占比", share) :
        usageTrack(Math.max(0, 100 * tokens(row) / max));
      line.append(title, track);
      group.append(line);
    }
    card.append(group);
    grid.append(card);
  }
  return grid;
}

const deviceHistory = new Map();
const expandedModelLists = new Set();

function bytes(value) {
  if (!Number.isFinite(value) || value < 0) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return value.toFixed(unit ? 2 : 0) + " " + units[unit];
}

function deviceObservation(card, v) {
  const activity = card.telemetry?.activity;
  if (!activity || !exists(activity.at)) return { activity: null, level: "", text: "当前会话状态未提供" };
  const age = Date.now() - new Date(activity.at).getTime();
  const limit = card.id === "local" ? 30000 : Math.max(120000, ((v.syncMinutes || 5) * 2 + 1) * 60000);
  if (!Number.isFinite(age) || age < -300000 || age > limit) return { activity: null, level: "", text: "会话状态已过期 · 上次观察 " + dateText(activity.at, true) };
  let text = "当前 " + counts.format(activity.active) + " 个独立会话运行";
  if (activity.ultrafast > 0) text += " · " + ultrafastLabel;
  if (activity.fast > 0) text += " · ⚡ Fast";
  if (activity.uncertain || activity.fastUncertain) text += " · 部分状态未知";
  return { activity, level: card.activityLevel || "", text };
}

function deviceTelemetry(card, v) {
  const box = el("div", "device-telemetry");
  const observation = deviceObservation(card, v);
  box.append(el("p", observation.activity?.ultrafast > 0 ? "tier-ultrafast" : "sub", observation.text));
  if (exists(card.countSince)) box.append(el("p", "muted", "统计起点 " + dateText(card.countSince)));
  const stats = card.activityStats;
  if (stats) box.append(el("p", "sub", "所选周期并发 " + counts.format(stats.occurrences) + " 次 · " +
    counts.format(Math.floor(stats.seconds / 60)) + " 分钟 · 峰值 " + counts.format(stats.peak) + " 个" + (stats.incomplete ? "（记录不完整）" : "")));
  if (card.fastTotal?.count) {
    const detail = el("details", "fast-breakdown");
    detail.append(el("summary", "", "Fast 用量 · " + tokenText(amount(card.fastTotal, v.cache), card.displayFactor) +
      " Token · " + counts.format(card.fastTotal.count) + " 条请求 · " + estimatedMoney(card.fastTotal, card.displayFactor)));
    const breakdown = item => item.name + " · " + counts.format(item.count) + " 条 · 输入 " + tokenText(item.input, card.displayFactor) +
      " · 输出 " + tokenText(item.output, card.displayFactor) + " · 缓存 " + tokenText(item.cache, card.displayFactor) + " · " +
      estimatedMoney(item, card.displayFactor) + " · " + percent(deviceEstimate(item, v, card.displayFactor));
    for (const model of card.fastModels || []) detail.append(el("p", "sub", "模型 " + breakdown(model)));
    for (const session of card.fastSessions || []) detail.append(el("p", "sub", breakdown(session)));
    detail.append(el("p", "muted", "按日志中可确认的档位汇总，可归属的子代理用量计入主会话。"));
    box.append(detail);
  }
  if (card.tierUnknown) box.append(el("p", "muted", counts.format(card.tierUnknown) + " 条请求未记录可确认的档位"));
  const traffic = card.telemetry?.traffic;
  if (traffic) {
    const section = el("div", "device-traffic");
    section.append(el("p", "", traffic.month + " 代理组总计 " + bytes(traffic.upload + traffic.download)));
    for (const group of traffic.groups || []) {
      const scope = !group.scope || group.scope === "legacy" ? "早期采集，配置未区分" : group.scope === "unknown" ? "配置未知" : "配置 " + group.scope.slice(0, 6);
      section.append(el("p", "sub", group.name + "（" + scope + "） · ↑ " + bytes(group.upload) + " · ↓ " + bytes(group.download) + " · 合计 " + bytes(group.upload + group.download)));
    }
    const since = exists(traffic.since) ? beijingDate.format(new Date(traffic.since)) + " 起采集（北京时间）" : "开始采集时间未知";
    section.append(el("p", "muted", "快照统计，可能漏计；跨组重复计入 · " + since + " · 更新 " + dateText(traffic.at, true)));
    box.append(section);
  } else box.append(el("p", "muted", "代理组月流量尚未提供"));
  return box;
}

function deviceModels(card, v) {
  const models = el("div", "device-models");
  const rows = card.models || [];
  if (!rows.length) {
    models.append(el("p", "sub", "该周期暂无用量"));
    return models;
  }
  const modelRow = model => {
    const row = el("div", "device-model");
    const share = deviceEstimate(model, v, card.displayFactor);
    if (model.unpriced) row.title = counts.format(model.unpriced) + " 条请求缺少参考价格";
    row.append(el("span", "item-name", model.name), el("span", "num", tokenText(amount(model, v.cache), card.displayFactor)),
      el("span", "money", estimatedMoney(model, card.displayFactor)), el("span", "sub", percent(share)),
      quotaProgress(model.name + " 自定义预算占比", share));
    return row;
  };
  models.append(...rows.slice(0, 3).map(modelRow));
  if (rows.length > 3) {
    const extra = el("details", "device-models-extra");
    extra.open = expandedModelLists.has(card.id);
    extra.addEventListener("toggle", () => {
      if (extra.open) expandedModelLists.add(card.id);
      else expandedModelLists.delete(card.id);
    });
    extra.append(el("summary", "", "其余 " + counts.format(rows.length - 3) + " 个模型"), ...rows.slice(3).map(modelRow));
    models.append(extra);
  }
  return models;
}

function setDeviceScale(id, enabled, reload = true) {
  return action("device-scale", { id, enabled }, enabled ? "已切换为设定倍率" : "已切换为真实倍率", () => {
    current.scaledDevices ??= {};
    current.scaledDevices[id] = enabled;
  }, reload);
}

function closeDeviceMenu() {
  deviceMenu?.remove();
  deviceMenu = null;
}

function showDeviceMenu(card, x, y, anchor) {
  closeDeviceMenu();
  const menu = el("div", "device-menu");
  deviceMenu = menu;
  menu.setAttribute("role", "menu");
  menu.setAttribute("aria-label", card.name + "显示口径");
  for (const [flag, label] of [[false, "以真实倍率显示"], [true, "以设定倍率显示"]]) {
    const checked = flag === Boolean(card.scaled);
    const option = button((checked ? "✓ " : "") + label, () => {
      closeDeviceMenu();
      if (!checked) void setDeviceScale(card.id, flag);
      else anchor.focus({ preventScroll: true });
    });
    option.setAttribute("role", "menuitemradio");
    option.setAttribute("aria-checked", String(checked));
    menu.append(option);
  }
  document.body.append(menu);
  menu.style.left = Math.max(4, Math.min(x, innerWidth - menu.offsetWidth - 4)) + "px";
  menu.style.top = Math.max(4, Math.min(y, innerHeight - menu.offsetHeight - 4)) + "px";
  menu.querySelector('[aria-checked="true"]').focus({ preventScroll: true });
  menu.addEventListener("keydown", event => {
    if (event.key === "Escape") {
      closeDeviceMenu();
      anchor.focus({ preventScroll: true });
    } else if (event.key === "ArrowUp" || event.key === "ArrowDown") {
      (document.activeElement.nextElementSibling || menu.firstElementChild).focus();
    } else return;
    event.preventDefault();
  });
}

document.addEventListener("pointerdown", event => {
  if (deviceMenu && !deviceMenu.contains(event.target)) closeDeviceMenu();
});
window.addEventListener("resize", closeDeviceMenu);

function deviceCards(v) {
  const key = "codex-usage-updates-v2:" + (v.repo || "local") + ":" + v.window + ":" + v.account;
  let history = deviceHistory.get(key);
  if (!history) {
    try { history = JSON.parse(localStorage.getItem(key)); } catch {}
  }
  const previous = history || {};
  const next = Object.create(null);
  const grid = el("div", "device-grid");
  for (const card of v.cards || []) {
    const total = amount(card.total, true);
    const version = JSON.stringify((card.models || []).map(row =>
      [row.id, row.input, row.output, row.cache, row.count]).sort((a, b) => a[0].localeCompare(b[0])));
    const old = Object.hasOwn(previous, card.id) ? previous[card.id] :
      !card.simulated && Object.hasOwn(previous, card.name) ? previous[card.name] : undefined;
    const changed = Boolean(old && !old.pending && !card.pending && old.version !== version);
    const unread = changed || Boolean(old?.unread);
    const delta = changed ? total - old.total : old?.delta || 0;
    next[card.id] = { version, total, unread, delta, pending: Boolean(card.pending) };
    const observation = deviceObservation(card, v);
    const level = observation.level ? " device-" + observation.level : unread ? " device-updated" : "";
    const item = el("section", "card device-card" + level);
    if (observation.level === "ultrafast-parallel") {
      // Polling redraws the cards. Continue the same pulse phase after a redraw.
      item.style.animationDelay = -(performance.now() % 4800) + "ms";
    }
    const header = el("div", "device-head");
    const name = el("div", "device-name");
    name.append(el("h2", "", card.name));
    if (card.simulated) name.append(el("span", "simulation-badge", "模拟"));
    header.append(name);
    if (unread) header.append(el("span", "device-alert", delta > 0 ? "+" + tokenText(delta, card.displayFactor) + " Token" : "用量已更新"));
    item.append(header);
    if (card.pending) {
      item.append(el("p", "sub", "等待真实设备接入"));
      grid.append(item);
      continue;
    }
    if (v.paired?.some(device => device.id === card.id)) {
      item.tabIndex = 0;
      item.setAttribute("aria-label", card.name + " · " + (card.scaled ? scaleText(card.displayFactor) : "真实倍率"));
      item.addEventListener("contextmenu", event => {
        event.preventDefault();
        showDeviceMenu(card, event.clientX, event.clientY, item);
      });
      item.addEventListener("keydown", event => {
        if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
          event.preventDefault();
          const box = item.getBoundingClientRect();
          showDeviceMenu(card, box.left + 12, box.top + 12, item);
        }
      });
    }
    const numbers = el("div", "device-total");
    numbers.append(el("strong", "num", tokenText(amount(card.total, v.cache), card.displayFactor)), el("span", "sub", "设备全量 Token"));
    if (card.scaled) numbers.append(el("span", "scale-indicator", scaleText(card.displayFactor)));
    item.append(numbers);
    const share = deviceEstimate(card.total, v, card.displayFactor);
    const meta = [estimatedMoney(card.total, card.displayFactor),
      counts.format(card.total.count) + " 条请求"];
    if (card.total.unpriced) meta.push(counts.format(card.total.unpriced) + " 条未计价");
    item.append(el("p", "sub device-meta", meta.join(" · ")));
    if (card.unassigned?.count) item.append(el("p", "sub device-meta", "账号待确认 " +
      counts.format(card.unassigned.count) + " 条 · " + tokenText(amount(card.unassigned, v.cache), card.displayFactor) + " Token · " +
      estimatedMoney(card.unassigned, card.displayFactor)));
    if (card.otherAccount?.count) item.append(el("p", "sub device-meta", "其他账号 " +
      counts.format(card.otherAccount.count) + " 条 · " + tokenText(amount(card.otherAccount, v.cache), card.displayFactor) + " Token"));
    const quota = el("div", "device-quota");
    const caption = el("div", "device-quota-caption");
    const quotaAmount = share == null ? "—" : estimatedMoney(card.total, card.displayFactor) + " / " + dollars.format(v.rule.budget);
    caption.append(el("span", "sub", "设备全量参考预算"), el("span", "sub num", quotaAmount), el("strong", "num", percent(share)));
    quota.append(caption, quotaProgress(card.name + " 自定义预算占比", share));
    item.append(quota, deviceModels(card, v), deviceTelemetry(card, v));
    const footer = el("div", "device-foot");
    footer.append(el("span", "muted", exists(card.updated) ? "数据 " + dateText(card.updated, true) : "暂无数据时间"),
      button("查看明细", () => {
        next[card.id].unread = false;
        deviceHistory.set(key, next);
        try { localStorage.setItem(key, JSON.stringify(next)); } catch {}
        page = "detail";
        priorOverviewAccount = v.account;
        filters.account = "all";
        choose("device", card.id);
      }));
    item.append(footer);
    grid.append(item);
  }
  if (JSON.stringify(next) !== JSON.stringify(previous)) {
    deviceHistory.set(key, next);
    try { localStorage.setItem(key, JSON.stringify(next)); } catch {}
  }
  return grid;
}

function speedStrip(speed) {
  const card = el("section", "card speed-strip");
  const label = el("div", "speed-label");
  label.append(el("span", "sub", "本机模型速度"), el("strong", "", speed.model || "等待模型记录"));
  const value = el("div", "speed-value");
  const number = speed.rate == null ? "—" : new Intl.NumberFormat("zh-CN", {
    minimumFractionDigits: 2, maximumFractionDigits: 2,
  }).format(speed.rate);
  value.append(el("strong", "num", number), el("span", "sub", "tok/s"));
  const note = speed.state === "idle" ? "当前没有运行中的会话" : speed.state === "unknown" ? "当前会话状态未知" : speed.rate == null ? "等待可计算的本机记录" :
    (speed.wholeRequest ? "最近请求平均（含等待）" : "最近生成速度估算") + " · " + dateText(speed.at, true);
  card.append(label, value, el("span", "muted speed-note", note));
  return card;
}

function overview(v) {
  if (v.role === "b") {
    const state = exists(v.lastSync) ? "最近同步 " + dateText(v.lastSync) : "等待首次同步";
    content.append(heading(v.name || "本机用量", state), speedStrip(v.speed), usageControls(v),
      el("p", "simulation-notice", "美元和百分比为参考价格与自定义预算的折算，并非官方实际扣费或额度占比。"), charts(v));
    return;
  }
  const actions = [button("重读本机日志", () => action("refresh", {}, "本机日志已读取"))];
  if (v.repo) actions.push(button("立即同步", () => action("sync", {}, "同步完成")));
  if (v.simulationAvailable) {
    const enabled = Boolean(v.simulationEnabled);
    actions.push(button(enabled ? "隐藏模拟设备" : "显示模拟设备", () =>
      action("simulation", { enabled: !enabled }, enabled ? "模拟设备已隐藏" : "模拟设备已显示", () => {
        filters.device = filters.project = filters.model = filters.session = filters.page = "";
        page = "overview";
      })));
  }
  actions.push(button("读取官方额度", () => action("quota", {}, "已读取官方额度"), true));
  content.append(heading("用量总览", "", actions), speedStrip(v.speed), usageControls(v));
  content.append(el("p", "simulation-notice", "A 端计价来源 · " + pricingSources[v.pricing.source].name + "。参考金额与占比按该价表和自定义预算折算。官方额度取账号读数。"));
  if (v.accountUnavailable && v.account === "current") content.append(el("p", "simulation-warning", "暂时无法识别当前登录账号；请选择已记录的账号或查看待确认记录。"));
  if (v.unassignedCount && v.account !== "unassigned" && v.account !== "all") content.append(el("p", "simulation-warning", counts.format(v.unassignedCount) + " 条请求的账号归属待确认，未计入当前选择的账号。可在账号筛选中查看。"));
  if (v.total?.inferred) content.append(el("p", "simulation-warning", counts.format(v.total.inferred) + " 条请求根据本机登录观察和日志推定账号；Codex 未提供逐条账号凭据，推定不是官方确认。"));
  if (v.account === "unassigned") content.append(el("p", "simulation-warning", "这些请求的账号尚未确认，不计入任何账号的已确认统计。"));
  if (v.account === "all") content.append(el("p", "simulation-warning", "当前显示全部账号的原始日志总量，包含待确认记录。选择本周期时按当前账号周期截取，其他账号的重置时间可能不同；该合计不代表单个账号额度。"));
  if (v.simulationEnabled) {
    content.append(el("p", "simulation-notice", "模拟设备仅供演示，顶部真实汇总只统计本机和已接入的真实设备。"));
  }
  if (v.paired?.length) content.append(el("p", "simulation-warning", "其他设备的账号归属依据该设备提供的记录；若设备上切换过账号，请核对其记录。"));
  if (v.simulationStatus) content.append(el("p", "simulation-warning", v.simulationStatus));
  const t = v.total;
  const kpis = el("div", "kpis");
  const quota = kpi("自定义预算占比", percent(estimate(t, v)), v.quota ? "参考预算 " + dollars.format(v.rule.budget) : "选择重置周期后查看");
  const local = v.cards?.find(card => card.id === "local")?.total;
  const localShare = deviceEstimate(local, v);
  quota.append(el("div", "foot kpi-local", "本机设备全量参考折算 " +
    (localShare == null ? "—" : money(local.usd) + " · " + percent(localShare))));
  const accountTokens = kpi(v.account === "all" ? "全部账号日志 Token" : v.account === "unassigned" ? "待确认日志 Token" : "所选账号已归属 Token", counts.format(amount(t, v.cache)), counts.format(t.count) + " 条请求");
  if (v.deviceTotal) accountTokens.append(el("div", "foot kpi-local", "全部真实设备全量 " +
    counts.format(amount(v.deviceTotal, v.cache)) + " Token · 待确认 " + counts.format(v.deviceUnassigned?.count || 0) + " 条"));
  kpis.append(
    accountTokens,
    kpi("参考美元估算", money(t.count > t.unpriced ? t.usd : null), t.unpriced ? t.unpriced + " 条请求缺少参考价格" : "按模型参考价格折算，非实际扣费"),
    kpi("账号官方已用", percent(v.official?.used), v.official ? "剩余 " + percent(100 - v.official.used) + " · " + dateText(v.official.end, true) + " 重置 · 读取 " + dateText(v.official.at, true) : v.account === "all" ? "全部账号不能合并官方额度" : "暂无该账号周期的直接官方读数", v.official?.used),
    quota,
  );
  content.append(kpis, deviceCards(v));
  const bits = ["本机日志 " + dateText(v.readAt), v.repo ? "私库同步 " + dateText(v.lastSync) : "尚未连接私有仓库"];
  if (v.official) bits.push("官方额度读取 " + dateText(v.official.at));
  const unknown = t.unassigned - (t.otherAccount || 0);
  if (unknown > 0 && v.quota && v.account !== "all") bits.push(unknown + " 条账号归属未确认，未计入当前账号额度估算");
  content.append(el("p", "status-line", bits.join(" · ")));
}

function effortSummary(v, isSession) {
  const section = el("section", "card table-card effort-card");
  section.id = "effort-summary";
  section.tabIndex = -1;
  section.setAttribute("aria-labelledby", "effort-title");
  const head = el("div", "table-head");
  const title = el("h2", "", isSession ? "会话用量 · 思考深度与档位" : filters.model + " · 思考深度");
  title.id = "effort-title";
  head.append(title);
  section.append(head);
  if (isSession) {
    if (!v.sessionQuota) {
      section.append(el("p", "empty", "暂无可确认的当前重置周期，无法汇总该会话。"));
      return section;
    }
    head.append(el("span", "sub", "本周期 · 全部模型 · " + dateText(v.sessionQuota.start, true) + " – " +
      dateText(v.sessionQuota.cutoff || v.sessionQuota.end, true)));
  }
  const efforts = isSession ? v.sessionEfforts : v.efforts;
  if (!efforts?.length) {
    section.append(el("p", "empty", "该会话在本周期暂无用量记录。"));
    return section;
  }
  const factor = v.scaled && isSession ? (v.sessionRule?.devices?.[v.device] ?? v.sessionRule?.factor ?? v.displayFactor) : v.displayFactor;
  const scope = isSession ? { quota: v.sessionQuota, rule: v.sessionRule } : v;
  const table = el("table"), thead = el("thead"), header = el("tr"), body = el("tbody");
  [isSession ? "思考深度 · 档位" : "思考深度", v.scaled ? "显示 Token" : "实际 Token", "请求", "参考美元", "自定义预算占比"].forEach((name, i) => header.append(el("th", i === 0 ? "text" : "", name)));
  thead.append(header);
  for (const effort of efforts) {
    const row = el("tr");
    const price = el("td", "money", estimatedMoney(effort, factor));
    if (effort.unpriced) price.append(el("div", "muted", counts.format(effort.unpriced) + " 条未计价"));
    row.append(el("td", effort.name.endsWith(ultrafastLabel) ? "text tier-ultrafast" : "text", effort.name), el("td", "num", tokenText(amount(effort, v.cache), factor)),
      el("td", "num", counts.format(effort.count)), price,
      el("td", "num", percent(deviceEstimate(effort, scope, factor))));
    body.append(row);
  }
  table.append(thead, body);
  const scroll = el("div", "table-scroll");
  scroll.append(table);
  section.append(scroll);
  return section;
}

function requestMode(r) {
  const mode = { normal: "标准", default: "标准", standard: "标准", fast: "⚡ Fast", priority: "⚡ Fast",
    ultrafast: ultrafastLabel, ultra_fast: ultrafastLabel }[r.tier?.toLowerCase()];
  if (!mode) return "档位未知";
  return mode + (r.actualTier ? "（实际）" : "（所选）");
}

function details(v) {
  content.append(heading("用量明细", "选择设备和周期，按项目、模型或会话查看逐次请求。",
    [button("清除明细筛选", () => {
      filters.project = filters.model = filters.session = "";
      filters.page = "";
      void load();
    })]), usageControls(v, true));
  const reading = [v.scaled ? scaleText(v.displayFactor) : "真实倍率",
    v.simulatedDevice ? "模拟设备参考金额" : "A 端计价来源 " + pricingSources[v.pricing.source].name];
  if (v.device !== "local" && !v.simulatedDevice) reading.push("设备缓存合计按缓存读取价估算");
  content.append(el("p", "simulation-notice", reading.join(" · ") + " · 占比按本周期自定义预算折算。"));
  if (v.simulatedDevice) content.append(el("p", "simulation-notice", "当前查看的是模拟设备数据，未计入真实汇总。"));
  if (v.account === "unassigned") content.append(el("p", "simulation-warning", "本机待确认请求可逐条选择账号并确认；请只确认你能核实的记录。其他设备的待确认记录暂不能在这里修改。"));
  const totals = el("div", "kpis");
  const factor = v.displayFactor;
  const priceNote = v.quota ? "自定义预算占比 " + percent(estimate(v.total, v)) : "";
  const unavailable = v.total.unpriced ? counts.format(v.total.unpriced) + " 条未计价" : "";
  totals.append(
    kpi(v.scaled ? "显示 Token" : "实际 Token", tokenText(amount(v.total, v.cache), factor), counts.format(v.count) + " 条请求"),
    kpi("输入与缓存", tokenText(v.total.input, factor), "另有 " + tokenText(v.total.cache, factor) + " 缓存 Token"),
    kpi("输出 Token", tokenText(v.total.output, factor), ""),
    kpi("参考美元估算", estimatedMoney(v.total, factor), [priceNote, unavailable].filter(Boolean).join(" · ")),
  );
  content.append(totals);
  if (filters.session) content.append(effortSummary(v, true));
  else if (filters.model && v.efforts?.length) content.append(effortSummary(v, false));
  const card = el("section", "card table-card");
  const head = el("div", "table-head");
  head.append(el("h2", "", "请求"), el("span", "sub", counts.format(v.count) + " 条"));
  card.append(head);
  if (!v.rows?.length) {
    card.append(el("div", "empty", "该筛选范围没有请求"));
    content.append(card);
    return;
  }
  const table = el("table");
  const thead = el("thead"), header = el("tr");
  ["时间", "设备", "项目", "会话", "模型", "输入", "输出", "缓存", "参考美元", "账号归属"].forEach((name, i) => header.append(el("th", i < 5 || i === 9 ? "text" : "", name)));
  thead.append(header);
  const body = el("tbody");
  for (const r of v.rows) {
    const row = el("tr", "request-row");
    const key = r.device + "/" + r.id;
    const extra = el("tr", "request-detail");
    extra.id = "request-" + r.device + "-" + r.id;
    extra.hidden = !expandedRequests.has(key);
    const when = el("td", "text");
    const toggle = el("button", "request-toggle", (extra.hidden ? "▸ " : "▾ ") + dateText(r.at));
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", String(!extra.hidden));
    toggle.setAttribute("aria-controls", extra.id);
    toggle.title = "查看该条请求的思考深度和档位";
    toggle.onclick = () => {
      extra.hidden = !extra.hidden;
      if (extra.hidden) expandedRequests.delete(key);
      else expandedRequests.add(key);
      toggle.textContent = (extra.hidden ? "▸ " : "▾ ") + dateText(r.at);
      toggle.setAttribute("aria-expanded", String(!extra.hidden));
    };
    when.append(toggle);
    row.append(when);
    row.addEventListener("click", event => {
      if (!event.target.closest("button, select, input, a")) toggle.click();
    });
    for (const value of [r.deviceName, r.projectName]) {
      const cell = el("td", "text", value);
      cell.title = value;
      row.append(cell);
    }
    const cell = el("td", "text"), jump = el("button", "", r.session.slice(0, 10));
    jump.type = "button";
    jump.title = "筛选该会话";
    jump.onclick = () => choose("session", r.device + "/" + r.session);
    cell.append(jump);
    const modelCell = el("td", "text"), model = el("button", "", r.model);
    model.type = "button";
    model.title = "查看该模型的思考深度";
    model.onclick = async () => {
      await choose("model", r.model);
      const summary = document.querySelector("#effort-summary");
      summary?.focus({ preventScroll: true });
      summary?.scrollIntoView({ block: "nearest" });
    };
    modelCell.append(model);
    const mode = requestMode(r);
    const ultrafast = mode.startsWith(ultrafastLabel);
    if (mode.startsWith("⚡")) modelCell.append(el("span", ultrafast ? "tier-ultrafast" : "sub",
      " " + mode + (r.fastOwner && r.fastOwner !== r.session ? " · 主会话 " + r.fastOwner.slice(0, 10) : "")));
    else if (mode === "档位未知") modelCell.append(el("span", "muted", " · 档位未知"));
    row.append(cell, modelCell);
    for (const value of [r.input, r.output, r.cache]) row.append(el("td", "num", tokenText(value, factor)));
    const attribution = el("td", "text sub");
    if (v.account === "unassigned" && r.device === "local" && !r.simulated) {
      const target = el("select");
      target.append(Object.assign(el("option", "", "选择账号"), { value: "" }));
      for (const item of v.accountTargets || []) target.append(Object.assign(el("option", "", item.name), { value: item.id }));
      const confirm = button("确认归属", () => {
        if (!target.value) return show("请先选择账号", true);
        action("confirm-account", { id: r.id, account: target.value }, "已记录这条请求的账号归属");
      });
      attribution.append(target, confirm);
    } else attribution.textContent = r.simulated ? "模拟" : r.otherAccount ? "其他账号" : r.attribution === "inferred" ? "观察推定" : r.attribution === "confirmed" ? "已核对" : r.account ? "设备记录" : "待确认";
    const price = el("td", r.priced ? "money" : "muted", r.priced ? money(r.usd * (factor ?? 1)) : "未计价");
    if (!r.priced) price.title = r.priceReason || "该请求的参考价格未确认";
    row.append(price, attribution);
    const depth = r.effort === "xhigh" ? "X High" : r.effort || "未记录";
    const note = el("td", ultrafast ? "text tier-ultrafast" : "text", "思考深度 " + depth + " · " + mode);
    note.colSpan = 10;
    extra.append(note);
    body.append(row, extra);
  }
  table.append(thead, body);
  const scroll = el("div", "table-scroll");
  scroll.id = "request-scroll";
  scroll.append(table);
  card.append(scroll);
  const pagination = el("div", "pagination");
  const pages = Math.ceil(v.count / 50);
  const previous = button("上一页", () => { filters.page = String(v.page - 1); void load(); });
  const next = button("下一页", () => { filters.page = String(v.page + 1); void load(); });
  previous.disabled = v.page === 0;
  next.disabled = v.page + 1 >= pages;
  pagination.append(el("span", "sub", (v.page + 1) + " / " + pages + " 页"), previous, next);
  card.append(pagination);
  content.append(card);
}

function input(type, value, placeholder) {
  const node = el("input");
  node.type = type;
  node.value = value || "";
  node.placeholder = placeholder || "";
  if (type === "password") node.autocomplete = "off";
  return node;
}

const permissionText = {
  authorized: "已获得 macOS 通知权限",
  quiet: "已获授权，横幅取决于 macOS 通知设置",
  denied: "请在 macOS「系统设置 → 通知 → Codex 用量」允许通知",
  pending: "等待 macOS 通知授权",
  unavailable: "请在 macOS 桌面应用中启用",
};

const pricingSources = {
  magpie: { name: "Magpie · Models.dev", url: "https://models.dev/" },
  "agent-speed": { name: "Agent Speed · GitHub", url: "https://github.com/zoefix/agent-speed/blob/main/spec/assets/prices.json" },
};

function repriceHelp(price) {
  const card = el("details", "card setting stack price-help");
  card.append(el("summary", "", "计价与使用说明"));
  card.append(el("p", "sub", price.source === "agent-speed"
    ? "Agent Speed 提供版本化模型单价。"
    : "Magpie 从 Models.dev 目录读取模型单价。"));
  card.append(el("p", "sub", "两种来源统一按官方 API 档位估算。GPT-6.2 Sol 和 Astra 的 Fast 为标准价的 2 倍，Ultrafast 自北京时间 2027 年 3 月 1 日 16:00 起由 3 倍调整为 6 倍。"));
  card.append(el("p", "sub", "A 端用当前价表重算本机和已同步的设备用量。新版 B 端随同步接收当前价表。未拆分的缓存按缓存读取价估算。"));
  card.append(el("p", "sub", "预算占比按自定义美元额度计算。Codex 的实际套餐额度与积分见官方用量。"));
  for (const [title, url] of [
    ["OpenAI 模型价格", "https://developers.openai.com/api/docs/pricing"],
    ["Codex 速度模式", "https://learn.chatgpt.com/docs/agent-configuration/speed"],
    ["Codex 套餐与积分", "https://learn.chatgpt.com/docs/pricing#speed-multipliers"],
  ]) {
    const source = el("a", "", title);
    source.href = url;
    source.target = "_blank";
    source.rel = "noopener noreferrer";
    card.append(source);
  }
  return card;
}

let pricingPending = false;

async function pricingAction(actionID, payload, message) {
  if (pricingPending || loading) return;
  pricingPending = true;
  drawPricingPanel(current.pricing);
  try {
    await action(actionID, payload, message, undefined, false);
  } finally {
    pricingPending = false;
    await refreshPriceStatus();
  }
}

function pricingPanel(price) {
  const card = el("section", "card setting stack pricing-settings");
  card.id = "pricing-settings";
  if (!price) return card;
  const source = pricingSources[price.source];
  card.append(el("h2", "", "价格表"));
  const top = el("div", "pricing-row");
  const meta = [source.name];
  if (price.version) meta.push("版本 " + price.version);
  if (price.updated) meta.push(price.updated + " 更新");
  if (price.models) meta.push(counts.format(price.models) + " 个模型");
  top.append(el("strong", "", meta.join(" · ")), el("span", "muted", price.origin));
  card.append(top, el("div", "sub", exists(price.success) ? "上次成功检查 " + dateText(price.success) : "尚未在线检查"));
  if (price.publication) card.append(el("div", "sub", "已向新版 B 端发布 · " + dateText(new Date(price.publication).toISOString())));
  if (price.publicationError) card.append(el("p", "warning", "B 端价表发布待重试 · " + price.publicationError));

  const busy = pricingPending || price.checking;
  const auto = el("input");
  auto.type = "checkbox";
  auto.setAttribute("role", "switch");
  auto.checked = price.auto;
  auto.disabled = busy;
  auto.addEventListener("change", () => void pricingAction("price-auto", { enabled: auto.checked },
    auto.checked ? "已开启价格表每日更新" : "已关闭价格表每日更新"));
  const label = el("label", "pricing-row pricing-section");
  label.append(document.createTextNode("每天自动更新价格表"), auto);
  card.append(label);

  const controls = el("div", "pricing-row");
  const check = button(busy ? "正在检查…" : "立即检查", () => void pricingAction("price-refresh", {}, "价格表已检查"));
  check.disabled = busy;
  const link = el("a", "", "查看价格与来源 ↗");
  link.href = source.url;
  link.target = "_blank";
  link.rel = "noopener noreferrer";
  controls.append(check, link);
  card.append(controls);

  if (price.error) card.append(el("p", "warning", price.error + "，继续使用现有价格表。"));
  if (price.missing?.length) {
    const text = price.missing.slice(0, 5).map(row => row.model + "（" + counts.format(row.count) + " 条，" + row.reason + "）");
    if (price.missing.length > 5) text.push("另有 " + (price.missing.length - 5) + " 项");
    card.append(el("p", "sub", "本机与已同步设备今日未计价 · " + text.join("、")));
  }

  const choices = el("fieldset", "pricing-choices pricing-section");
  choices.append(el("legend", "", "A 端计价来源"));
  for (const [id, option] of Object.entries(pricingSources)) {
    const radio = el("input");
    radio.type = "radio";
    radio.name = "pricing-source";
    radio.value = id;
    radio.checked = id === price.source;
    radio.disabled = busy;
    radio.addEventListener("change", () => void pricingAction("price-source", { source: id }, "A 端已按所选价格表重算"));
    const row = el("label", "check");
    row.append(radio, document.createTextNode(option.name));
    choices.append(row);
  }
  card.append(choices, repriceHelp(price));
  return card;
}

function drawPricingPanel(price) {
  const old = document.querySelector("#pricing-settings");
  if (!old) return;
  const card = pricingPanel(price);
  const details = card.querySelector("details");
  if (details) details.open = Boolean(old.querySelector("details")?.open);
  old.replaceWith(card);
}

async function refreshPriceStatus(force = true) {
  if (!current || page !== "settings" || pricingPending) return;
  try {
    const response = await fetch("/api/price-status", { cache: "no-store" });
    if (!response.ok || page !== "settings" || pricingPending) return;
    const price = await response.json();
    if (force || JSON.stringify(price) !== JSON.stringify(current.pricing)) {
      current.pricing = price;
      drawPricingPanel(price);
    }
  } catch {
    if (force) drawPricingPanel(current.pricing);
  }
}

function settings(v) {
  content.append(heading("同步设置", v.repo || "GitHub 私有仓库", v.repo ? [
    button("立即同步", () => action("sync", {}, "同步完成"), true),
  ] : []));
  const grid = el("div", "twocol");
  const setup = el("form", "card setting stack");
  const repo = input("text", v.repo, "用户名/仓库名");
  const token = input("password", "", v.repo ? "已保存，留空保留" : "仅限该私有仓库");
  setup.append(el("h2", "", "连接私有仓库"), field("GitHub 私有仓库", repo),
    field("GitHub 访问令牌", token), el("button", "btn", "保存连接"));
  setup.addEventListener("submit", event => {
    event.preventDefault();
    action("setup", { repo: repo.value, token: token.value }, "连接已保存");
  });
  const frequency = el("form", "card setting stack");
  const minutes = input("number", v.syncMinutes);
  minutes.min = "1";
  minutes.max = "1440";
  minutes.required = true;
  frequency.append(el("h2", "", "采集端同步"), field("上传间隔（分钟）", minutes),
    el("button", "btn", "保存上传频率"));
  frequency.addEventListener("submit", event => {
    event.preventDefault();
    action("frequency", { minutes: Number(minutes.value) }, "频率已保存，采集端会自动接收");
  });
  grid.append(setup, frequency);
  if (v.repo) {
    const deployment = el("form", "card setting stack");
    deployment.append(el("h2", "", "设备安装包"));
    if (v.deploymentDir) {
      deployment.append(el("p", "sub", "安装包目录"), el("p", "deploy-path", v.deploymentDir),
        ...(v.deployments || []).map(name => el("div", "sub", name + " · 已生成")),
        ...(v.paired || []).map(device => el("div", "sub", (v.deviceAliases?.[device.id] ? v.deviceAliases[device.id] + " · " : "") + device.name + " · 已接入")));
    }
    const remaining = (v.deviceTargets || []).map(device => device.id).filter(name =>
      !(v.deployments || []).includes(name) && !(v.deployments || []).includes(name + " TClash") && !(v.paired || []).some(device => device.name === name));
    if (remaining.length) {
      deployment.append(el("p", "sub", "为设备单独生成安装包。令牌仅限本私库，且必须与 A 端令牌不同；安装包含有令牌，请私下传送。"));
      const name = el("select");
      for (const item of remaining) name.append(Object.assign(el("option", "", item), { value: item }));
      const key = input("password", "", "该设备独立的仓库令牌");
      key.required = true;
      const tclash = el("button", "btn primary", "生成 TClash 安装包");
      tclash.value = "tclash";
      const legacy = el("button", "btn", "生成旧版 B 端安装包");
      legacy.value = "legacy";
      deployment.append(field("选择设备", name), field("设备访问令牌", key), tclash, legacy);
      deployment.addEventListener("submit", event => {
        event.preventDefault();
        action(event.submitter?.value === "tclash" ? "deploy-tclash-one" : "deploy-one", { name: name.value, token: key.value.trim() }, "安装包已生成");
      });
    }
    for (const device of v.paired || []) {
      if (!/^Windows [1-3]$/.test(device.name) ||
          (v.deployments || []).includes(device.name + " TClash 迁移")) continue;
      const migrate = el("button", "btn", `为 ${device.name} 生成 TClash 迁移包`);
      migrate.type = "button";
      migrate.addEventListener("click", () => action("deploy-tclash-migration", { name: device.name }, "迁移包已生成"));
      deployment.append(el("p", "sub", "迁移包在原 Windows 设备上接续已配对的 B 端。"), migrate);
    }
    if (v.paired?.length || v.deployments?.length) {
      const confirmation = el("div", "stack");
      confirmation.hidden = true;
      const reset = button("清理旧设备并重新分发", () => {
        reset.hidden = true;
        confirmation.hidden = false;
      });
      confirmation.append(
        el("p", "warning", "将清理所有旧远端设备的配对、A 端旧统计和私库当前设备统计；保留 Mac 1 数据。请先在 GitHub 撤销旧设备令牌。"),
        button("确认清理旧设备", () => action("reset-remote", { confirm: "reset-remote-devices" }, "旧设备已清理，可重新分发"), true),
        button("取消", () => { confirmation.hidden = true; reset.hidden = false; })
      );
      deployment.append(el("p", "sub", "清理前请先在 GitHub 撤销旧设备令牌；GitHub 以往提交仍可能保留旧文件。"), reset, confirmation);
    }
    grid.append(deployment);
  }
  {
    const names = el("section", "card setting stack");
    names.append(el("h2", "", "设备名称"), el("p", "sub", "修改名称不会改变设备身份或用量；留空恢复默认名称。"));
    for (const device of [{ id: "local", name: "Mac 1" }, ...(v.paired || [])]) {
      const row = el("form", "stack");
      const alias = input("text", v.deviceAliases?.[device.id], device.name);
      alias.maxLength = 120;
      row.append(field(device.name, alias), el("button", "btn", "保存名称"));
      row.addEventListener("submit", event => {
        event.preventDefault();
        action("rename-device", { id: device.id, name: alias.value }, "设备名称已保存");
      });
      names.append(row);
    }
    grid.append(names);
  }
  {
    const rules = el("form", "card setting stack");
    const active = v.currentRule;
    rules.append(el("h2", "", "B 端显示倍率"));
    if (active) {
      const factor = input("number", active.factor), budget = input("number", active.budget);
      factor.step = "any";
      budget.step = "0.01";
      factor.required = budget.required = true;
      const line = el("div", "inline");
      line.append(field("B 端默认显示系数", factor), field("参考预算（美元，非官方额度）", budget));
      rules.append(line);
      const deviceInputs = new Map();
      for (const device of v.paired || []) deviceInputs.set(device.id, input("number", active.devices?.[device.id], "留空沿用默认"));
      rules.addEventListener("submit", event => {
        event.preventDefault();
        const overrides = Object.fromEntries([...deviceInputs].filter(([, node]) => node.value !== "").map(([id, node]) => [id, Number(node.value)]));
        action("rule", { account: active.account, end: active.end, factor: Number(factor.value),
          budget: Number(budget.value), devices: overrides }, "参数已保存，下次同步下发");
      });
      for (const device of v.paired || []) rules.append(deviceScaleSetting(v, device, deviceInputs.get(device.id)));
      rules.append(el("button", "btn", "保存倍率与预算"));
    } else {
      rules.append(el("p", "sub", "读取当前账号的官方重置周期后可编辑倍率和预算。"));
      for (const device of v.paired || []) rules.append(deviceScaleSetting(v, device));
    }
    grid.append(rules);
  }
  const notices = el("section", "card setting stack");
  const enabled = el("input");
  enabled.type = "checkbox";
  enabled.checked = v.noticesEnabled;
  enabled.disabled = v.noticePermission === "unavailable";
  const noticeSwitch = el("label", "check");
  noticeSwitch.append(enabled, document.createTextNode("启用设备通知"));
  enabled.addEventListener("change", async () => {
    const value = enabled.checked;
    enabled.disabled = true;
    if (await action("desktop-notices", { enabled: value }, value ? "设备通知已启用" : "设备通知已暂停", undefined, false)) {
      current.noticesEnabled = value;
    } else enabled.checked = !value;
    enabled.disabled = false;
    void refreshNoticePermission();
  });
  const permission = el("p", "sub", permissionText[v.noticePermission] ?? "");
  permission.id = "notice-permission";
  notices.append(el("h2", "", "设备通知"), noticeSwitch, permission);
  grid.append(notices, pricingPanel(v.pricing));
  content.append(grid, el("p", "status-line", "上次同步 " + dateText(v.lastSync)));
}

function deviceScaleSetting(v, device, override) {
  const row = el("div", "scale-setting");
  const alias = v.deviceAliases?.[device.id];
  const title = alias ? alias + " · " + device.name : device.name;
  if (override) {
    override.step = "any";
    row.append(field(title, override));
  } else row.append(el("span", "sub", title));
  const scale = el("input");
  scale.type = "checkbox";
  scale.setAttribute("role", "switch");
  scale.setAttribute("aria-label", title + "在 A 端使用设定倍率");
  scale.checked = Boolean(v.scaledDevices?.[device.id]);
  scale.addEventListener("change", async () => {
    const value = scale.checked;
    scale.disabled = true;
    if (!await setDeviceScale(device.id, value, false)) scale.checked = !value;
    scale.disabled = false;
  });
  const control = el("label", "check scale-control");
  control.append(scale, document.createTextNode("A 端按设定倍率显示"));
  row.append(control);
  return row;
}

async function refreshNoticePermission() {
  const note = document.querySelector("#notice-permission");
  if (!note) return;
  try {
    const response = await fetch("/api/notice-status");
    if (!response.ok) return;
    const result = await response.json();
    note.textContent = permissionText[result.permission] ?? "";
  } catch {}
}

function draw() {
  if (!current) return;
  const v = current;
  const requestScroll = document.querySelector("#request-scroll")?.scrollLeft;
  document.querySelector("#role-label").textContent = v.role === "a" ? "A 端" : v.role === "b" ? "B 端" : "";
  content.replaceChildren();
  tabs.replaceChildren();
  if (!v.role) {
    content.append(heading("开始使用", "在主控 Mac 上开启 A 端。采集端使用 A 端生成的安装包。",
      [button("开启 A 端", () => action("role", { role: "a" }, "A 端已开启"), true)]));
    return;
  }
  const entries = [["overview", v.role === "a" ? "总览" : "本机用量"]];
  if (v.role === "a") entries.push(["detail", "明细"], ["settings", "同步设置"]);
  for (const [id, name] of entries) {
    const item = el("button", "", name);
    if (id === page) item.setAttribute("aria-current", "page");
    item.addEventListener("click", () => go(id));
    tabs.append(item);
  }
  if (v.status) content.append(el("p", "status-line warning", v.status));
  if (page !== "settings" && !exists(v.readAt) && !v.status) {
    content.append(heading("正在读取本机日志", "首次打开时需要读取近 90 天的记录。"));
    return;
  }
  if (page === "detail" && v.role === "a") details(v);
  else if (page === "settings") settings(v);
  else overview(v);
  if (requestScroll) {
    const table = document.querySelector("#request-scroll");
    if (table) table.scrollLeft = requestScroll;
  }
}

void load();
setInterval(() => {
  if (!loading && page !== "settings" && current?.role) void load(true);
  else if (page === "settings") {
    void refreshNoticePermission();
    void refreshPriceStatus(false);
  }
}, 5000);
