"use strict";

const content = document.querySelector("#content");
const tabs = document.querySelector("#tabs");
const status = document.querySelector("#notice");
const filters = { cache: "1" };
const counts = new Intl.NumberFormat("zh-CN");
const dollars = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: 2 });
const shortDate = new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });
const longDate = new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
let page = "overview";
let current;
let loading = false;
let serial = 0;
let noticeTimer;

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
    if (onlyChanged && content.contains(document.activeElement) &&
        ["SELECT", "INPUT", "TEXTAREA"].includes(document.activeElement.tagName)) return;
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

async function action(name, body = {}, message = "已更新", after) {
  if (loading) return;
  loading = true;
  show("正在处理…");
  try {
    const response = await fetch("/api/action/" + name, {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "操作失败");
    if (after) after();
    await load();
    show(message);
  } catch (err) {
    show(err.message, true);
  } finally {
    loading = false;
  }
}

function go(next) {
  page = next;
  if (next === "overview" && (filters.device || filters.project || filters.model || filters.session)) {
    filters.device = filters.project = filters.model = filters.session = filters.page = "";
    void load();
    return;
  }
  draw();
}

function choose(name, value) {
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

function estimate(g, v) {
  if (!v.quota || !v.rule || !g?.estimated || v.account === "all" || v.account === "unassigned") return null;
  return g.estimateUsd / v.rule.budget * 100;
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

function deviceModels(card, v) {
  const models = el("div", "device-models");
  const rows = card.models || [];
  if (!rows.length) {
    models.append(el("p", "sub", "该周期暂无用量"));
    return models;
  }
  const modelRow = model => {
    const row = el("div", "device-model");
    const share = estimate(model, v);
    row.append(el("span", "item-name", model.name), el("span", "num", counts.format(amount(model, v.cache))),
      el("span", "money", money(model.count > model.unpriced ? model.usd : null)), el("span", "sub", percent(share)),
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

function deviceCards(v) {
  const key = "codex-usage-updates:" + (v.repo || "local") + ":" + v.window + ":" + v.account;
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
    const item = el("section", "card device-card" + (unread ? " device-updated" : ""));
    const header = el("div", "device-head");
    const name = el("div", "device-name");
    name.append(el("h2", "", card.name));
    if (card.simulated) name.append(el("span", "simulation-badge", "模拟"));
    header.append(name);
    if (unread) header.append(el("span", "device-alert", delta > 0 ? "+" + counts.format(delta) + " Token" : "用量已更新"));
    item.append(header);
    if (card.pending) {
      item.append(el("p", "sub", "等待真实设备接入"));
      grid.append(item);
      continue;
    }
    const numbers = el("div", "device-total");
    numbers.append(el("strong", "num", counts.format(amount(card.total, v.cache))), el("span", "sub", "Token 合计"));
    item.append(numbers);
    const share = estimate(card.total, v);
    const meta = [money(card.total.count > card.total.unpriced ? card.total.usd : null),
      counts.format(card.total.count) + " 条请求"];
    item.append(el("p", "sub device-meta", meta.join(" · ")));
    const quota = el("div", "device-quota");
    const caption = el("div", "device-quota-caption");
    const quotaAmount = share == null ? "—" : money(card.total.estimateUsd) + " / " + dollars.format(v.rule.budget);
    caption.append(el("span", "sub", "参考预算折算"), el("span", "sub num", quotaAmount), el("strong", "num", percent(share)));
    quota.append(caption, quotaProgress(card.name + " 自定义预算占比", share));
    item.append(quota, deviceModels(card, v));
    const footer = el("div", "device-foot");
    footer.append(el("span", "muted", exists(card.updated) ? "数据 " + dateText(card.updated, true) : "暂无数据时间"),
      button("查看明细", () => {
        next[card.id].unread = false;
        deviceHistory.set(key, next);
        try { localStorage.setItem(key, JSON.stringify(next)); } catch {}
        page = "detail";
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
  const note = speed.rate == null ? "等待可计算的本机记录" :
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
  content.append(el("p", "simulation-notice", "官方额度是账号整体读数。设备、模型和项目的美元及百分比是参考价格与自定义预算的折算，不代表官方扣费或真实额度占比。"));
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
  const localShare = estimate(local, v);
  quota.append(el("div", "foot kpi-local", "本机参考预算折算 " +
    (localShare == null ? "—" : money(local.estimateUsd) + " · " + percent(localShare))));
  kpis.append(
    kpi(v.account === "all" ? "全部账号日志 Token" : v.account === "unassigned" ? "待确认日志 Token" : "所选账号已归属 Token", counts.format(amount(t, v.cache)), counts.format(t.count) + " 条请求"),
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

function effortSummary(v) {
  const section = el("section", "card table-card effort-card");
  section.id = "effort-summary";
  section.tabIndex = -1;
  section.setAttribute("aria-labelledby", "effort-title");
  const head = el("div", "table-head");
  const title = el("h2", "", filters.model + " · 思考深度");
  title.id = "effort-title";
  head.append(title);
  section.append(head);
  const table = el("table"), thead = el("thead"), header = el("tr"), body = el("tbody");
  ["思考深度", "实际 Token", "请求", "参考美元", "自定义预算占比"].forEach((name, i) => header.append(el("th", i === 0 ? "text" : "", name)));
  thead.append(header);
  for (const effort of v.efforts) {
    const row = el("tr");
    const share = estimate(effort, v);
    row.append(el("td", "text", effort.name), el("td", "num", counts.format(amount(effort, v.cache))),
      el("td", "num", counts.format(effort.count)), el("td", "money", money(effort.count > effort.unpriced ? effort.usd : null)),
      el("td", "num", share == null ? "—" : money(effort.estimateUsd) + " · " + percent(share)));
    body.append(row);
  }
  table.append(thead, body);
  const scroll = el("div", "table-scroll");
  scroll.append(table);
  section.append(scroll);
  return section;
}

function details(v) {
  content.append(heading("用量明细", "选择设备和周期，按项目、模型或会话查看逐次请求。",
    [button("清除明细筛选", () => {
      filters.project = filters.model = filters.session = "";
      filters.page = "";
      void load();
    })]), usageControls(v, true), el("p", "simulation-notice", "美元及百分比按参考价格和自定义预算计算，不是官方扣费或真实额度占比。"));
  if (v.simulatedDevice) content.append(el("p", "simulation-notice", "当前查看的是模拟设备数据，未计入真实汇总。"));
  if (v.account === "unassigned") content.append(el("p", "simulation-warning", "本机待确认请求可逐条选择账号并确认；请只确认你能核实的记录。其他设备的待确认记录暂不能在这里修改。"));
  const totals = el("div", "kpis");
  totals.append(
    kpi("实际 Token", counts.format(amount(v.total, v.cache)), counts.format(v.count) + " 条请求"),
    kpi("输入与缓存", counts.format(v.total.input), "另有 " + counts.format(v.total.cache) + " 缓存 Token"),
    kpi("输出 Token", counts.format(v.total.output), ""),
    kpi("参考美元估算", money(v.count > v.total.unpriced ? v.total.usd : null), v.quota ? "自定义预算占比 " + percent(estimate(v.total, v)) : ""),
  );
  content.append(totals);
  if (filters.model && v.efforts?.length) content.append(effortSummary(v));
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
    const row = el("tr");
    for (const value of [dateText(r.at), r.deviceName, r.projectName]) {
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
    row.append(cell, modelCell);
    for (const value of [r.input, r.output, r.cache]) row.append(el("td", "num", counts.format(value)));
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
    row.append(el("td", "money", money(r.priced ? r.usd : null)), attribution);
    body.append(row);
  }
  table.append(thead, body);
  const scroll = el("div", "table-scroll");
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
        ...(v.deployments || []).map(name => el("div", "sub", name + ".zip · 已生成")),
        ...(v.paired || []).map(device => el("div", "sub", device.name + " · 已接入")));
    }
    const remaining = ["Windows 1", "Windows 2", "Mac 2", "Mac 3", "Mac 4"].filter(name =>
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
      if (!["Windows 1", "Windows 2"].includes(device.name) ||
          (v.deployments || []).includes(device.name + " TClash 迁移")) continue;
      const migrate = el("button", "btn", `为 ${device.name} 生成 TClash 迁移包`);
      migrate.type = "button";
      migrate.addEventListener("click", () => action("deploy-tclash-migration", { name: device.name }, "迁移包已生成"));
      deployment.append(el("p", "sub", "迁移包在原 Windows 设备上接续已配对的 B 端。"), migrate);
    }
    grid.append(deployment);
  }
  {
    const rules = el("form", "card setting stack");
    const active = v.windows?.find(w => w.id === v.window && w.current);
    rules.append(el("h2", "", "本周期展示参数"));
    if (active) {
      const factor = input("number", v.rule.factor), budget = input("number", v.rule.budget);
      factor.step = "any";
      budget.step = "0.01";
      factor.required = budget.required = true;
      const line = el("div", "inline");
      line.append(field("默认系数", factor), field("参考预算（美元，非官方额度）", budget));
      rules.append(line);
      const deviceInputs = new Map();
      for (const device of v.paired || []) {
        const override = input("number", v.rule.devices?.[device.id], "留空沿用默认");
        override.step = "any";
        rules.append(field(device.name + " · 系数", override));
        deviceInputs.set(device.id, override);
      }
      rules.append(el("button", "btn", "保存展示参数"));
      rules.addEventListener("submit", event => {
        event.preventDefault();
        const overrides = Object.fromEntries([...deviceInputs].filter(([, node]) => node.value !== "").map(([id, node]) => [id, Number(node.value)]));
        action("rule", { account: active.account, end: active.end, factor: Number(factor.value),
          budget: Number(budget.value), devices: overrides }, "参数已保存，下次同步下发");
      });
    } else {
      rules.append(el("p", "sub", "在总览选择正在进行的重置周期后调整。"));
    }
    grid.append(rules);
  }
  content.append(grid, el("p", "status-line", "上次同步 " + dateText(v.lastSync)));
}

function draw() {
  if (!current) return;
  const v = current;
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
}

void load();
setInterval(() => {
  if (!loading && page !== "settings" && current?.role) void load(true);
}, 5000);
