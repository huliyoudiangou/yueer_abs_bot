const tg = window.Telegram && window.Telegram.WebApp ? window.Telegram.WebApp : null;
if (tg) {
  tg.ready();
  tg.expand();
  document.documentElement.style.setProperty("--tg-bg", tg.backgroundColor || "");
}

const app = {
  tab: "fields",
  state: null,
  busy: false,
  busyAction: null,
  loading: false,
  loadError: "",
  toolMode: "inspect",
  selectedPlotNo: null,
  selectedSeedKey: null,
  selectedHerbKey: null,
  selectedRecipeKey: null,
  seedShelfMode: "all",
  herbShelfMode: "all",
  recipeShelfMode: "all",
  tabMotion: "none",
  motionTimer: null,
  lastAction: null,
  batchAction: null,
  renderQueued: false,
  nodes: new Map(),
  dirty: { structure: true, plots: true, dock: true, owner: true, summary: true },
  offline: false,
  offlineMode: false,
  usingCache: false,
  clockTimer: null,
  silentTimer: null,
  retryTimer: null,
  retryCount: 0,
  initData: tg ? tg.initData : "",
};

const tabMeta = {
  fields: { icon: uiIcon("field"), label: "灵田", count: () => app.state ? app.state.counts.readyPlots : 0 },
  seeds: { icon: uiIcon("seed"), label: "种子", count: () => app.state ? app.state.counts.seedInventory : 0 },
  herbs: { icon: uiIcon("herb"), label: "药草", count: () => app.state ? app.state.counts.herbInventory : 0 },
  market: { icon: uiIcon("market"), label: "药铺", count: () => app.state ? app.state.market.filter((offer) => offer.left > 0).length : 0 },
  recipes: { icon: uiIcon("recipe"), label: "丹方", count: () => app.state ? app.state.counts.recipeUnlocked : 0 },
};

const maxGardenPlots = 6;
const tabOrder = ["fields", "seeds", "herbs", "market", "recipes"];
const gardenStateCacheKey = "garden_snapshot";
const gardenSnapshotMaxAgeMs = 300000;
const gardenApiTimeoutMs = 8000;
const gardenApiRetryCount = 2;
const writeActions = new Set(["harvest-all", "open-plot", "buy-seed", "plant", "plant-all", "harvest", "sell-one", "sell-custom", "buy-recipe", "alchemy"]);
const localMockEnabled = isLocalDevHost() && new URLSearchParams(window.location.search).get("mock") === "1";

const content = document.querySelector("#content");
const statusBar = document.querySelector("#statusBar");
const pointsEl = document.querySelector("#points");
const plotCountEl = document.querySelector("#plotCount");
const readyCountEl = document.querySelector("#readyCount");
const gardenPulseEl = document.querySelector("#gardenPulse");
const bottomDock = document.querySelector("#bottomDock");
const ownerPanel = document.querySelector("#ownerPanel");
const offlineBanner = document.querySelector("#offlineBanner");
const refreshBtn = document.querySelector("#refreshBtn");

if (refreshBtn) refreshBtn.addEventListener("click", () => loadState());
if (bottomDock) {
  bottomDock.addEventListener("click", (event) => {
    const button = event.target.closest("[data-tab]");
    if (!button) return;
    switchTab(button.dataset.tab);
    haptic("selection");
  });
}
content.addEventListener("click", handleContentClick);
if (ownerPanel) ownerPanel.addEventListener("click", handleOwnerPanelClick);
document.addEventListener("pointerdown", handleTapFeedback, { passive: true });
document.addEventListener("visibilitychange", handleGardenVisibilityChange);

initializeStaticLeaveKeys();
loadState();
startGardenTimers();
startFPSMonitor();

function initializeStaticLeaveKeys() {
  if (pointsEl) pointsEl.dataset.leave = "points";
  if (plotCountEl) plotCountEl.dataset.leave = "plot-count";
  if (readyCountEl) readyCountEl.dataset.leave = "ready-count";
  cacheLeaveNodes();
}

function startFPSMonitor() {
  if (window.location.hash !== "#fps") return;
  let lastTime = performance.now();
  let frames = 0;
  function fpsLoop() {
    frames += 1;
    const now = performance.now();
    if (now - lastTime >= 1000) {
      console.log(`FPS: ${frames}`);
      frames = 0;
      lastTime = now;
    }
    window.requestAnimationFrame(fpsLoop);
  }
  window.requestAnimationFrame(fpsLoop);
}

function startGardenTimers() {
  stopGardenTimers();
  if (document.hidden) return;
  app.clockTimer = window.setInterval(tickGardenClock, 1000);
  app.silentTimer = window.setInterval(() => {
    if (app.state && !app.busy) loadState({ silent: true });
  }, 30000);
}

function stopGardenTimers() {
  if (app.clockTimer) {
    window.clearInterval(app.clockTimer);
    app.clockTimer = null;
  }
  if (app.silentTimer) {
    window.clearInterval(app.silentTimer);
    app.silentTimer = null;
  }
}

function handleGardenVisibilityChange() {
  if (document.hidden) {
    stopGardenTimers();
    if (app.retryTimer) {
      window.clearTimeout(app.retryTimer);
      app.retryTimer = null;
    }
    return;
  }
  startGardenTimers();
  if (app.state) {
    loadState({ silent: true });
  }
}

async function loadState(options = {}) {
  if (!options.silent) {
    app.loading = true;
    app.loadError = "";
    app.dirty.structure = true;
    render();
  }
  if (!app.initData) {
    if (localMockEnabled) {
      app.state = mockGardenState();
      app.offline = false;
      app.offlineMode = false;
      app.usingCache = false;
      app.loadError = "";
      app.loading = false;
      ensureSelections();
      setStatus("本地药园 Mock 已启用，仅用于前端调试");
      app.dirty.structure = true;
      render();
      return;
    }
    const message = "请在 Telegram 私聊发送「药园」后点击「打开药园」重新打开";
    app.loading = false;
    app.loadError = message;
    if (!options.silent) {
      setStatus(message, true);
      render();
    }
    return;
  }
  if (!options.silent) setStatus("同步中");
  try {
    const previousPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
    const wasOffline = app.offlineMode || app.usingCache || app.offline;
    const payload = await api("/api/garden/state", { method: "GET" });
    app.state = requireGardenStatePayload(payload);
    app.offline = false;
    app.offlineMode = false;
    app.usingCache = false;
    app.loadError = "";
    saveGardenStateCache(app.state);
    hideOfflineBanner();
    app.retryCount = 0;
    ensureSelections();
    if (!options.silent || wasOffline) setStatus("");
    if (!options.silent) app.loading = false;
    markStateDirty(previousPlotCount);
    if (options.silent && canPatchCurrentView()) {
      patchState();
    } else {
      scheduleRender("state");
    }
  } catch (error) {
    const snap = loadGardenStateCache();
    if (snap) {
      const previousPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
      app.state = snap;
      app.offline = true;
      app.offlineMode = true;
      app.usingCache = true;
      app.loadError = "";
      ensureSelections();
      showOfflineBanner("数据来自本地快照，正在重连后端");
      if (!options.silent) app.loading = false;
      markStateDirty(previousPlotCount);
      if (options.silent && canPatchCurrentView()) {
        patchState();
      } else {
        render();
      }
      scheduleRetry();
      return;
    } else if (!options.silent) {
      app.loadError = error.message || "药园读取失败";
      setStatus(app.loadError, true);
      app.loading = false;
      scheduleRender("error");
    }
  } finally {
    if (!options.silent) app.loading = false;
  }
}

async function runAction(path, body, fallback) {
  if (app.busy) {
    setStatus("上一道园务还在处理，稍候再点", true);
    haptic("error");
    return;
  }
  if (app.usingCache || app.offline || app.offlineMode) {
    setStatus("当前显示的是离线园况，重连后才能提交操作", true);
    haptic("error");
    return;
  }
  app.busy = true;
  app.batchAction = buildBatchAction(path, body);
  app.busyAction = {
    kind: actionKind(path),
    label: actionBusyText(path),
  };
  setStatus("处理中");
  haptic("impact");
  app.dirty.structure = true;
  render();
  try {
    const previousPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
    const payload = await api(path, {
      method: "POST",
      body: JSON.stringify(body || {}),
    });
    if (!payload.state) {
      markCommittedActionNeedsSync(payload, fallback, actionKind(path), body);
      return;
    }
    app.state = requireGardenStatePayload(payload);
    app.offline = false;
    app.offlineMode = false;
    app.usingCache = false;
    saveGardenStateCache(app.state);
    hideOfflineBanner();
    app.retryCount = 0;
    ensureSelections();
    app.lastAction = {
      kind: actionKind(path),
      plotNo: body && body.plotNo ? Number(body.plotNo) : null,
      seedKey: body && body.seedKey ? body.seedKey : "",
      recipeKey: body && body.recipeKey ? body.recipeKey : "",
      at: Date.now(),
    };
    setStatus(payload.message || fallback || "已完成");
    showActionBurst(payload.message || fallback || "已完成", actionKind(path));
    haptic("success");
    markStateDirty(previousPlotCount);
    app.dirty.structure = true;
  } catch (error) {
    setStatus(error.message || "操作失败", true);
    haptic("error");
    app.dirty.structure = true;
  } finally {
    app.busy = false;
    app.busyAction = null;
    render();
  }
}

async function runHarvestAllAction() {
  const readyPlots = app.state ? app.state.plots.filter((plot) => plot.status === "ready") : [];
  if (readyPlots.length === 0) {
    setStatus("暂无成熟药草", true);
    haptic("error");
    return;
  }
  app.busy = true;
  app.busyAction = {
    kind: "harvest",
    label: actionBusyText("/api/garden/harvest-all"),
  };
  setStatus("收获中");
  haptic("impact");
  let shouldRenderAfterFinish = false;
  try {
    const previousPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
    const apiPromise = api("/api/garden/harvest-all", {
      method: "POST",
      body: JSON.stringify({}),
    });
    const fxPromise = playHarvestAllSequence(readyPlots);
    const [payload] = await Promise.all([apiPromise, fxPromise]);
    if (!payload.state) {
      markCommittedActionNeedsSync(payload, "一键收获完成", "harvest", null);
      shouldRenderAfterFinish = true;
      return;
    }
    app.state = requireGardenStatePayload(payload);
    app.offline = false;
    app.offlineMode = false;
    app.usingCache = false;
    saveGardenStateCache(app.state);
    hideOfflineBanner();
    app.retryCount = 0;
    ensureSelections();
    app.lastAction = {
      kind: "harvest",
      plotNo: null,
      seedKey: "",
      recipeKey: "",
      at: Date.now(),
    };
    setStatus(payload.message || "一键收获完成");
    showActionBurst(payload.message || "一键收获完成", "harvest");
    haptic("success");
    markStateDirty(previousPlotCount);
    if (canPatchCurrentView()) {
      patchState();
    } else {
      app.dirty.structure = true;
      render();
    }
  } catch (error) {
    setStatus(error.message || "操作失败", true);
    haptic("error");
    app.dirty.structure = true;
    shouldRenderAfterFinish = true;
  } finally {
    app.busy = false;
    app.busyAction = null;
    if (shouldRenderAfterFinish) render();
  }
}

function markCommittedActionNeedsSync(payload, fallback, kind, body) {
  const message = payload.message || fallback || "操作已完成，正在重新同步园况";
  app.offline = true;
  app.offlineMode = true;
  app.usingCache = false;
  app.loadError = "";
  app.lastAction = {
    kind,
    plotNo: body && body.plotNo ? Number(body.plotNo) : null,
    seedKey: body && body.seedKey ? body.seedKey : "",
    recipeKey: body && body.recipeKey ? body.recipeKey : "",
    at: Date.now(),
  };
  setStatus(message, true);
  showActionBurst(message, kind);
  haptic("success");
  showOfflineBanner(message);
  scheduleRetry();
  app.dirty.structure = true;
}

function playHarvestAllSequence(plots) {
  const tasks = plots.map((plot, index) => new Promise((resolve) => {
    window.setTimeout(() => {
      const tileEl = content.querySelector(`[data-leave="plot-${plot.plotNo}"]`);
      if (!tileEl) {
        resolve();
        return;
      }
      tileEl.classList.add("batch-preview", "batch-harvest");
      tileEl.insertAdjacentHTML("beforeend", renderTileActionFx("harvest"));
      window.setTimeout(() => {
        tileEl.querySelectorAll(".tile-fx").forEach((node) => node.remove());
        tileEl.classList.remove("batch-preview", "batch-harvest");
        resolve();
      }, 920);
    }, index * 120);
  }));
  return Promise.all(tasks);
}

async function api(path, options = {}) {
  return retryWithBackoff(path, options);
}

function requireGardenStatePayload(payload) {
  if (!payload || !isGardenStatePayload(payload.state)) {
    throw new Error("园况数据异常，请稍后再试");
  }
  return payload.state;
}

function isGardenStatePayload(state) {
  return !!(
    state &&
    typeof state === "object" &&
    isGardenNonNegativeInteger(state.points) &&
    isGardenStateCounts(state.counts) &&
    (state.nextPlot === undefined || state.nextPlot === null || isGardenStateNextPlot(state.nextPlot)) &&
    Array.isArray(state.plots) &&
    Array.isArray(state.seeds) &&
    Array.isArray(state.herbs) &&
    Array.isArray(state.recipes) &&
    Array.isArray(state.market) &&
    state.plots.every(isGardenStatePlot) &&
    state.seeds.every(isGardenStateSeed) &&
    state.herbs.every(isGardenStateHerb) &&
    state.recipes.every(isGardenStateRecipe) &&
    state.market.every(isGardenStateMarketOffer)
  );
}

function isGardenNonNegativeInteger(value) {
  return Number.isInteger(value) && value >= 0;
}

function isGardenPositivePlotNo(value) {
  return Number.isInteger(value) && value >= 1 && value <= maxGardenPlots;
}

function isGardenString(value) {
  return typeof value === "string";
}

function isGardenBoolean(value) {
  return typeof value === "boolean";
}

function isGardenPlotStatus(value) {
  return value === "empty" || value === "growing" || value === "ready";
}

function isGardenStateCounts(counts) {
  return !!(
    counts &&
    typeof counts === "object" &&
    isGardenNonNegativeInteger(counts.plots) &&
    isGardenNonNegativeInteger(counts.readyPlots) &&
    isGardenNonNegativeInteger(counts.seedInventory) &&
    isGardenNonNegativeInteger(counts.herbInventory) &&
    isGardenNonNegativeInteger(counts.recipeUnlocked)
  );
}

function isGardenStateNextPlot(nextPlot) {
  return !!(
    nextPlot &&
    typeof nextPlot === "object" &&
    isGardenPositivePlotNo(nextPlot.plotNo) &&
    isGardenNonNegativeInteger(nextPlot.cost)
  );
}

function isGardenStatePlot(plot) {
  return !!(
    plot &&
    typeof plot === "object" &&
    isGardenPositivePlotNo(plot.plotNo) &&
    isGardenPlotStatus(plot.status) &&
    (plot.seedKey === undefined || isGardenString(plot.seedKey)) &&
    (plot.herbName === undefined || isGardenString(plot.herbName)) &&
    (plot.remainingSeconds === undefined || isGardenNonNegativeInteger(plot.remainingSeconds))
  );
}

function isGardenStateSeed(seed) {
  return !!(
    seed &&
    typeof seed === "object" &&
    isGardenString(seed.key) &&
    isGardenString(seed.seedName) &&
    isGardenString(seed.herbName) &&
    isGardenString(seed.growText) &&
    isGardenString(seed.yieldText) &&
    isGardenNonNegativeInteger(seed.price) &&
    isGardenNonNegativeInteger(seed.growSeconds) &&
    isGardenNonNegativeInteger(seed.dailyLimit) &&
    isGardenNonNegativeInteger(seed.leftToday) &&
    isGardenNonNegativeInteger(seed.inventory) &&
    isGardenBoolean(seed.purchasable)
  );
}

function isGardenStateHerb(herb) {
  return !!(
    herb &&
    typeof herb === "object" &&
    isGardenString(herb.key) &&
    isGardenString(herb.herbName) &&
    isGardenNonNegativeInteger(herb.inventory) &&
    isGardenNonNegativeInteger(herb.basePrice) &&
    isGardenNonNegativeInteger(herb.marketPrice) &&
    isGardenNonNegativeInteger(herb.marketLimit) &&
    isGardenNonNegativeInteger(herb.marketLeft) &&
    isGardenBoolean(herb.urgent) &&
    isGardenBoolean(herb.sellable)
  );
}

function isGardenStateRecipe(recipe) {
  return !!(
    recipe &&
    typeof recipe === "object" &&
    isGardenString(recipe.key) &&
    isGardenString(recipe.name) &&
    isGardenString(recipe.productName) &&
    isGardenNonNegativeInteger(recipe.unlockPrice) &&
    isGardenNonNegativeInteger(recipe.alchemyCost) &&
    isGardenNonNegativeInteger(recipe.productInventory) &&
    isGardenBoolean(recipe.unlocked) &&
    (recipe.effect === undefined || isGardenString(recipe.effect)) &&
    Array.isArray(recipe.materials) &&
    recipe.materials.every(isGardenStateRecipeMaterial)
  );
}

function isGardenStateRecipeMaterial(material) {
  return !!(
    material &&
    typeof material === "object" &&
    isGardenString(material.itemName) &&
    isGardenNonNegativeInteger(material.need) &&
    isGardenNonNegativeInteger(material.owned) &&
    isGardenBoolean(material.enough)
  );
}

function isGardenStateMarketOffer(offer) {
  return !!(
    offer &&
    typeof offer === "object" &&
    isGardenString(offer.seedKey) &&
    isGardenString(offer.herbName) &&
    isGardenNonNegativeInteger(offer.price) &&
    isGardenNonNegativeInteger(offer.limit) &&
    isGardenNonNegativeInteger(offer.sold) &&
    isGardenNonNegativeInteger(offer.left)
  );
}

async function retryWithBackoff(path, options = {}) {
  let lastError = null;
  for (let attempt = 0; attempt <= gardenApiRetryCount; attempt += 1) {
    if (attempt > 0) setStatus("重连中", true);
    const controller = new AbortController();
    const timeoutId = window.setTimeout(() => controller.abort(), gardenApiTimeoutMs);
    try {
      const response = await fetch(path, {
        ...options,
        signal: controller.signal,
        headers: {
          "Content-Type": "application/json",
          "X-Telegram-Init-Data": app.initData,
          ...(options && options.headers ? options.headers : {}),
        },
      });
      let payload = null;
      try {
        payload = await response.json();
      } catch (_) {
        const message = "响应格式异常，请稍后再试";
        if (response.status >= 500 && attempt < gardenApiRetryCount) {
          lastError = new Error(message);
          await wait(gardenRetryDelay(attempt));
          continue;
        }
        throw new Error(message);
      }
      if (!payload || typeof payload !== "object" || !response.ok || payload.ok !== true) {
        const message = payload && payload.message ? payload.message : "请求失败";
        if (response.status >= 500 && attempt < gardenApiRetryCount) {
          lastError = new Error(message);
          await wait(gardenRetryDelay(attempt));
          continue;
        }
        throw new Error(message);
      }
      return payload;
    } catch (error) {
      lastError = error;
      const canRetry = error && (error.name === "AbortError" || error instanceof TypeError);
      if (!canRetry || attempt >= gardenApiRetryCount) break;
      await wait(gardenRetryDelay(attempt));
    } finally {
      window.clearTimeout(timeoutId);
    }
  }
  throw lastError || new Error("请求失败");
}

function wait(ms) {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function gardenRetryDelay(attempt) {
  return 1500;
}

function showOfflineBanner(message) {
  if (!offlineBanner) return;
  const detail = offlineBanner.querySelector("em");
  if (detail && message) detail.textContent = message;
  offlineBanner.hidden = false;
  setStatus(message || "数据来自本地快照，正在重连后端", true);
}

function hideOfflineBanner() {
  if (offlineBanner) offlineBanner.hidden = true;
  if (app.retryTimer) {
    window.clearTimeout(app.retryTimer);
    app.retryTimer = null;
  }
  app.retryCount = 0;
}

function scheduleRetry() {
  if (app.retryTimer || document.hidden) return;
  app.retryCount = (app.retryCount || 0) + 1;
  const delay = Math.min(30000, 3000 * app.retryCount);
  app.retryTimer = window.setTimeout(async () => {
    app.retryTimer = null;
    try {
      setStatus("重连中", true);
      const previousPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
      const payload = await api("/api/garden/state", { method: "GET" });
      app.state = requireGardenStatePayload(payload);
      app.offline = false;
      app.offlineMode = false;
      app.usingCache = false;
      app.loadError = "";
      saveGardenStateCache(app.state);
      hideOfflineBanner();
      ensureSelections();
      markStateDirty(previousPlotCount);
      render();
      setStatus("");
    } catch (_) {
      scheduleRetry();
    }
  }, delay);
}

function scheduleRender(reason) {
  if (app.renderQueued) return;
  app.renderQueued = true;
  const frame = window.requestAnimationFrame || ((callback) => window.setTimeout(callback, 16));
  frame(() => {
    app.renderQueued = false;
    render(reason);
  });
}

function gardenLocalStorage() {
  try {
    return window.localStorage || null;
  } catch (_) {
    return null;
  }
}

function saveGardenStateCache(state) {
  const storage = gardenLocalStorage();
  if (!storage || !isGardenStatePayload(state)) return;
  try {
    storage.setItem(gardenStateCacheKey, JSON.stringify({
      state,
      savedAt: Date.now(),
    }));
  } catch (_) {
    // localStorage may be blocked in some embedded browsers.
  }
}

function loadGardenStateCache() {
  const storage = gardenLocalStorage();
  if (!storage) return null;
  try {
    const raw = storage.getItem(gardenStateCacheKey);
    if (!raw) return null;
    const cached = JSON.parse(raw);
    if (!cached || !cached.state) return null;
    if (Date.now() - Number(cached.savedAt || 0) >= gardenSnapshotMaxAgeMs) return null;
    const normalized = normalizeCachedGardenState(cached.state, cached.savedAt);
    if (!isGardenStatePayload(normalized)) return null;
    return normalized;
  } catch (_) {
    return null;
  }
}

function normalizeCachedGardenState(state, savedAt) {
  const copy = JSON.parse(JSON.stringify(state));
  const elapsed = Math.max(0, Math.floor((Date.now() - Number(savedAt || Date.now())) / 1000));
  if (elapsed > 0 && Array.isArray(copy.plots)) {
    copy.plots.forEach((plot) => {
      if (plot.status === "growing") {
        plot.remainingSeconds = Math.max(0, Number(plot.remainingSeconds || 0) - elapsed);
        if (plot.remainingSeconds <= 0) plot.status = "ready";
      }
    });
  }
  if (copy.counts && Array.isArray(copy.plots)) {
    copy.counts.readyPlots = copy.plots.filter((plot) => plot.status === "ready").length;
  }
  copy.serverTime = new Date().toISOString();
  return copy;
}

function isLocalDevHost() {
  const host = window.location.hostname;
  return host === "localhost" || host === "127.0.0.1" || host === "::1";
}

function mockGardenState() {
  const now = new Date();
  return {
    points: 126,
    serverTime: now.toISOString(),
    counts: {
      plots: 4,
      readyPlots: 1,
      seedInventory: 7,
      herbInventory: 39,
      recipeUnlocked: 3,
    },
    nextPlot: { plotNo: 5, cost: 180 },
    plots: [
      { plotNo: 1, status: "ready", seedKey: "ninglu", seedName: "凝露草种子", herbName: "凝露草", remainingSeconds: 0, maturesAt: now.toISOString() },
      { plotNo: 2, status: "growing", seedKey: "qingling", seedName: "青灵叶种子", herbName: "青灵叶", remainingSeconds: 2880, maturesAt: new Date(now.getTime() + 2880000).toISOString() },
      { plotNo: 3, status: "growing", seedKey: "chiyang", seedName: "赤阳花种子", herbName: "赤阳花", remainingSeconds: 9600, maturesAt: new Date(now.getTime() + 9600000).toISOString() },
      { plotNo: 4, status: "empty", seedKey: "", herbName: "", remainingSeconds: 0, maturesAt: "" },
    ],
    seeds: [
      { key: "ninglu", seedName: "凝露草种子", herbName: "凝露草", inventory: 3, growSeconds: 7200, growText: "2小时", yieldText: "2-4株", purchasable: true, dailyLimit: 10, boughtToday: 2, leftToday: 8, price: 15 },
      { key: "qingling", seedName: "青灵叶种子", herbName: "青灵叶", inventory: 2, growSeconds: 14400, growText: "4小时", yieldText: "2-3株", purchasable: true, dailyLimit: 8, boughtToday: 1, leftToday: 7, price: 25 },
      { key: "chiyang", seedName: "赤阳花种子", herbName: "赤阳花", inventory: 1, growSeconds: 21600, growText: "6小时", yieldText: "1-3株", purchasable: true, dailyLimit: 6, boughtToday: 0, leftToday: 6, price: 40 },
      { key: "yuehua", seedName: "月华藤种子", herbName: "月华藤", inventory: 1, growSeconds: 28800, growText: "8小时", yieldText: "1-2株", purchasable: true, dailyLimit: 4, boughtToday: 0, leftToday: 4, price: 60 },
      { key: "xuanshen", seedName: "玄参根种子", herbName: "玄参根", inventory: 0, growSeconds: 43200, growText: "12小时", yieldText: "1-2株", purchasable: true, dailyLimit: 3, boughtToday: 0, leftToday: 3, price: 90 },
      { key: "ziyuzhi", seedName: "紫玉芝种子", herbName: "紫玉芝", inventory: 0, growSeconds: 64800, growText: "18小时", yieldText: "1株", purchasable: true, dailyLimit: 1, boughtToday: 0, leftToday: 1, price: 140 },
      { key: "longxue", seedName: "龙血果种子", herbName: "龙血果", inventory: 0, growSeconds: 86400, growText: "24小时", yieldText: "1株", purchasable: false, dailyLimit: 0, boughtToday: 0, leftToday: 0, price: 0 },
      { key: "tianxin", seedName: "天心莲种子", herbName: "天心莲", inventory: 0, growSeconds: 129600, growText: "36小时", yieldText: "1株", purchasable: false, dailyLimit: 0, boughtToday: 0, leftToday: 0, price: 0 },
    ],
    herbs: [
      { key: "ninglu", herbName: "凝露草", inventory: 14, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 4, sellable: true },
      { key: "qingling", herbName: "青灵叶", inventory: 9, urgent: true, marketLeft: 5, marketLimit: 12, marketSold: 7, marketPrice: 11, basePrice: 9, sellable: true },
      { key: "chiyang", herbName: "赤阳花", inventory: 6, urgent: true, marketLeft: 3, marketLimit: 8, marketSold: 5, marketPrice: 21, basePrice: 18, sellable: true },
      { key: "yuehua", herbName: "月华藤", inventory: 4, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 36, sellable: true },
      { key: "xuanshen", herbName: "玄参根", inventory: 3, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 54, sellable: true },
      { key: "ziyuzhi", herbName: "紫玉芝", inventory: 2, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 126, sellable: true },
      { key: "longxue", herbName: "龙血果", inventory: 1, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 80, sellable: true },
      { key: "tianxin", herbName: "天心莲", inventory: 0, urgent: false, marketLeft: 0, marketLimit: 0, marketSold: 0, marketPrice: 0, basePrice: 120, sellable: true },
    ],
    market: [
      { seedKey: "qingling", herbName: "青灵叶", limit: 12, sold: 7, left: 5, price: 11 },
      { seedKey: "chiyang", herbName: "赤阳花", limit: 8, sold: 5, left: 3, price: 21 },
    ],
    recipes: [
      { key: "juling", name: "聚灵丹方", productName: "聚灵丹", productInventory: 2, unlocked: true, unlockPrice: 20, alchemyCost: 3, effect: "服用增加丹药修为", materials: [{ itemName: "凝露草", owned: 14, need: 8, enough: true }, { itemName: "青灵叶", owned: 9, need: 5, enough: true }] },
      { key: "zhuji", name: "筑基丹方", productName: "筑基丹", productInventory: 0, unlocked: true, unlockPrice: 30, alchemyCost: 5, effect: "用于筑基突破", materials: [{ itemName: "凝露草", owned: 14, need: 3, enough: true }, { itemName: "赤阳花", owned: 6, need: 1, enough: true }] },
      { key: "jiangchen", name: "降尘丹方", productName: "降尘丹", productInventory: 1, unlocked: true, unlockPrice: 60, alchemyCost: 8, effect: "用于结丹突破", materials: [{ itemName: "赤阳花", owned: 6, need: 2, enough: true }, { itemName: "月华藤", owned: 4, need: 1, enough: true }] },
      { key: "jiuzhuan", name: "九转造化丹方", productName: "九转造化丹", productInventory: 0, unlocked: false, unlockPrice: 100, alchemyCost: 30, effect: "服用增加丹药修为", materials: [{ itemName: "青灵叶", owned: 9, need: 8, enough: true }, { itemName: "玄参根", owned: 3, need: 3, enough: true }, { itemName: "紫玉芝", owned: 2, need: 1, enough: true }] },
      { key: "jiuqu", name: "九曲灵参丹方", productName: "九曲灵参丹", productInventory: 0, unlocked: false, unlockPrice: 160, alchemyCost: 25, effect: "用于元婴突破", materials: [{ itemName: "玄参根", owned: 3, need: 2, enough: true }, { itemName: "紫玉芝", owned: 2, need: 1, enough: true }] },
      { key: "butian", name: "补天丹方", productName: "补天丹", productInventory: 0, unlocked: false, unlockPrice: 280, alchemyCost: 50, effect: "用于高阶突破", materials: [{ itemName: "龙血果", owned: 1, need: 1, enough: true }, { itemName: "天心莲", owned: 0, need: 1, enough: false }, { itemName: "紫玉芝", owned: 2, need: 1, enough: true }] },
    ],
  };
}

function setStatus(text, isError) {
  statusBar.hidden = !text;
  statusBar.textContent = text || "";
  statusBar.classList.toggle("error", Boolean(isError));
}

function render(reason = "") {
  if (app.state && !app.dirty.structure && canPatchCurrentView()) {
    patchState();
    return;
  }
  renderSummary({ force: app.dirty.structure });
  if (!app.state) {
    content.innerHTML = renderGardenPlaceholder();
    cacheLeaveNodes();
    app.dirty.structure = false;
    return;
  }
  if (app.tab === "fields") renderFields();
  if (app.tab === "seeds") renderSeeds();
  if (app.tab === "herbs") renderHerbs();
  if (app.tab === "market") renderMarket();
  if (app.tab === "recipes") renderRecipes();
  cacheLeaveNodes();
  app.dirty.structure = false;
  app.dirty.plots = false;
  app.dirty.dock = false;
  app.dirty.owner = false;
  app.dirty.summary = false;
  applyContentMotion();
}

function requestStructureRender() {
  app.dirty.structure = true;
  render();
}

function renderSummary(options = {}) {
  renderTabs(options);
  if (!app.state) {
    renderOwnerPanel();
    return;
  }
  patchSummary();
  if (gardenPulseEl) gardenPulseEl.textContent = gardenPulseText();
  renderOwnerPanel(options);
}

function renderOwnerPanel(options = {}) {
  if (!ownerPanel) return;
  if (!app.state) {
    ownerPanel.hidden = true;
    ownerPanel.innerHTML = "";
    return;
  }
  ownerPanel.hidden = false;
  if (!options.force && app.nodes.get("owner-headline")) {
    patchOwner();
    return;
  }
  const readyCount = readyPlotCount();
  const emptyCount = emptyPlotCount();
  const next = nextMaturePlot();
  const seed = selectedSeed();
  const headline = ownerPanelHeadline(readyCount, emptyCount, next);
  const action = ownerPanelAction(readyCount, emptyCount, seed);
  ownerPanel.innerHTML = `
    ${animeKeeperHTML("owner")}
    <div class="owner-copy">
      <span class="eyebrow">灵圃管家</span>
      <strong data-leave="owner-headline">${escapeHtml(headline.title)}</strong>
      <em data-leave="owner-detail">${escapeHtml(headline.detail)}</em>
    </div>
    <div class="owner-stats" aria-label="药园状态">
      <span><strong data-leave="owner-ready">${readyCount}</strong><em>可收</em></span>
      <span><strong data-leave="owner-empty">${emptyCount}</strong><em>空田</em></span>
      <span><strong data-leave="owner-seed">${seed ? seed.inventory : 0}</strong><em>当前种</em></span>
    </div>
    <button class="owner-action" type="button" data-leave="owner-action" data-action="${action.action}" ${action.plotNo ? `data-plot="${action.plotNo}"` : ""} ${action.seedKey ? `data-seed="${escapeAttr(action.seedKey)}"` : ""}>
      <strong data-leave="owner-action-label">${escapeHtml(action.label)}</strong>
      <em data-leave="owner-action-detail">${escapeHtml(action.detail)}</em>
    </button>
  `;
  cacheLeaveNodes(ownerPanel);
}

function animeKeeperHTML(variant = "owner") {
  return `
    <div class="anime-keeper anime-keeper-${variant}" aria-hidden="true">
      <span class="keeper-hair"></span>
      <span class="keeper-crown"></span>
      <span class="keeper-face">
        <i class="eye eye-left"></i>
        <i class="eye eye-right"></i>
        <i class="mouth"></i>
      </span>
      <span class="keeper-robe"></span>
      <span class="keeper-sleeve sleeve-left"></span>
      <span class="keeper-sleeve sleeve-right"></span>
      <span class="keeper-sword"></span>
      <span class="keeper-talisman"></span>
    </div>
  `;
}

function handleOwnerPanelClick(event) {
  const button = event.target.closest("[data-action]");
  if (!button || !ownerPanel.contains(button)) return;
  const action = button.dataset.action;
  if (action === "focus-plot") {
    app.selectedPlotNo = Number(button.dataset.plot);
    app.tab = "fields";
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "open-seeds") return switchTab("seeds");
  if (action === "open-herbs") return switchTab("herbs");
  if (action === "open-market") return switchTab("market");
  if (action === "open-recipes") return switchTab("recipes");
}

function ownerPanelHeadline(readyCount, emptyCount, next) {
  const offer = firstMatchedMarketOffer();
  const recipes = Array.isArray(app.state.recipes) ? app.state.recipes : [];
  const recipe = recipes.find((item) => item.unlocked && canAlchemy(item));
  if (readyCount > 0) {
    return {
      title: `${readyCount} 块灵田等收获`,
      detail: offer ? `先收成熟，仓中 ${offer.herbName} 可看急收` : "先收成熟，再补一轮灵种",
    };
  }
  if (emptyCount > 0) {
    return {
      title: `${emptyCount} 块空田待播`,
      detail: offer ? `${offer.herbName} 有急收价，播前可先处理库存` : "挑好种子后可以连续点田",
    };
  }
  if (offer) {
    return {
      title: `${offer.herbName} 正在急收`,
      detail: `剩余额度 ${offer.left}，先卖高价货`,
    };
  }
  if (recipe) {
    return {
      title: `${recipe.productName} 可开炉`,
      detail: "材料已齐，核对炉火费后可炼丹",
    };
  }
  if (next) {
    return {
      title: `${next.plotNo} 号田快成熟`,
      detail: `${formatRemaining(next.remainingSeconds)} 后可收 ${next.herbName}`,
    };
  }
  return {
    title: "园区运转平稳",
    detail: "可去药铺或丹炉看看下一步",
  };
}

function ownerPanelAction(readyCount, emptyCount, seed) {
  const readyPlot = app.state.plots.find((plot) => plot.status === "ready");
  if (readyPlot) {
    return {
      action: "focus-plot",
      plotNo: readyPlot.plotNo,
      label: "去收获",
      detail: `${readyPlot.plotNo} 号田`,
    };
  }
  const emptyPlot = app.state.plots.find((plot) => plot.status === "empty");
  if (emptyPlot && seed && seed.inventory > 0) {
    return {
      action: "focus-plot",
      plotNo: emptyPlot.plotNo,
      label: "去播种",
      detail: `${seed.seedName} x${seed.inventory}`,
    };
  }
  if (emptyCount > 0) {
    return {
      action: "open-seeds",
      label: "买种子",
      detail: "补货架",
    };
  }
  const offer = firstMatchedMarketOffer();
  if (offer) {
    return {
      action: "open-market",
      label: "看药铺",
      detail: offer.herbName,
    };
  }
  return {
    action: "open-recipes",
    label: "看丹炉",
    detail: "查材料",
  };
}

function renderGardenPlaceholder() {
  if (app.loadError) {
    return `
      <section class="farm-placeholder farm-error" aria-label="药园读取失败">
        <div class="placeholder-scene">
          ${animeKeeperHTML("placeholder")}
          <span class="placeholder-sun"></span>
          <span class="placeholder-cloud"></span>
          <strong>药园暂时没连上</strong>
          <em>${escapeHtml(app.loadError)}</em>
        </div>
        <button class="btn" type="button" data-action="retry-load">重新巡园</button>
      </section>
    `;
  }
  return `
    <section class="farm-placeholder farm-loading" aria-label="药园同步中">
      <div class="placeholder-scene">
        ${animeKeeperHTML("placeholder")}
        <span class="placeholder-sun"></span>
        <span class="placeholder-cloud"></span>
        <strong>${app.loading ? "管事正在巡园" : "等待园况"}</strong>
        <em>同步灵田、种子、药铺和丹炉状态</em>
      </div>
      <div class="field-skeleton" aria-hidden="true">
        <span></span><span></span><span></span><span></span>
      </div>
    </section>
  `;
}

function renderFields() {
  const state = app.state;
  const selected = selectedPlot();
  const activeSeed = selectedSeed();
  const emptyCount = emptyPlotCount();
  const readyCount = readyPlotCount();
  const phase = gardenPhase();
  content.innerHTML = `
    <div class="farm-stage field-first mode-${app.toolMode} phase-${phase} ${app.busy ? "busy" : ""}">
      ${renderFarmMap(state, readyCount, emptyCount)}
      ${renderFarmGuide(activeSeed, readyCount, emptyCount)}
      ${renderFarmActionDock(activeSeed, readyCount, emptyCount)}
      ${renderPlotQuickBar(selected, activeSeed)}
      <div class="tool-dock" aria-label="药园工具">
        ${renderToolButton("inspect", uiIcon("hand"), "手势")}
        ${renderToolButton("plant", uiIcon("seed"), "播种")}
        ${renderToolButton("harvest", uiIcon("harvest"), "收获")}
      </div>
      ${renderFarmBusyVeil()}
    </div>
  `;
}

function renderFarmMap(state, readyCount, emptyCount) {
  const next = nextMaturePlot();
  const yardMood = readyCount > 0 ? "map-ready" : emptyCount > 0 ? "map-empty" : "map-growing";
  const selected = selectedPlot();
  const selectedText = selected
    ? `${selected.plotNo} 号 ${selected.status === "empty" ? "空田" : selected.herbName}`
    : "未选地块";
  return `
    <section class="farm-map ${yardMood}" aria-label="灵田小院">
      <div class="farm-map-head">
        <div>
          <span class="scene-kicker">灵田小院</span>
          <strong>${farmMapTitle(readyCount, emptyCount)}</strong>
        </div>
        <span>${escapeHtml(next ? `${next.plotNo} 号 ${formatRemaining(next.remainingSeconds)}` : selectedText)}</span>
      </div>
      <div class="farm-yard" aria-label="灵田地图">
        <span class="yard-sun" aria-hidden="true"></span>
        <span class="yard-cloudbank" aria-hidden="true"></span>
        <span class="yard-hut" aria-hidden="true"><i></i><b></b></span>
        <span class="yard-gate" aria-hidden="true"><i></i></span>
        <span class="yard-spring" aria-hidden="true"><i></i><b></b></span>
        <span class="yard-bridge" aria-hidden="true"></span>
        <span class="yard-bird bird-a" aria-hidden="true"></span>
        <span class="yard-bird bird-b" aria-hidden="true"></span>
        <span class="yard-breeze breeze-a" aria-hidden="true"></span>
        <span class="yard-breeze breeze-b" aria-hidden="true"></span>
        <span class="yard-path path-a" aria-hidden="true"></span>
        <span class="yard-path path-b" aria-hidden="true"></span>
        <span class="yard-vein vein-a" aria-hidden="true"></span>
        <span class="yard-vein vein-b" aria-hidden="true"></span>
        <span class="yard-rune-ring ring-a" aria-hidden="true"></span>
        <span class="yard-rune-ring ring-b" aria-hidden="true"></span>
        <span class="yard-spark spark-a" aria-hidden="true"></span>
        <span class="yard-spark spark-b" aria-hidden="true"></span>
        <span class="yard-spark spark-c" aria-hidden="true"></span>
        <span class="yard-foreground" aria-hidden="true"></span>
        ${renderYardToolBadge(readyCount, emptyCount)}
        <div class="farm-grid">
          ${renderFarmGridSlots(state)}
        </div>
      </div>
    </section>
  `;
}

function renderYardToolBadge(readyCount, emptyCount) {
  const seed = selectedSeed();
  const info = farmModeInfo(seed, readyCount, emptyCount);
  return `
    <div class="yard-tool-badge ${info.kind}" aria-label="地图当前工具">
      <span>${info.icon}</span>
      <strong>${escapeHtml(info.meta)}</strong>
    </div>
  `;
}

function renderYardKeeper(readyCount, emptyCount) {
  const lines = yardKeeperLine(readyCount, emptyCount);
  return `
    <div class="yard-keeper ${lines.kind}" aria-label="药园管事提示">
      ${animeKeeperHTML("yard")}
      <div>
        <strong>${escapeHtml(lines.title)}</strong>
        <em>${escapeHtml(lines.detail)}</em>
      </div>
    </div>
  `;
}

function renderYardBasket(readyCount, emptyCount) {
  const next = nextMaturePlot();
  const text = readyCount > 0
    ? `${readyCount} 块可收`
    : emptyCount > 0
      ? `${emptyCount} 块待播`
      : next
        ? `${next.plotNo} 号等待`
        : "巡园中";
  return `
    <div class="yard-basket ${readyCount > 0 ? "basket-ready" : emptyCount > 0 ? "basket-empty" : "basket-calm"}" aria-hidden="true">
      <span></span>
      <strong>${escapeHtml(text)}</strong>
    </div>
  `;
}

function renderFarmGridSlots(state) {
  const plotsByNo = new Map(state.plots.map((plot) => [plot.plotNo, plot]));
  const slots = [];
  for (let plotNo = 1; plotNo <= maxGardenPlots; plotNo += 1) {
    const plot = plotsByNo.get(plotNo);
    if (plot) {
      slots.push(renderFarmTile(plot));
      continue;
    }
    slots.push(renderLockedFarmTile(plotNo, state.nextPlot));
  }
  return slots.join("");
}

function renderFarmTile(plot) {
  const selected = app.selectedPlotNo === plot.plotNo;
  const ready = plot.status === "ready";
  const empty = plot.status === "empty";
  const stage = cropStage(plot);
  const toolTarget = (app.toolMode === "plant" && empty) || (app.toolMode === "harvest" && ready);
  const actionFx = recentPlotActionKind(plot.plotNo, plot.seedKey);
  const batchFx = activeBatchPlotKind(plot.plotNo);
  const progress = empty ? 0 : ready ? 100 : progressValue(plot);
  const statusText = app.toolMode === "plant" && empty ? "点此播种" : app.toolMode === "harvest" && ready ? "点此收获" : empty ? "空闲" : ready ? "可收获" : formatRemaining(plot.remainingSeconds);
  const seed = app.state.seeds.find((item) => item.key === plot.seedKey);
  const batchStyle = batchFx ? ` style="--batch-delay:${batchPlotDelay(plot.plotNo)}ms"` : "";
  return `
    <button class="farm-tile ${plot.status} crop-stage-${stage} ${selected ? "selected" : ""} ${ready ? "ready" : ""} ${toolTarget ? "tool-target" : ""} ${actionFx ? `just-done action-${actionFx}` : ""} ${batchFx ? `batch-preview batch-${batchFx}` : ""}" type="button" data-action="select-plot" data-plot="${plot.plotNo}" data-clock-plot="${plot.plotNo}" data-leave="plot-${plot.plotNo}" data-stage="${stage}" data-status="${plot.status}"${batchStyle} aria-label="${plot.plotNo} 号田 ${statusText}">
      <span class="plot-no">${plot.plotNo}</span>
      <span class="soil soil-${plot.status} stage-${stage}">
        <span class="soil-moisture" aria-hidden="true"></span>
        <span class="soil-ridges" aria-hidden="true"></span>
        <span class="crop-shadow" aria-hidden="true"></span>
        <span class="plot-formation" aria-hidden="true"></span>
        <span class="crop stage-${stage}" data-crop-stage="${stage}" aria-hidden="true">${cropIcon(plot, seed)}</span>
        ${renderCropAura(plot, progress)}
      </span>
      ${actionFx ? renderTileActionFx(actionFx) : ""}
      <span class="tile-action-badge ${tileActionKind(plot)}" data-clock-badge="${plot.plotNo}">${tileActionLabel(plot, progress)}</span>
      <span class="tile-name">${empty ? "空田" : escapeHtml(plot.herbName)}</span>
      <span class="tile-status" data-clock-remaining="${plot.plotNo}">${statusText}</span>
    </button>
  `;
}

function renderTileActionFx(kind) {
  const label = kind === "harvest" ? "+收获" : kind === "market" ? "+回收" : kind === "alchemy" ? "+成丹" : "+播种";
  return `
    <span class="tile-fx ${kind}" aria-hidden="true">
      <i></i><i></i><i></i>
      <strong>${label}</strong>
    </span>
  `;
}

function renderCropAura(plot, progress) {
  if (!plot || plot.status === "empty") {
    return `<span class="crop-aura empty-aura" aria-hidden="true"><i></i><i></i><i></i></span>`;
  }
  if (plot.status === "ready") {
    return `<span class="crop-aura mature-aura" aria-hidden="true"><i></i><i></i><i></i><i></i></span>`;
  }
  const tone = progress >= 75 ? "late-aura" : progress >= 35 ? "mid-aura" : "sprout-aura";
  return `<span class="crop-aura ${tone}" aria-hidden="true"><i></i><i></i><i></i></span>`;
}

function renderFarmModeBanner(seed, readyCount, emptyCount) {
  const info = farmModeInfo(seed, readyCount, emptyCount);
  return `
    <div class="farm-mode-banner ${info.kind}" aria-label="当前工具状态">
      <span>${info.icon}</span>
      <div>
        <strong>${escapeHtml(info.title)}</strong>
        <em>${escapeHtml(info.detail)}</em>
      </div>
      <i>${escapeHtml(info.meta)}</i>
    </div>
  `;
}

function renderFarmBusyVeil() {
  if (!app.busy || !app.busyAction) return "";
  return `
    <div class="farm-busy-veil" aria-live="polite">
      <span class="busy-spinner ${app.busyAction.kind}"></span>
      <strong>${escapeHtml(app.busyAction.label)}</strong>
      <em>正在向后端确认资产状态</em>
    </div>
  `;
}

function renderLockedFarmTile(plotNo, nextPlot) {
  const isNext = nextPlot && nextPlot.plotNo === plotNo;
  const missing = isNext ? Math.max(0, nextPlot.cost - app.state.points) : 0;
  const canOpen = isNext && missing <= 0 && !app.busy;
  const action = canOpen ? "open-plot" : "locked-plot";
  const status = isNext ? (missing > 0 ? `还差 ${missing} 积分` : `${nextPlot.cost} 积分`) : "继续开垦前田";
  return `
    <button class="farm-tile locked ${isNext ? "openable" : ""} ${missing > 0 ? "needs-points" : ""}" type="button" data-action="${action}" data-plot="${plotNo}" data-leave="plot-${plotNo}" data-cost="${isNext ? nextPlot.cost : ""}" data-missing="${missing}" ${app.busy ? "disabled" : ""} aria-label="${plotNo} 号田 ${isNext ? "待开垦" : "未解锁"}">
      <span class="plot-no">${plotNo}</span>
      <span class="soil locked-soil">
        <span class="lock-mark" aria-hidden="true">${isNext ? "锄" : "锁"}</span>
      </span>
      <span class="tile-action-badge ${isNext ? "plant-badge" : "empty-badge"}">${isNext ? (missing > 0 ? "差" : "开") : "锁"}</span>
      <span class="tile-name">${isNext ? "待开垦" : "未解锁"}</span>
      <span class="tile-status">${status}</span>
    </button>
  `;
}

function renderActiveSeedStrip(seed, emptyCount) {
  if (!seed) {
    return `
      <div class="active-seed-strip empty-seed">
        <span class="mini-tool">${uiIcon("seed")}</span>
        <strong>暂无种子</strong>
        <button type="button" data-action="open-seeds">去商店</button>
      </div>
    `;
  }
  const canPlant = seed.inventory > 0 && emptyCount > 0 && !app.busy;
  return `
    <div class="active-seed-strip ${canPlant ? "can-plant" : ""}">
      <span class="seed-pack tiny">${seedIcon(seed)}</span>
      <span>
        <strong>${escapeHtml(seed.seedName)}</strong>
        <em>${escapeHtml(seed.herbName)} · ${seed.inventory} 枚 · ${escapeHtml(seed.growText)}</em>
      </span>
      <button type="button" data-action="use-seed" data-seed="${escapeAttr(seed.key)}" ${canPlant ? "" : "disabled"}>播种</button>
      <button type="button" data-action="open-seeds">换种</button>
    </div>
  `;
}

function renderSeedPocket(activeSeed, emptyCount) {
  const ownedSeeds = app.state.seeds.filter((seed) => seed.inventory > 0);
  const totalInventory = ownedSeeds.reduce((sum, seed) => sum + seed.inventory, 0);
  const plantableCount = activeSeed ? Math.min(activeSeed.inventory, emptyCount) : 0;
  const currentSeedLabel = activeSeed && activeSeed.inventory > 0 ? activeSeed.seedName : "未握种";
  const pocketHint = emptyCount > 0
    ? (plantableCount > 0 ? `本轮可播 ${plantableCount} 块` : "空田待备种")
    : "灵田满员";
  if (ownedSeeds.length === 0) {
    return `
      <div class="seed-pocket empty-pocket">
        <div class="pocket-head">
          <div>
            <span class="scene-kicker">随身种子袋</span>
            <strong>袋里暂时空着</strong>
          </div>
          <span>${emptyCount > 0 ? `${emptyCount} 块空田` : "灵田满员"}</span>
        </div>
        <button class="pocket-empty" type="button" data-action="open-seeds">
          <span>籽</span>
          <strong>去种子铺备些灵种</strong>
          <em>${emptyCount > 0 ? "买完可回灵田连续播种" : "先补货，成熟后再轮作"}</em>
        </button>
      </div>
    `;
  }
  return `
    <div class="seed-pocket" aria-label="随身种子袋">
      <div class="pocket-head">
        <div>
          <span class="scene-kicker">随身种子袋</span>
          <strong>${escapeHtml(currentSeedLabel)}</strong>
        </div>
        <span>${pocketHint}</span>
      </div>
      <div class="pocket-summary" aria-label="种子袋概览">
        <span><strong>${ownedSeeds.length}</strong><em>种灵种</em></span>
        <span><strong>${totalInventory}</strong><em>枚库存</em></span>
        <span><strong>${emptyCount}</strong><em>块空田</em></span>
      </div>
      <div class="pocket-scroll">
        ${ownedSeeds.map((seed) => {
          const selected = activeSeed && activeSeed.key === seed.key;
          const canSelect = !app.busy;
          const canPlant = seed.inventory > 0 && emptyCount > 0;
          const seedDots = Math.max(0, Math.min(5, seed.inventory));
          const status = selected ? "已握" : canPlant ? "可播" : "备着";
          const statusClass = selected ? "selected-badge" : canPlant ? "ready-badge" : "idle-badge";
          const plantHint = canPlant ? `可播 ${Math.min(seed.inventory, emptyCount)} 块` : "暂存袋中";
          return `
            <button class="pocket-seed ${selected ? "selected" : ""}" type="button" data-action="quick-seed" data-seed="${escapeAttr(seed.key)}" ${canSelect ? "" : "disabled"}>
              <span class="pocket-badge ${statusClass}">${status}</span>
              <span class="pocket-icon">${seedIcon(seed)}</span>
              <strong>${escapeHtml(seed.seedName)}</strong>
              <em>x${seed.inventory} · ${plantHint}</em>
              <span class="pocket-grow">${escapeHtml(seed.growText)}</span>
              <span class="seed-stock-dots pocket-dots" aria-label="种袋库存">
                ${Array.from({ length: 5 }, (_, index) => `<i class="${index < seedDots ? "filled" : ""}"></i>`).join("")}
              </span>
            </button>
          `;
        }).join("")}
        <button class="pocket-shop-card" type="button" data-action="open-seeds">
          <span>+</span>
          <strong>补灵种</strong>
          <em>去种子铺</em>
        </button>
      </div>
    </div>
  `;
}

function renderFarmNotice(readyCount, emptyCount) {
  const next = nextMaturePlot();
  if (readyCount > 0) {
    return `
      <div class="farm-notice ready-notice">
        <span>${uiIcon("harvest")}</span>
        <strong>${readyCount} 块灵田已成熟</strong>
        <em>切到收获工具，点亮地块即可入袋</em>
      </div>
    `;
  }
  if (next) {
    return `
      <div class="farm-notice">
        <span>${uiIcon("clock")}</span>
        <strong>${next.plotNo} 号田下一批成熟</strong>
        <em>${formatRemaining(next.remainingSeconds)} · ${escapeHtml(next.herbName)}</em>
      </div>
    `;
  }
  if (emptyCount > 0) {
    return `
      <div class="farm-notice seed-notice">
        <span>${uiIcon("seed")}</span>
        <strong>${emptyCount} 块空田待播种</strong>
        <em>选好种子后切播种工具连续点田</em>
      </div>
    `;
  }
  return `
    <div class="farm-notice">
      <span>${uiIcon("herb")}</span>
      <strong>药园正在稳定生长</strong>
      <em>保持灵田轮转，成熟后及时收获</em>
    </div>
  `;
}

function renderFarmGuide(seed, readyCount, emptyCount) {
  const guide = farmGuidePlan(seed, readyCount, emptyCount);
  return `
    <div class="farm-guide ${guide.tone}" aria-label="巡园指引">
      <span class="guide-avatar">${guide.icon}</span>
      <div>
        <strong>${escapeHtml(guide.title)}</strong>
        <em>${escapeHtml(guide.detail)}</em>
      </div>
      <button type="button" data-action="farm-guide-primary">${escapeHtml(guide.actionLabel)}</button>
    </div>
  `;
}

function renderFarmActionDock(seed, readyCount, emptyCount) {
  const canHarvest = readyCount > 0 && !app.busy;
  const plantCount = seed ? Math.min(seed.inventory, emptyCount) : 0;
  const canPlantAll = plantCount > 0 && !app.busy;
  const urgentCount = app.state.market.filter((offer) => offer.left > 0).length;
  return `
    <div class="farm-action-dock" aria-label="快捷操作">
      <button type="button" data-action="harvest-all" ${canHarvest ? "" : "disabled"}>
        <span>${uiIcon("harvest")}</span>
        <strong>收成熟</strong>
        <em>${readyCount > 0 ? `${readyCount} 块` : "暂无"}</em>
      </button>
      <button type="button" data-action="plant-all" data-seed="${seed ? escapeAttr(seed.key) : ""}" ${canPlantAll ? "" : "disabled"}>
        <span>${uiIcon("seed")}</span>
        <strong>批量播</strong>
        <em>${farmPlantAllHint(seed, emptyCount, plantCount)}</em>
      </button>
      <button type="button" data-action="open-seeds">
        <span>${uiIcon("shop")}</span>
        <strong>买种</strong>
        <em>${seed ? `袋中 ${seed.inventory}` : "去补货"}</em>
      </button>
      <button type="button" data-action="open-market">
        <span>${uiIcon("market")}</span>
        <strong>药铺</strong>
        <em>${urgentCount > 0 ? `急收 ${urgentCount}` : "看行情"}</em>
      </button>
    </div>
  `;
}

function renderFarmTaskBoard(seed, readyCount, emptyCount) {
  const next = nextMaturePlot();
  const urgentCount = app.state.market.filter((offer) => offer.left > 0).length;
  const alchemyReady = app.state.recipes.filter((recipe) => recipe.unlocked && canAlchemy(recipe)).length;
  const canHarvest = readyCount > 0 && !app.busy;
  const canPlant = seed && seed.inventory > 0 && emptyCount > 0 && !app.busy;
  return `
    <div class="farm-task-board" aria-label="今日药园待办">
      <div class="task-board-head">
        <div>
          <span class="scene-kicker">药园管事</span>
          <strong>${farmTaskTitle(readyCount, emptyCount, urgentCount, alchemyReady)}</strong>
        </div>
        <span>${formatFarmClock()}</span>
      </div>
      <div class="task-lanes">
        <button class="task-card ${canHarvest ? "hot" : ""}" type="button" data-action="harvest-all" ${canHarvest ? "" : "disabled"}>
          <span>收</span>
          <strong>${readyCount > 0 ? `${readyCount} 块成熟` : "暂无成熟"}</strong>
          <em>${readyCount > 0 ? "一键入袋" : (next ? `${next.plotNo} 号 ${formatRemaining(next.remainingSeconds)}` : "灵田稳定")}</em>
        </button>
        <button class="task-card ${canPlant ? "hot" : ""}" type="button" data-action="plant-all" data-seed="${seed ? escapeAttr(seed.key) : ""}" ${canPlant ? "" : "disabled"}>
          <span>${uiIcon("seed")}</span>
          <strong>${emptyCount > 0 ? `${emptyCount} 块空田` : "满田运转"}</strong>
          <em>${seed ? `${escapeHtml(seed.seedName)} x${seed.inventory}` : "先备灵种"}</em>
        </button>
        <button class="task-card ${urgentCount > 0 ? "hot" : ""}" type="button" data-action="open-market">
          <span>${uiIcon("market")}</span>
          <strong>${urgentCount > 0 ? `${urgentCount} 种急收` : "查看药铺"}</strong>
          <em>${urgentCount > 0 ? "可对照库存" : "行情面板"}</em>
        </button>
        <button class="task-card ${alchemyReady > 0 ? "hot" : ""}" type="button" data-action="open-recipes">
          <span>${uiIcon("recipe")}</span>
          <strong>${alchemyReady > 0 ? `${alchemyReady} 张可炼` : "丹炉待命"}</strong>
          <em>${alchemyReady > 0 ? "材料齐备" : "查看材料"}</em>
        </button>
      </div>
    </div>
  `;
}

function renderFarmFeed(seed, readyCount, emptyCount) {
  const items = farmFeedItems(seed, readyCount, emptyCount);
  return `
    <div class="farm-feed" aria-label="药园动态">
      <div class="farm-feed-head">
        <div>
          <span class="scene-kicker">农场动态</span>
          <strong>${items[0] ? escapeHtml(items[0].title) : "园况平稳"}</strong>
        </div>
        <span>${items.length} 条</span>
      </div>
      <div class="feed-list">
        ${items.map((item) => `
          <button class="feed-item ${item.kind}" type="button" data-action="${item.action}" ${item.plotNo ? `data-plot="${item.plotNo}"` : ""} ${item.mode ? `data-mode="${escapeAttr(item.mode)}"` : ""}>
            <span>${item.icon}</span>
            <strong>${escapeHtml(item.title)}</strong>
            <em>${escapeHtml(item.detail)}</em>
            <i>${escapeHtml(item.meta)}</i>
          </button>
        `).join("")}
      </div>
    </div>
  `;
}

function renderMaturityTimeline() {
  const rows = timelinePlots();
  const next = rows.find((plot) => plot.status === "growing");
  return `
    <div class="maturity-board" aria-label="灵田成熟时刻表">
      <div class="maturity-head">
        <div>
          <span class="scene-kicker">成熟时刻表</span>
          <strong>${maturityBoardTitle(rows)}</strong>
        </div>
        <span data-clock-next>${next ? `下一块 ${formatRemaining(next.remainingSeconds)}` : "巡园"}</span>
      </div>
      <div class="maturity-list">
        ${rows.map((plot) => `
          <button class="maturity-row ${plot.status} ${app.selectedPlotNo === plot.plotNo ? "selected" : ""}" type="button" data-action="focus-plot" data-plot="${plot.plotNo}">
            <span>${plot.plotNo}</span>
            <strong>${timelinePlotTitle(plot)}</strong>
            <em data-clock-timeline="${plot.plotNo}">${timelinePlotMeta(plot)}</em>
            <i data-clock-progress="${plot.plotNo}" style="--value:${plot.status === "empty" ? 0 : plot.status === "ready" ? 100 : progressValue(plot)}%"></i>
          </button>
        `).join("") || `<button class="maturity-row empty" type="button" data-action="open-seeds"><span>种</span><strong>暂无灵田</strong><em>先去备种开园</em><i style="--value:0%"></i></button>`}
      </div>
    </div>
  `;
}

function renderPlotQuickBar(plot, activeSeed) {
  if (!plot) return "";
  const ready = plot.status === "ready";
  const empty = plot.status === "empty";
  const progress = empty ? 0 : ready ? 100 : progressValue(plot);
  const plantedSeed = app.state.seeds.find((seed) => seed.key === plot.seedKey);
  const title = empty ? `${plot.plotNo} 号空田` : `${plot.plotNo} 号 ${escapeHtml(plot.herbName)}`;
  const subtitle = empty
    ? (activeSeed ? `手上 ${escapeHtml(activeSeed.seedName)} · 库存 ${activeSeed.inventory}` : "先去备种")
    : ready
      ? "成熟可收，点击入袋"
      : `${formatRemaining(plot.remainingSeconds)} 后成熟 · ${plantedSeed ? escapeHtml(plantedSeed.seedName) : "灵种"}`;
  const action = empty
    ? (activeSeed && activeSeed.inventory > 0
      ? `<button type="button" data-action="plant" data-plot="${plot.plotNo}" data-seed="${escapeAttr(activeSeed.key)}" ${app.busy ? "disabled" : ""}>播种</button>`
      : `<button type="button" data-action="open-seeds">买种</button>`)
    : ready
      ? `<button type="button" data-action="harvest" data-plot="${plot.plotNo}" ${app.busy ? "disabled" : ""}>收获</button>`
      : `<button type="button" disabled>生长中</button>`;
  return `
    <div class="plot-quick-bar ${plot.status}" aria-label="当前地块快捷操作">
      <span class="quick-plot-no">${plot.plotNo}</span>
      <div>
        <strong>${title}</strong>
        <em data-clock-quick="${plot.plotNo}">${subtitle}</em>
      </div>
      <div class="quick-plot-actions">
        ${action}
        ${empty ? `<button class="secondary" type="button" data-action="open-seeds">换种</button>` : ""}
      </div>
      <i data-clock-progress="${plot.plotNo}" style="--value:${progress}%"></i>
    </div>
  `;
}

function renderShelfModes(kind, active, modes) {
  return `
    <div class="shelf-modes" aria-label="${kind} 筛选">
      ${modes.map((mode) => `
        <button class="${active === mode.key ? "active" : ""}" type="button" data-action="set-${kind}-mode" data-mode="${escapeAttr(mode.key)}">
          <span>${mode.label}</span>
          <strong>${mode.count}</strong>
        </button>
      `).join("")}
    </div>
  `;
}

function renderShelfEmpty(action, label, text) {
  return `
    <button class="empty action-empty shelf-empty" type="button" data-action="${action}" data-mode="all">
      <strong>${escapeHtml(label)}</strong>
      <span>${escapeHtml(text)}，点此查看全部</span>
    </button>
  `;
}

function renderPlotAdvice(plot, seed) {
  const advice = plotAdvice(plot, seed);
  return `
    <div class="plot-advice ${advice.kind}">
      <span>${advice.icon}</span>
      <strong>${escapeHtml(advice.title)}</strong>
      <em>${escapeHtml(advice.detail)}</em>
    </div>
  `;
}

function renderPlotStatusCard(plot, seed) {
  const progress = plot && plot.status === "empty" ? 0 : progressValue(plot);
  const ready = plot && plot.status === "ready";
  const status = plotStatusInfo(plot, seed);
  return `
    <div class="plot-status-card ${status.kind}">
      <div class="status-dial" style="--value:${progress}%">
        <strong ${plot ? `data-clock-status-dial="${plot.plotNo}"` : ""}>${ready ? "收" : plot.status === "empty" ? "种" : `${progress}%`}</strong>
      </div>
      <div>
        <span>${escapeHtml(status.title)}</span>
        <em ${plot ? `data-clock-status-detail="${plot.plotNo}"` : ""}>${escapeHtml(status.detail)}</em>
      </div>
      <i>${escapeHtml(status.meta)}</i>
    </div>
  `;
}

function renderPlotCoach(plot, seed) {
  const coach = plotCoachInfo(plot, seed);
  return `
    <div class="plot-coach ${coach.kind}">
      ${animeKeeperHTML("coach")}
      <div>
        <strong>${escapeHtml(coach.title)}</strong>
        <em>${escapeHtml(coach.detail)}</em>
      </div>
    </div>
  `;
}

function renderPlotPanel(plot) {
  if (!plot) {
    return `<section class="plot-panel"><div class="empty">开垦灵田后可在这里打理药草</div></section>`;
  }

  if (plot.status === "empty") {
    const activeSeed = selectedSeed();
    const canUseActiveSeed = activeSeed && activeSeed.inventory > 0;
    const seedButtons = app.state.seeds
      .filter((seed) => seed.inventory > 0)
      .map((seed) => `
        <button class="seed-token ${seed.key === app.selectedSeedKey ? "selected" : ""}" type="button" data-action="plant" data-plot="${plot.plotNo}" data-seed="${escapeAttr(seed.key)}">
          <strong>${escapeHtml(seed.seedName)}</strong>
          <span>${escapeHtml(seed.herbName)} · ${escapeHtml(seed.growText)} · x${seed.inventory}</span>
        </button>
      `)
      .join("");
    return `
      <section class="plot-panel">
        <div class="panel-head">
          <div>
            <span class="eyebrow">Plot ${plot.plotNo}</span>
            <h2>${plot.plotNo} 号灵田</h2>
          </div>
          <span class="badge">空田</span>
        </div>
        <div class="plot-stat-grid">
          <span>状态 <strong>可播种</strong></span>
          <span>当前种 <strong>${activeSeed ? escapeHtml(activeSeed.seedName) : "-"}</strong></span>
          <span>库存 <strong>${activeSeed ? activeSeed.inventory : 0}</strong></span>
        </div>
        ${renderPlotStatusCard(plot, activeSeed)}
        ${renderPlotAdvice(plot, activeSeed)}
        ${renderPlotCoach(plot, activeSeed)}
        ${canUseActiveSeed ? `
          <button class="seed-hand" type="button" data-action="plant" data-plot="${plot.plotNo}" data-seed="${escapeAttr(activeSeed.key)}">
            <span class="seed-pack small">${seedIcon(activeSeed)}</span>
            <span>
              <strong>种下 ${escapeHtml(activeSeed.seedName)}</strong>
              <em>${escapeHtml(activeSeed.herbName)} · ${escapeHtml(activeSeed.growText)} · 产量 ${escapeHtml(activeSeed.yieldText)}</em>
            </span>
          </button>
        ` : ""}
        <div class="seed-tray">${seedButtons || `<button class="empty action-empty" type="button" data-action="open-seeds">暂无可种种子，去种子商店</button>`}</div>
      </section>
    `;
  }

  const ready = plot.status === "ready";
  const progress = ready ? 100 : progressValue(plot);
  const seed = app.state.seeds.find((item) => item.key === plot.seedKey);
  return `
    <section class="plot-panel ${ready ? "ready" : ""}">
      <div class="panel-head">
        <div>
          <span class="eyebrow">Plot ${plot.plotNo}</span>
          <h2>${escapeHtml(plot.herbName)}</h2>
        </div>
        <span class="badge ${ready ? "gold" : ""}">${ready ? "成熟" : "生长中"}</span>
      </div>
      <div class="plot-stat-grid">
        <span>进度 <strong data-clock-selected-progress="${plot.plotNo}">${progress}%</strong></span>
        <span>阶段 <strong>${ready ? "可收" : cropStageName(plot)}</strong></span>
        <span>成熟 <strong>${ready ? "现在" : formatShortTime(plot.maturesAt)}</strong></span>
      </div>
      ${renderPlotStatusCard(plot, seed)}
      <div class="crop-focus">
        <div class="crop-big stage-${cropStage(plot)}">${cropIcon(plot, seed)}</div>
        <div class="crop-copy">
          <strong>${ready ? "灵草已成熟" : "正在吸纳灵气"}</strong>
          <span data-clock-selected-remaining="${plot.plotNo}">${ready ? "可立即收获入袋" : `剩余 ${formatRemaining(plot.remainingSeconds)} · ${seed ? escapeHtml(seed.seedName) : "灵种"}`}</span>
        </div>
      </div>
      <div class="growth-track" aria-label="成长进度">
        <span class="${progress >= 1 ? "done" : ""}">播种</span>
        <span class="${progress >= 35 ? "done" : ""}">发芽</span>
        <span class="${progress >= 75 ? "done" : ""}">成株</span>
        <span class="${ready ? "done" : ""}">收获</span>
      </div>
      ${renderPlotAdvice(plot, seed)}
      ${renderPlotCoach(plot, seed)}
      <div class="progress"><span data-clock-progress="${plot.plotNo}" style="--value:${progress}%"></span></div>
      <div class="actions">
        <button class="btn harvest-main" type="button" data-action="harvest" data-plot="${plot.plotNo}" ${ready ? "" : "disabled"}>${ready ? "收获入袋" : "等待成熟"}</button>
      </div>
    </section>
  `;
}

function renderSeeds() {
  const selected = selectedSeed();
  const seeds = filteredSeeds();
  content.innerHTML = `
    <section class="shop-scene">
      <div class="shop-awning">
        <div>
          <span class="scene-kicker">种子商店</span>
          <strong>挑选今日灵种</strong>
        </div>
        <span class="scene-chip">${app.state.points} 积分</span>
      </div>
      ${renderShopKeeper(selected, seeds)}
      ${renderShelfModes("seed", app.seedShelfMode, seedShelfModes())}
      <div class="shop-shelf">
        <span class="shelf-prop prop-seed-a" aria-hidden="true"></span>
        <span class="shelf-prop prop-seed-b" aria-hidden="true"></span>
        ${seeds.map(renderSeedGoods).join("") || renderShelfEmpty("set-seed-mode", seedModeLabel(app.seedShelfMode), "当前货架暂无灵种")}
      </div>
    </section>
    ${renderSeedCounter(selected)}
  `;
}

function renderShopKeeper(seed, visibleSeeds) {
  const emptyCount = emptyPlotCount();
  const buyable = app.state.seeds.filter((item) => item.purchasable && item.leftToday > 0 && item.price <= app.state.points).length;
  const line = seed && seed.inventory > 0
    ? `${seed.seedName} 袋中还有 ${seed.inventory} 枚`
    : buyable > 0
      ? `${buyable} 种灵种今日可买`
      : visibleSeeds.length > 0
        ? "今日货架先看库存和限购"
        : "当前筛选没有货，换个货架看看";
  return `
    <div class="shop-keeper" aria-label="种子铺提示">
      ${animeKeeperHTML("shop")}
      <div>
        <strong>${emptyCount > 0 ? `${emptyCount} 块空田待播` : "灵田暂时满员"}</strong>
        <em>${escapeHtml(line)}</em>
      </div>
    </div>
  `;
}

function renderSeedGoods(seed) {
  const selected = seed.key === app.selectedSeedKey;
  const disabled = !seed.purchasable || seed.leftToday <= 0;
  const affordable = seed.price <= app.state.points;
  const badge = selected ? "已选" : disabled ? "售罄" : affordable ? "可买" : "缺积分";
  const limitPercent = seed.dailyLimit > 0 ? Math.round((seed.leftToday / seed.dailyLimit) * 100) : 0;
  const seedDots = Math.max(0, Math.min(5, seed.inventory));
  return `
    <button class="goods-card ${selected ? "selected" : ""} ${disabled ? "soldout" : ""} ${!disabled && !affordable ? "short-points" : ""}" type="button" data-action="select-seed" data-seed="${escapeAttr(seed.key)}">
      <span class="card-ribbon">${badge}</span>
      <span class="goods-icon">${seedIcon(seed)}</span>
      <span class="seed-price-tag">
        <em>${seed.price > 0 ? seed.price : "稀"}</em>
      </span>
      <strong>${escapeHtml(seed.seedName)}</strong>
      <span>${seed.price > 0 ? `${seed.price} 积分` : "稀有种"}</span>
      <em>${seed.inventory} 枚 · 限 ${seed.leftToday}</em>
      <span class="seed-stock-dots" aria-label="种袋库存">
        ${Array.from({ length: 5 }, (_, index) => `<i class="${index < seedDots ? "filled" : ""}"></i>`).join("")}
      </span>
      <span class="goods-meter" style="--value:${Math.max(0, Math.min(100, limitPercent))}%">
        <i></i>
      </span>
    </button>
  `;
}

function renderSeedShopGuide(seed) {
  const guide = seedShopGuide(seed);
  return `
    <div class="shop-guide ${guide.kind}">
      <span>${guide.icon}</span>
      <div>
        <strong>${escapeHtml(guide.title)}</strong>
        <em>${escapeHtml(guide.detail)}</em>
      </div>
      <button type="button" data-action="${guide.action}" ${guide.seedKey ? `data-seed="${escapeAttr(guide.seedKey)}"` : ""}>${escapeHtml(guide.label)}</button>
    </div>
  `;
}

function renderSeedCounter(seed) {
  if (!seed) return `<section class="counter-panel"><div class="empty">暂无种子货架</div></section>`;
  const affordable = seed.price <= app.state.points;
  const canBuy = seed.purchasable && seed.leftToday > 0 && affordable && !app.busy;
  const counterNote = !affordable ? "积分不足" : seed.inventory > 0 ? "已入袋" : "可买入";
  return `
    <section class="counter-panel seed-counter">
      <div class="counter-visual">
        <div class="seed-pack">${seedIcon(seed)}</div>
        <div>
          <span class="eyebrow">今日限购 ${seed.leftToday}/${seed.dailyLimit} · ${counterNote}</span>
          <h2>${escapeHtml(seed.seedName)}</h2>
          <p>${escapeHtml(seed.herbName)} · ${escapeHtml(seed.growText)} · 产量 ${escapeHtml(seed.yieldText)}</p>
        </div>
      </div>
      <div class="counter-stats">
        <span>售价 <strong>${seed.price}</strong></span>
        <span>背包 <strong>${seed.inventory}</strong></span>
        <span>限购 <strong>${seed.leftToday}</strong></span>
      </div>
      <div class="actions">
        <button class="btn" type="button" data-action="buy-seed" data-seed="${escapeAttr(seed.key)}" ${canBuy ? "" : "disabled"}>买入种子</button>
        ${seed.inventory > 0 ? `<button class="btn secondary" type="button" data-action="use-seed" data-seed="${escapeAttr(seed.key)}">去播种</button>` : ""}
      </div>
    </section>
  `;
}

function renderHerbs() {
  const selected = selectedHerb();
  const herbs = filteredHerbs();
  content.innerHTML = `
    <section class="warehouse-scene">
      <div class="warehouse-head">
        <div>
          <span class="scene-kicker">草药背包</span>
          <strong>${app.state.counts.herbInventory} 株灵草入库</strong>
        </div>
        <span class="scene-chip">仓库</span>
      </div>
      ${renderWarehouseKeeper(selected, herbs)}
      ${renderShelfModes("herb", app.herbShelfMode, herbShelfModes())}
      <div class="warehouse-grid">
        <span class="warehouse-prop prop-crate" aria-hidden="true"></span>
        <span class="warehouse-prop prop-scale" aria-hidden="true"></span>
        ${herbs.map(renderHerbBin).join("") || renderShelfEmpty("set-herb-mode", herbModeLabel(app.herbShelfMode), "当前仓格暂无灵草")}
      </div>
    </section>
    ${renderHerbInventoryPanel(selected)}
  `;
}

function renderMarket() {
  const selected = marketSelectedHerb();
  const matched = firstMatchedMarketOffer();
  content.innerHTML = `
    <section class="warehouse-scene market-scene">
      <div class="warehouse-head">
        <div>
          <span class="scene-kicker">药铺</span>
          <strong>${app.state.market.length > 0 ? "今日急收行情" : "今日暂无急收"}</strong>
        </div>
        <span class="scene-chip">${app.state.market.filter((offer) => offer.left > 0).length} 种可回收</span>
      </div>
      <div class="market-strip">
        ${app.state.market.map(renderMarketOffer).join("") || `<span class="market-empty">今日暂无急收行情</span>`}
      </div>
      <div class="warehouse-guide ${matched ? "guide-market" : "guide-calm"}">
        <span>${uiIcon("market")}</span>
        <div>
          <strong>${matched ? `${escapeHtml(matched.herbName)} 可走急收` : "药铺按库存结算"}</strong>
          <em>${matched ? `今日还剩 ${matched.left} 株额度` : "选择药草后可按基础价或急收价回收"}</em>
        </div>
        <button type="button" data-action="open-herbs">看仓库</button>
      </div>
    </section>
    ${renderHerbCounter(selected)}
  `;
}

function renderWarehouseKeeper(herb, visibleHerbs) {
  const stocked = app.state.herbs.filter((item) => item.inventory > 0).length;
  const line = herb && herb.inventory > 0
    ? `${herb.herbName} 库存 ${herb.inventory} 株`
    : visibleHerbs.length > 0
      ? "点仓格查看库存和炼丹材料"
      : "当前筛选暂无草药，换个仓格范围";
  return `
    <div class="warehouse-keeper" aria-label="仓库管事提示">
      ${animeKeeperHTML("warehouse")}
      <div>
        <strong>${stocked > 0 ? `${stocked} 种草药有货` : "仓库暂时空着"}</strong>
        <em>${escapeHtml(line)}</em>
      </div>
    </div>
  `;
}

function renderMarketOffer(offer) {
  const herb = app.state.herbs.find((item) => item.key === offer.seedKey);
  const inventory = herb ? herb.inventory : 0;
  const matched = Math.min(inventory, offer.left);
  const selected = offer.seedKey === app.selectedHerbKey;
  const canMatch = matched > 0;
  return `
    <button class="market-offer ${selected ? "selected" : ""} ${canMatch ? "match" : "empty-offer"}" type="button" data-action="select-herb" data-seed="${escapeAttr(offer.seedKey)}">
      <strong>${escapeHtml(offer.herbName)}</strong>
      <span>${offer.price} 积分 · 剩 ${offer.left}</span>
      <em>${canMatch ? `可卖 x${matched}` : inventory > 0 ? `库存 x${inventory}` : "无库存"}</em>
    </button>
  `;
}

function renderHerbBin(herb) {
  const selected = herb.key === app.selectedHerbKey;
  const badge = selected ? "已选" : herb.inventory > 0 ? "有货" : "空箱";
  const stockValue = Math.max(0, Math.min(100, herb.inventory * 12));
  return `
    <button class="herb-bin ${selected ? "selected" : ""} ${herb.inventory <= 0 ? "empty-bin" : ""}" type="button" data-action="select-herb" data-seed="${escapeAttr(herb.key)}">
      <span class="card-ribbon">${badge}</span>
      <span class="bin-icon">${herbIcon(herb)}</span>
      <strong>${escapeHtml(herb.herbName)}</strong>
      <span>库存 ${herb.inventory}</span>
      <span class="herb-stock-meter" style="--value:${stockValue}%"><i></i></span>
    </button>
  `;
}

function renderHerbInventoryPanel(herb) {
  if (!herb) return `<section class="counter-panel"><div class="empty">暂无草药库存</div></section>`;
  return `
    <section class="counter-panel herb-inventory-panel">
      <div class="counter-visual">
        <div class="herb-crate">${herbIcon(herb)}</div>
        <div>
          <span class="eyebrow">仓库档案</span>
          <h2>${escapeHtml(herb.herbName)}</h2>
          <p>库存 ${herb.inventory} 株，可作为丹方材料或送往药铺处理</p>
        </div>
      </div>
      <div class="counter-stats">
        <span>库存 <strong>${herb.inventory}</strong></span>
        <span>状态 <strong>${herb.inventory > 0 ? "有货" : "空"}</strong></span>
        <span>用途 <strong>炼丹</strong></span>
      </div>
      <div class="actions">
        <button class="btn secondary" type="button" data-action="open-market" ${herb.inventory > 0 ? "" : "disabled"}>去药铺处理</button>
      </div>
    </section>
  `;
}

function renderHerbWarehouseGuide(herb) {
  const guide = herbWarehouseGuide(herb);
  return `
    <div class="warehouse-guide ${guide.kind}">
      <span>${guide.icon}</span>
      <div>
        <strong>${escapeHtml(guide.title)}</strong>
        <em>${escapeHtml(guide.detail)}</em>
      </div>
      <button type="button" data-action="${guide.action}" ${guide.seedKey ? `data-seed="${escapeAttr(guide.seedKey)}"` : ""} ${guide.mode ? `data-mode="${escapeAttr(guide.mode)}"` : ""}>${escapeHtml(guide.label)}</button>
    </div>
  `;
}

function renderHerbCounter(herb) {
  if (!herb) return `<section class="counter-panel"><div class="empty">暂无草药</div></section>`;
  const canSell = herb.inventory > 0 && herb.sellable && !app.busy;
  const defaultQty = Math.max(1, Math.min(Number(herb.inventory || 0), 1));
  const preview = herbSellPreview(herb, defaultQty);
  const marketMeter = herbMarketPercent(herb);
  return `
    <section class="counter-panel market-counter">
      <div class="counter-visual">
        <div class="herb-crate">${herbIcon(herb)}</div>
        <div>
          <span class="eyebrow">${herb.urgent ? `药铺急收剩 ${herb.marketLeft}/${herb.marketLimit}` : "普通回收"}</span>
          <h2>${escapeHtml(herb.herbName)}</h2>
          <p>${herb.urgent ? `急收价 ${herb.marketPrice}，超出额度按基础价 ${herb.basePrice}` : `基础回收价 ${herb.basePrice}`}</p>
        </div>
      </div>
      <div class="counter-stats">
        <span>库存 <strong>${herb.inventory}</strong></span>
        <span>基础 <strong>${herb.basePrice}</strong></span>
        <span>急收 <strong>${herb.urgent ? herb.marketPrice : "-"}</strong></span>
      </div>
      <div class="sell-preview ${preview.urgentQty > 0 ? "urgent-preview" : ""}">
        <span>
          <em>当前预估</em>
          <strong>${preview.total} 积分</strong>
        </span>
        <span>
          <em>急收额度</em>
          <strong>${preview.urgentQty} 株</strong>
        </span>
        <span>
          <em>普通回收</em>
          <strong>${preview.baseQty} 株</strong>
        </span>
      </div>
      <div class="market-ledger ${herb.urgent ? "urgent-ledger" : ""}">
        <span class="ledger-prop" aria-hidden="true"></span>
        <div>
          <strong>${herb.urgent ? "急收柜台" : "普通账台"}</strong>
          <em>${herb.urgent ? `今日额度剩 ${herb.marketLeft}/${herb.marketLimit}` : "未入今日急收行情"}</em>
          <i class="ledger-meter" style="--value:${marketMeter}%"></i>
        </div>
      </div>
      <div class="actions">
        <button class="btn secondary" type="button" data-action="sell-one" data-seed="${escapeAttr(herb.key)}" ${canSell ? "" : "disabled"}>回收 1 株</button>
        <label class="qty-row">
          <input type="number" min="1" max="${Math.max(1, Number(herb.inventory || 0))}" value="${defaultQty}" inputmode="numeric" data-sell-qty="${escapeAttr(herb.key)}" ${canSell ? "" : "disabled"}>
          <button class="btn" type="button" data-action="sell-custom" data-seed="${escapeAttr(herb.key)}" ${canSell ? "" : "disabled"}>指定回收</button>
        </label>
      </div>
    </section>
  `;
}

function renderRecipes() {
  const selected = selectedRecipe();
  const recipes = filteredRecipes();
  content.innerHTML = `
    <section class="alchemy-scene">
      <div class="alchemy-room-props" aria-hidden="true">
        <span class="room-prop prop-cabinet"></span>
        <span class="room-prop prop-stool"></span>
        <span class="room-prop prop-scroll-stack"></span>
        <span class="room-prop prop-sword-stand"></span>
        <span class="room-prop prop-hanging-rune rune-left"></span>
        <span class="room-prop prop-hanging-rune rune-right"></span>
        <span class="room-prop prop-spirit-window"></span>
      </div>
      <div class="furnace">
        <div class="furnace-smoke" aria-hidden="true">
          <i></i><i></i><i></i>
        </div>
        <div class="furnace-fire" aria-hidden="true"></div>
        <div class="furnace-rune-ring" aria-hidden="true"><i></i><i></i><i></i><i></i></div>
        <div class="furnace-vessel" aria-hidden="true">
          <span class="vessel-lid"></span>
          <span class="vessel-ear ear-left"></span>
          <span class="vessel-ear ear-right"></span>
          <span class="vessel-belly">${furnaceMarkIcon(selected)}</span>
          <span class="vessel-leg leg-left"></span>
          <span class="vessel-leg leg-right"></span>
        </div>
        <div class="furnace-orbit ${selected ? "active" : "idle"}" aria-hidden="true">
          ${selected ? pillIcon(selected) : alchemyIdleIcon()}
          <i></i><i></i><i></i>
        </div>
        <div class="furnace-copy">
          <span class="scene-kicker">丹方炼丹</span>
          <strong>${selected ? escapeHtml(selected.productName) : "请选择丹方"}</strong>
          <span>${selected ? (selected.unlocked ? "丹炉可用" : "尚未参悟") : "丹炉待命"}</span>
        </div>
      </div>
      ${renderAlchemyGuide(selected)}
      ${renderShelfModes("recipe", app.recipeShelfMode, recipeShelfModes())}
      <div class="recipe-scrolls">
        ${recipes.map(renderRecipeSlip).join("") || renderShelfEmpty("set-recipe-mode", recipeModeLabel(app.recipeShelfMode), "当前卷架暂无丹方")}
      </div>
    </section>
    ${renderRecipeCounter(selected)}
  `;
}

function renderRecipeSlip(recipe) {
  const selected = recipe.key === app.selectedRecipeKey;
  const ready = recipe.unlocked && canAlchemy(recipe);
  const missingCount = recipe.materials.filter((mat) => !mat.enough).length;
  return `
    <button class="recipe-slip ${selected ? "selected" : ""} ${ready ? "ready" : ""} ${!recipe.unlocked ? "locked-slip" : ""}" type="button" data-action="select-recipe" data-recipe="${escapeAttr(recipe.key)}">
      <span class="recipe-diagram">${recipeIcon(recipe)}</span>
      <strong>${escapeHtml(recipe.name)}</strong>
      <em>${recipe.unlocked ? (ready ? "可炼" : `缺 ${missingCount}`) : `${recipe.unlockPrice} 参悟`}</em>
    </button>
  `;
}

function renderAlchemyGuide(recipe) {
  const guide = alchemyGuide(recipe);
  return `
    <div class="alchemy-guide ${guide.kind}">
      <span>${guide.icon}</span>
      <div>
        <strong>${escapeHtml(guide.title)}</strong>
        <em>${escapeHtml(guide.detail)}</em>
      </div>
      <button type="button" data-action="${guide.action}" ${guide.recipeKey ? `data-recipe="${escapeAttr(guide.recipeKey)}"` : ""} ${guide.mode ? `data-mode="${escapeAttr(guide.mode)}"` : ""}>${escapeHtml(guide.label)}</button>
    </div>
  `;
}

function renderRecipeCounter(recipe) {
  if (!recipe) return `<section class="counter-panel"><div class="empty">暂无丹方</div></section>`;
  const action = recipe.unlocked ? "alchemy" : "buy-recipe";
  const canRun = recipe.unlocked ? canAlchemy(recipe) && !app.busy : !app.busy;
  const label = recipe.unlocked ? "开炉炼丹" : "参悟丹方";
  const needsMaterial = recipe.unlocked && !canAlchemy(recipe);
  return `
    <section class="counter-panel alchemy-counter">
      <div class="counter-visual">
        <div class="pill-orb">${pillIcon(recipe)}</div>
        <div>
          <span class="eyebrow">${recipe.unlocked ? "已参悟" : `参悟需 ${recipe.unlockPrice} 积分`}</span>
          <h2>${escapeHtml(recipe.productName)}</h2>
          <p>${recipe.effect ? escapeHtml(recipe.effect) : "丹方已收录"}</p>
        </div>
      </div>
      <div class="material-grid">
        ${recipe.materials.map((mat) => `
          <span class="material ${mat.enough ? "enough" : ""}">
            ${escapeHtml(mat.itemName)}
            <strong>${mat.owned}/${mat.need}</strong>
            <i class="material-meter" style="--value:${materialPercent(mat)}%"></i>
          </span>
        `).join("")}
      </div>
      ${renderMissingMaterialGuide(recipe)}
      <div class="counter-stats">
        <span>炉火 <strong>${recipe.alchemyCost}</strong></span>
        <span>成丹 <strong>${recipe.productInventory}</strong></span>
        <span>材料 <strong>${recipe.materials.filter((mat) => mat.enough).length}/${recipe.materials.length}</strong></span>
      </div>
      <div class="actions">
        <button class="btn" type="button" data-action="${action}" data-recipe="${escapeAttr(recipe.key)}" ${canRun ? "" : "disabled"}>${label}</button>
        ${needsMaterial ? `<button class="btn secondary" type="button" data-action="open-herbs">查看草药</button>` : ""}
      </div>
    </section>
  `;
}

function handleContentClick(event) {
  const button = event.target.closest("[data-action]");
  if (!button || !content.contains(button)) return;
  handleAction(button.dataset.action, button.dataset, button);
}

function handleAction(action, dataset, button) {
  if (!action) return;
  if (writeActions.has(action)) {
    if (app.busy) {
      setStatus("上一道园务还在处理，稍候再点", true);
      haptic("error");
      return;
    }
    if (app.usingCache || app.offline || app.offlineMode) {
      setStatus("当前显示的是离线园况，重连后才能提交操作", true);
      haptic("error");
      return;
    }
    if (button) button.disabled = true;
  }
  if (action === "select-plot") return handlePlotTap(Number(dataset.plot));
  if (action === "focus-plot") {
    app.selectedPlotNo = Number(dataset.plot);
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "locked-plot") {
    const plotNo = dataset.plot;
    const missing = Number(dataset.missing || 0);
    setStatus(missing > 0 ? `开垦 ${plotNo} 号田还差 ${missing} 积分` : "请按顺序开垦前一块灵田", missing > 0);
    haptic("selection");
    return;
  }
  if (action === "select-tool") return handleToolTap(dataset.tool);
  if (action === "farm-guide-primary") return handleFarmGuideTap();
  if (action === "retry-load") return loadState();
  if (action === "select-seed") {
    app.selectedSeedKey = dataset.seed;
    const seed = selectedSeed();
    if (seed) setStatus(`已选 ${seed.seedName}，可切回灵田播种`);
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "set-seed-mode") {
    app.seedShelfMode = dataset.mode || "all";
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "use-seed") {
    app.selectedSeedKey = dataset.seed;
    app.toolMode = "plant";
    switchTab("fields");
    return;
  }
  if (action === "quick-seed") {
    app.selectedSeedKey = dataset.seed;
    app.toolMode = "plant";
    const seed = selectedSeed();
    if (seed) setStatus(hasEmptyPlot() ? `已握好 ${seed.seedName}，可直接点空田播种` : `已握好 ${seed.seedName}，暂无空田`);
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "select-herb") {
    app.selectedHerbKey = dataset.seed;
    const herb = selectedHerb();
    if (herb) setStatus(`已查看 ${herb.herbName}，库存 ${herb.inventory}`);
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "set-herb-mode") {
    app.herbShelfMode = dataset.mode || "all";
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "select-recipe") {
    app.selectedRecipeKey = dataset.recipe;
    const recipe = selectedRecipe();
    if (recipe) setStatus(recipe.unlocked ? `已选 ${recipe.productName}` : `${recipe.name} 尚未参悟`);
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "set-recipe-mode") {
    app.recipeShelfMode = dataset.mode || "all";
    haptic("selection");
    requestStructureRender();
    return;
  }
  if (action === "find-material") {
    const itemName = dataset.item || "";
    const herb = app.state.herbs.find((entry) => entry.herbName === itemName);
    if (herb) {
      app.selectedHerbKey = herb.key;
      app.herbShelfMode = "all";
      setStatus(`已翻到 ${herb.herbName} 仓格`);
    } else {
      setStatus(`${itemName || "所需材料"} 暂未入仓`);
    }
    haptic("selection");
    switchTab("herbs");
    return;
  }
  if (action === "harvest-all") return runHarvestAllAction();
  if (action === "open-plot") return runAction("/api/garden/open-plot", {}, "灵田开垦成功");
  if (action === "buy-seed") return runAction("/api/garden/buy-seed", { seedKey: dataset.seed }, "种子已入袋");
  if (action === "plant") return runAction("/api/garden/plant", { plotNo: Number(dataset.plot), seedKey: dataset.seed }, "种植成功");
  if (action === "plant-all") return runAction("/api/garden/plant-all", { seedKey: dataset.seed }, "一键种植完成");
  if (action === "harvest") return runAction("/api/garden/harvest", { plotNo: Number(dataset.plot) }, "收获成功");
  if (action === "sell-one") return runAction("/api/garden/sell-herb", { seedKey: dataset.seed, quantity: 1 }, "药草回收完成");
  if (action === "sell-custom") {
    const quantity = readMarketSellQuantity(dataset.seed);
    if (quantity <= 0) {
      setStatus("请输入有效的回收数量", true);
      haptic("error");
      return;
    }
    return runAction("/api/garden/sell-herb", { seedKey: dataset.seed, quantity }, "药草回收完成");
  }
  if (action === "open-seeds") return switchTab("seeds");
  if (action === "open-herbs") return switchTab("herbs");
  if (action === "open-market") return switchTab("market");
  if (action === "open-recipes") return switchTab("recipes");
  if (action === "buy-recipe") return runAction("/api/garden/buy-recipe", { recipeKey: dataset.recipe }, "丹方已参悟");
  if (action === "alchemy") return runAction("/api/garden/alchemy", { recipeKey: dataset.recipe }, "炼丹完成");
}

function handleToolTap(tool) {
  if (tool === "market") {
    app.toolMode = "inspect";
    switchTab("market");
    return;
  }
  if (tool === "plant") {
    const seed = selectedSeed();
    if (!seed || seed.inventory <= 0) {
      app.toolMode = "inspect";
      setStatus("先去种子商店备好种子");
      switchTab("seeds");
      return;
    }
  }
  app.toolMode = tool || "inspect";
  haptic("selection");
  requestStructureRender();
}

function handleFarmGuideTap() {
  const seed = selectedSeed();
  const guide = farmGuidePlan(seed, readyPlotCount(), emptyPlotCount());
  haptic("selection");
  if (guide.kind === "harvest") {
    const plot = nextReadyPlot();
    if (plot) app.selectedPlotNo = plot.plotNo;
    app.toolMode = "harvest";
    setStatus(plot ? `已对准 ${plot.plotNo} 号成熟灵田` : "暂无成熟灵田");
    requestStructureRender();
    return;
  }
  if (guide.kind === "plant") {
    const plot = nextEmptyPlot();
    if (plot) app.selectedPlotNo = plot.plotNo;
    app.toolMode = "plant";
    setStatus(plot && seed ? `已对准 ${plot.plotNo} 号空田，可播 ${seed.seedName}` : "暂无可播灵田");
    requestStructureRender();
    return;
  }
  if (guide.kind === "seed") {
    setStatus("先去种子货架补些灵种");
    switchTab("seeds");
    return;
  }
  if (guide.kind === "market") {
    const offer = firstMatchedMarketOffer();
    if (offer) app.selectedHerbKey = offer.seedKey;
    setStatus(offer ? `已翻到 ${offer.herbName} 急收行情` : "打开药铺行情");
    switchTab("market");
    return;
  }
  if (guide.kind === "alchemy") {
    const recipe = app.state.recipes.find((item) => item.unlocked && canAlchemy(item));
    if (recipe) app.selectedRecipeKey = recipe.key;
    app.recipeShelfMode = "ready";
    setStatus(recipe ? `已选 ${recipe.productName}` : "打开丹炉查看丹方");
    switchTab("recipes");
    return;
  }
  setStatus(guide.detail);
}

function handlePlotTap(plotNo) {
  const plot = app.state ? app.state.plots.find((item) => item.plotNo === plotNo) : null;
  if (!plot) return;
  app.selectedPlotNo = plotNo;
  haptic("selection");

  if (app.toolMode === "plant" && plot.status === "empty") {
    if (app.busy || app.usingCache || app.offline || app.offlineMode) {
      setStatus(app.busy ? "上一道园务还在处理，稍候再点" : "当前显示的是离线园况，重连后才能提交操作", true);
      haptic("error");
      return;
    }
    const seed = selectedSeed();
    if (!seed || seed.inventory <= 0) {
      setStatus("先准备一枚可种的种子", true);
      requestStructureRender();
      return;
    }
    runAction("/api/garden/plant", { plotNo, seedKey: seed.key }, "种植成功");
    return;
  }

  if (app.toolMode === "harvest" && plot.status === "ready") {
    if (app.busy || app.usingCache || app.offline || app.offlineMode) {
      setStatus(app.busy ? "上一道园务还在处理，稍候再点" : "当前显示的是离线园况，重连后才能提交操作", true);
      haptic("error");
      return;
    }
    runAction("/api/garden/harvest", { plotNo }, "收获成功");
    return;
  }

  requestStructureRender();
}

function switchTab(tab) {
  if (!tabOrder.includes(tab)) return;
  const previousTab = app.tab;
  if (previousTab === tab) {
    syncActiveTab();
    return;
  }
  app.tabMotion = tabDirection(previousTab, tab);
  app.tab = tab;
  syncActiveTab();
  ensureSelections();
  app.dirty.structure = true;
  render();
}

function tabDirection(previousTab, nextTab) {
  const previousIndex = tabOrder.indexOf(previousTab);
  const nextIndex = tabOrder.indexOf(nextTab);
  if (previousIndex < 0 || nextIndex < 0 || previousIndex === nextIndex) return "none";
  return nextIndex > previousIndex ? "forward" : "back";
}

function applyContentMotion() {
  if (!content) return;
  const motion = app.tabMotion || "none";
  content.dataset.motion = motion;
  if (app.motionTimer) {
    window.clearTimeout(app.motionTimer);
    app.motionTimer = null;
  }
  app.tabMotion = "none";
  if (motion === "none") return;
  app.motionTimer = window.setTimeout(() => {
    content.dataset.motion = "none";
    app.motionTimer = null;
  }, 360);
}

function syncActiveTab() {
  if (bottomDock) {
    bottomDock.querySelectorAll("[data-tab]").forEach((item) => item.classList.toggle("active", item.dataset.tab === app.tab));
  }
}

function renderTabs(options = {}) {
  if (!options.force && app.nodes.get("dock-fields")) {
    patchDock();
    return;
  }
  if (bottomDock) {
    bottomDock.innerHTML = Object.keys(tabMeta).map((tab) => {
      const meta = tabMeta[tab];
      const count = meta.count();
      const tone = dockTone(tab);
      return `
        <button class="dock-tab ${tone} ${app.tab === tab ? "active" : ""}" type="button" data-tab="${tab}" data-leave="dock-${tab}" aria-label="${meta.label}">
          <i class="dock-light" aria-hidden="true"></i>
          <span>${meta.icon}</span>
          <strong>${meta.label}</strong>
          <em>${dockHint(tab)}</em>
          ${count > 0 ? `<b>${count > 99 ? "99+" : count}</b>` : ""}
        </button>
      `;
    }).join("");
  }
  syncActiveTab();
}

function dockHint(tab) {
  if (!app.state) return "同步中";
  if (tab === "fields") {
    const readyCount = readyPlotCount();
    if (readyCount > 0) return `${readyCount} 可收`;
    const emptyCount = emptyPlotCount();
    if (emptyCount > 0) return `${emptyCount} 空田`;
    const next = nextMaturePlot();
    return next ? formatRemaining(next.remainingSeconds) : "打理";
  }
  if (tab === "seeds") {
    const buyable = app.state.seeds.filter((seed) => seed.purchasable && seed.leftToday > 0 && seed.price <= app.state.points).length;
    return buyable > 0 ? `${buyable} 可买` : "货架";
  }
  if (tab === "herbs") {
    const stocked = app.state.herbs.filter((herb) => herb.inventory > 0).length;
    return stocked > 0 ? `${stocked} 有货` : "仓库";
  }
  if (tab === "market") {
    const offer = firstMatchedMarketOffer();
    if (offer) return "可急收";
    const active = app.state.market.filter((item) => item.left > 0).length;
    return active > 0 ? `${active} 行情` : "回收";
  }
  const ready = app.state.recipes.filter((recipe) => recipe.unlocked && canAlchemy(recipe)).length;
  return ready > 0 ? `${ready} 可炼` : "丹炉";
}

function dockTone(tab) {
  if (!app.state) return "dock-idle";
  if (tab === "fields") {
    if (readyPlotCount() > 0) return "dock-hot";
    if (emptyPlotCount() > 0) return "dock-seed";
    return "dock-grow";
  }
  if (tab === "seeds") {
    const buyable = app.state.seeds.some((seed) => seed.purchasable && seed.leftToday > 0 && seed.price <= app.state.points);
    return buyable ? "dock-seed" : "dock-idle";
  }
  if (tab === "herbs") {
    return app.state.herbs.some((herb) => herb.inventory > 0) ? "dock-grow" : "dock-idle";
  }
  if (tab === "market") {
    return firstMatchedMarketOffer() ? "dock-market" : "dock-idle";
  }
  const ready = app.state.recipes.some((recipe) => recipe.unlocked && canAlchemy(recipe));
  return ready ? "dock-alchemy" : "dock-idle";
}

function renderToolButton(mode, icon, label) {
  return `
    <button class="tool ${app.toolMode === mode ? "active" : ""}" type="button" data-action="select-tool" data-tool="${mode}" aria-label="${label}">
      <span>${icon}</span>
      <em>${label}</em>
    </button>
  `;
}

function gardenToolHint(seed) {
  if (app.toolMode === "plant") {
    return seed && seed.inventory > 0 ? `播种模式：点空田种下 ${escapeHtml(seed.seedName)}` : "播种模式：先去种子商店买种子";
  }
  if (app.toolMode === "harvest") return "收获模式：点成熟灵田直接收获";
  return "点击地块查看详情，或选择工具后连续打理";
}

function farmModeInfo(seed, readyCount, emptyCount) {
  if (app.toolMode === "plant") {
    const canPlant = seed && seed.inventory > 0 && emptyCount > 0;
    return {
      kind: canPlant ? "mode-ready" : "mode-warn",
      icon: "🌰",
      title: canPlant ? "播种工具已拿起" : "播种前先备种",
      detail: canPlant ? `点空田种下 ${seed.seedName}，可连续打理` : (seed ? `${seed.seedName} 库存不足或暂无空田` : "先去种子商店挑一枚灵种"),
      meta: canPlant ? `${Math.min(seed.inventory, emptyCount)} 块可播` : "不可播",
    };
  }
  if (app.toolMode === "harvest") {
    return {
      kind: readyCount > 0 ? "mode-hot" : "mode-calm",
      icon: "🧺",
      title: readyCount > 0 ? "收获工具已就绪" : "暂时没有成熟田",
      detail: readyCount > 0 ? "点成熟地块即可收进背包，也可一键收成熟" : "成熟后这里会亮起收获提示",
      meta: readyCount > 0 ? `${readyCount} 块可收` : "等待",
    };
  }
  return {
    kind: "mode-calm",
    icon: "✋",
    title: "手势查看模式",
    detail: "点地块看详情，切换工具后可连续播种或收获",
    meta: "巡园",
  };
}

function cropStageName(plot) {
  const stage = cropStage(plot);
  if (stage <= 1) return "发芽";
  if (stage === 2) return "抽枝";
  if (stage === 3) return "将熟";
  return "成熟";
}

function gardenPhase() {
  const date = app.state && app.state.serverTime ? new Date(app.state.serverTime) : new Date();
  const hour = Number.isNaN(date.getHours()) ? new Date().getHours() : date.getHours();
  if (hour >= 5 && hour < 11) return "morning";
  if (hour >= 11 && hour < 18) return "day";
  if (hour >= 18 && hour < 21) return "dusk";
  return "night";
}

function gardenPhaseName(phase) {
  if (phase === "morning") return "灵圃晨光";
  if (phase === "day") return "晴昼灵田";
  if (phase === "dusk") return "暮色药园";
  return "星露灵圃";
}

function farmSceneTitle(readyCount, emptyCount) {
  if (readyCount > 0) return `${readyCount} 块灵田可收获`;
  if (emptyCount > 0) return `${emptyCount} 块空田待播种`;
  return "灵田运转良好";
}

function farmTaskTitle(readyCount, emptyCount, urgentCount, alchemyReady) {
  if (readyCount > 0) return "成熟灵草等你收";
  if (emptyCount > 0) return "空田可以继续播";
  if (alchemyReady > 0) return "丹炉材料已齐备";
  if (urgentCount > 0) return "药铺急收可查看";
  return "今日园务已清爽";
}

function farmFeedItems(seed, readyCount, emptyCount) {
  const items = [];
  const readyPlot = app.state.plots.find((plot) => plot.status === "ready");
  if (readyPlot) {
    items.push({
      kind: "feed-ready",
      icon: "收",
      title: `${readyPlot.plotNo} 号田成熟`,
      detail: `${readyPlot.herbName} 可收进背包`,
      meta: readyCount > 1 ? `另有 ${readyCount - 1} 块` : "现在",
      action: "focus-plot",
      plotNo: readyPlot.plotNo,
    });
  }

  const emptyPlot = app.state.plots.find((plot) => plot.status === "empty");
  if (emptyPlot) {
    items.push({
      kind: "feed-seed",
      icon: "种",
      title: `${emptyPlot.plotNo} 号田可补种`,
      detail: seed && seed.inventory > 0 ? `可用 ${seed.seedName} x${seed.inventory}` : "先去种子铺补货",
      meta: `${emptyCount} 空田`,
      action: "focus-plot",
      plotNo: emptyPlot.plotNo,
    });
  }

  const matched = firstMatchedMarketOffer();
  if (matched) {
    items.push({
      kind: "feed-market",
      icon: "市",
      title: `${matched.herbName} 急收可处理`,
      detail: `药铺剩余额度 ${matched.left}`,
      meta: "药铺",
      action: "open-market",
    });
  }

  const recipe = app.state.recipes.find((item) => item.unlocked && canAlchemy(item));
  if (recipe) {
    items.push({
      kind: "feed-alchemy",
      icon: "丹",
      title: `${recipe.productName} 可开炉`,
      detail: `材料 ${recipe.materials.length}/${recipe.materials.length}`,
      meta: "丹炉",
      action: "open-recipes",
    });
  }

  const next = nextMaturePlot();
  if (next && !items.some((item) => item.plotNo === next.plotNo)) {
    items.push({
      kind: "feed-grow",
      icon: "时",
      title: `${next.plotNo} 号田快成熟`,
      detail: `${formatRemaining(next.remainingSeconds)} 后可收 ${next.herbName}`,
      meta: formatShortTime(next.maturesAt),
      action: "focus-plot",
      plotNo: next.plotNo,
    });
  }

  if (items.length === 0) {
    items.push({
      kind: "feed-calm",
      icon: "巡",
      title: "园区运转平稳",
      detail: "暂无紧急园务，保持巡园即可",
      meta: formatFarmClock(),
      action: "select-tool",
      mode: "inspect",
    });
  }

  return items.slice(0, 4);
}

function farmMapTitle(readyCount, emptyCount) {
  if (readyCount > 0) return `${readyCount} 块金光熟田`;
  if (emptyCount > 0) return `${emptyCount} 块空田待轮作`;
  return "满园灵草生长中";
}

function yardKeeperLine(readyCount, emptyCount) {
  if (readyCount > 0) {
    return {
      kind: "keeper-hot",
      title: "管事提醒",
      detail: "熟田亮着，先收再播",
    };
  }
  if (emptyCount > 0) {
    return {
      kind: "keeper-seed",
      title: "管事提醒",
      detail: "空田别闲着，补一轮灵种",
    };
  }
  const next = nextMaturePlot();
  return {
    kind: "keeper-calm",
    title: "管事巡园",
    detail: next ? `${next.plotNo} 号田还需 ${formatRemaining(next.remainingSeconds)}` : "灵气稳定，等候成熟",
  };
}

function gardenPulseText() {
  if (!app.state) return "同步园况中";
  const readyCount = readyPlotCount();
  const emptyCount = emptyPlotCount();
  if (readyCount > 0) return `${readyCount} 块灵田成熟，先收获`;
  if (emptyCount > 0 && app.state.counts.seedInventory > 0) return `${emptyCount} 块空田待播，种子已备`;
  if (emptyCount > 0) return `${emptyCount} 块空田待播，先补种`;
  const offer = firstMatchedMarketOffer();
  if (offer) return `${offer.herbName} 急收可处理`;
  const recipe = app.state.recipes.find((item) => item.unlocked && canAlchemy(item));
  if (recipe) return `${recipe.productName} 材料已齐`;
  const next = nextMaturePlot();
  if (next) return `${next.plotNo} 号田 ${formatRemaining(next.remainingSeconds)} 后成熟`;
  return "园务清爽，灵气稳定";
}

function farmPlantAllHint(seed, emptyCount, plantCount) {
  if (!seed) return "先买种";
  if (seed.inventory <= 0) return "无库存";
  if (emptyCount <= 0) return "无空田";
  return `${plantCount} 块`;
}

function formatFarmClock() {
  const date = app.state && app.state.serverTime ? new Date(app.state.serverTime) : new Date();
  const safe = Number.isNaN(date.getTime()) ? new Date() : date;
  const hours = String(safe.getHours()).padStart(2, "0");
  const minutes = String(safe.getMinutes()).padStart(2, "0");
  return `${hours}:${minutes}`;
}

function tileActionLabel(plot, progress) {
  if (!plot || plot.status === "empty") {
    return app.toolMode === "plant" ? "种" : "空";
  }
  if (plot.status === "ready") return app.toolMode === "harvest" ? "收" : "熟";
  return `${progress}%`;
}

function tileActionKind(plot) {
  if (!plot || plot.status === "empty") {
    return app.toolMode === "plant" ? "plant-badge" : "empty-badge";
  }
  if (plot.status === "ready") return "harvest-badge";
  return "grow-badge";
}

function tileStatusTag(plot, progress) {
  if (!plot || plot.status === "empty") {
    const seed = selectedSeed();
    return {
      kind: "tag-empty",
      label: app.toolMode === "plant" && seed && seed.inventory > 0 ? "可播" : "空田",
      meta: seed && seed.inventory > 0 ? seed.seedName : "先备种",
    };
  }
  if (plot.status === "ready") {
    return {
      kind: "tag-ready",
      label: "成熟",
      meta: "立即收",
    };
  }
  return {
    kind: "tag-grow",
    label: cropStageName(plot),
    meta: progress >= 75 ? "将熟" : formatShortTime(plot.maturesAt),
  };
}

function tileToolTip(plot, progress) {
  if (!plot || plot.status === "empty") {
    const seed = selectedSeed();
    if (app.toolMode === "plant" && seed && seed.inventory > 0) {
      return {
        kind: "tip-hot",
        label: "点田播种",
        meta: seed.seedName,
      };
    }
    return {
      kind: "tip-calm",
      label: "空田待播",
      meta: seed ? `库存 ${seed.inventory}` : "去买种",
    };
  }
  if (plot.status === "ready") {
    return {
      kind: app.toolMode === "harvest" ? "tip-hot" : "tip-ready",
      label: app.toolMode === "harvest" ? "点田收获" : "成熟可收",
      meta: plot.herbName,
    };
  }
  return {
    kind: progress >= 75 ? "tip-soon" : "tip-grow",
    label: progress >= 75 ? "即将成熟" : "生长中",
    meta: formatRemaining(plot.remainingSeconds),
  };
}

function plotAdvice(plot, seed) {
  if (!plot || plot.status === "empty") {
    if (seed && seed.inventory > 0) {
      return {
        kind: "advice-seed",
        icon: "🌰",
        title: "可以立即播种",
        detail: `${seed.seedName} 库存 ${seed.inventory} 枚，点下方按钮即可种下`,
      };
    }
    return {
      kind: "advice-empty",
      icon: "🏪",
      title: "这块田还空着",
      detail: "先去种子商店补货，再回来播种",
    };
  }
  if (plot.status === "ready") {
    return {
      kind: "advice-ready",
      icon: "🧺",
      title: "现在是最佳收获时机",
      detail: "收获后空田可继续轮作",
    };
  }
  return {
    kind: "advice-grow",
    icon: "⏳",
    title: `${cropStageName(plot)}阶段`,
    detail: `${formatRemaining(plot.remainingSeconds)} 后成熟，预计 ${formatShortTime(plot.maturesAt)}`,
  };
}

function plotStatusInfo(plot, seed) {
  if (!plot) {
    return {
      kind: "status-empty",
      title: "灵田未选中",
      detail: "点一块田查看状态",
      meta: "巡园",
    };
  }
  if (plot.status === "empty") {
    if (seed && seed.inventory > 0) {
      return {
        kind: "status-seed",
        title: `${plot.plotNo} 号田可播种`,
        detail: `当前手里是 ${seed.seedName}`,
        meta: `x${seed.inventory}`,
      };
    }
    return {
      kind: "status-empty",
      title: `${plot.plotNo} 号田空着`,
      detail: "先去种子铺补种",
      meta: "待播",
    };
  }
  if (plot.status === "ready") {
    return {
      kind: "status-ready",
      title: `${plot.plotNo} 号田已成熟`,
      detail: `${plot.herbName} 可以收进背包`,
      meta: "可收",
    };
  }
  return {
    kind: "status-grow",
    title: `${plot.plotNo} 号田生长中`,
    detail: `${cropStageName(plot)} · 还需 ${formatRemaining(plot.remainingSeconds)}`,
    meta: `${progressValue(plot)}%`,
  };
}

function plotCoachInfo(plot, seed) {
  if (!plot || plot.status === "empty") {
    if (app.toolMode === "plant" && seed && seed.inventory > 0) {
      return {
        kind: "coach-seed",
        title: "管事递来种袋",
        detail: "点播种按钮或直接点空田即可种下当前灵种",
      };
    }
    return {
      kind: "coach-calm",
      title: "管事在田埂等候",
      detail: seed ? "切到播种工具后可连续补田" : "先去种子铺挑一枚灵种",
    };
  }
  if (plot.status === "ready") {
    return {
      kind: "coach-ready",
      title: "管事举起竹篮",
      detail: app.toolMode === "harvest" ? "点成熟田可连续收获" : "切到收获工具会更顺手",
    };
  }
  return {
    kind: "coach-grow",
    title: "管事轻声巡田",
    detail: `等 ${formatRemaining(plot.remainingSeconds)} 后再来收获`,
  };
}

function tickGardenClock() {
  if (!app.state || document.hidden) return;
  let changed = false;
  app.state.plots.forEach((plot) => {
    if (plot.status === "growing" && plot.remainingSeconds > 0) {
      plot.remainingSeconds -= 1;
      changed = true;
      if (plot.remainingSeconds <= 0) {
        plot.remainingSeconds = 0;
        plot.status = "ready";
      }
      const tileEl = content.querySelector(`[data-leave="plot-${plot.plotNo}"]`);
      if (tileEl) patchPlot(tileEl, plot);
    }
  });
  if (!changed) return;
  app.state.counts.readyPlots = readyPlotCount();
  patchSummary();
  patchDock();
  patchOwner();
  updateGardenClockDOM();
}

function updateGardenClockDOM() {
  if (!app.state) return;
  if (gardenPulseEl) gardenPulseEl.textContent = gardenPulseText();
  app.state.plots.forEach((plot) => {
    const ready = plot.status === "ready";
    const empty = plot.status === "empty";
    const progress = empty ? 0 : ready ? 100 : progressValue(plot);
    const statusText = app.toolMode === "plant" && empty ? "点此播种" : app.toolMode === "harvest" && ready ? "点此收获" : empty ? "空闲" : ready ? "可收获" : formatRemaining(plot.remainingSeconds);
    updateText(`[data-clock-remaining="${plot.plotNo}"]`, statusText);
    updateText(`[data-clock-badge="${plot.plotNo}"]`, tileActionLabel(plot, progress));
    updateText(`[data-clock-tag="${plot.plotNo}"]`, tileStatusTag(plot, progress).meta);
    updateText(`[data-clock-tip="${plot.plotNo}"]`, tileToolTip(plot, progress).meta);
    updateText(`[data-clock-timeline="${plot.plotNo}"]`, timelinePlotMeta(plot));
    updateText(`[data-clock-status-dial="${plot.plotNo}"]`, ready ? "收" : empty ? "种" : `${progress}%`);
    updateText(`[data-clock-status-detail="${plot.plotNo}"]`, plotStatusInfo(plot, selectedSeed()).detail);
    updateText(`[data-clock-selected-progress="${plot.plotNo}"]`, `${progress}%`);
    updateText(`[data-clock-selected-remaining="${plot.plotNo}"]`, ready ? "可立即收获入袋" : empty ? "空田可补种" : `剩余 ${formatRemaining(plot.remainingSeconds)} · ${plot.herbName || "灵种"}`);
    updateText(`[data-clock-quick="${plot.plotNo}"]`, quickPlotSubtitle(plot));
    updateProgress(`[data-clock-progress="${plot.plotNo}"]`, progress);
  });
  const next = nextMaturePlot();
  updateText("[data-clock-next]", next ? `下一块 ${formatRemaining(next.remainingSeconds)}` : "巡园");
}

function cacheLeaveNodes(root = document) {
  if (root === document) app.nodes.clear();
  root.querySelectorAll("[data-leave]").forEach((node) => {
    app.nodes.set(node.dataset.leave, node);
  });
}

function markStateDirty(previousPlotCount) {
  const nextPlotCount = app.state && Array.isArray(app.state.plots) ? app.state.plots.length : 0;
  app.dirty.structure = previousPlotCount !== nextPlotCount || !content.querySelector("[data-leave]");
  app.dirty.plots = true;
  app.dirty.dock = true;
  app.dirty.owner = true;
  app.dirty.summary = true;
}

function canPatchCurrentView() {
  return app.state && app.tab === "fields" && !app.dirty.structure && Boolean(content.querySelector("[data-leave]"));
}

function patchState() {
  patchSummary();
  app.state.plots.forEach((plot) => {
    const tileEl = content.querySelector(`[data-leave="plot-${plot.plotNo}"]`);
    if (tileEl) patchPlot(tileEl, plot);
  });
  patchDock();
  patchOwner();
  updateGardenClockDOM();
}

function patchPlot(tileEl, plot) {
  if (!tileEl || !plot) return;
  const ready = plot.status === "ready";
  const empty = plot.status === "empty";
  const stage = cropStage(plot);
  const progress = empty ? 0 : ready ? 100 : progressValue(plot);
  const badge = tileEl.querySelector(".tile-action-badge");
  const status = tileEl.querySelector(".tile-status");
  const soil = tileEl.querySelector(".soil");
  const crop = tileEl.querySelector(".crop");

  setNodeText(status, plotTileStatusText(plot));
  if (badge) {
    setNodeText(badge, tileActionLabel(plot, progress));
    badge.classList.remove("plant-badge", "empty-badge", "harvest-badge", "grow-badge");
    badge.classList.add(tileActionKind(plot));
  }
  if (soil) {
    soil.classList.remove("soil-empty", "soil-growing", "soil-ready", "stage-0", "stage-1", "stage-2", "stage-3", "stage-4");
    soil.classList.add(`soil-${plot.status}`, `stage-${stage}`);
  }
  if (crop) {
    const previousStage = Number(crop.dataset.cropStage || tileEl.dataset.stage || -1);
    crop.classList.remove("stage-0", "stage-1", "stage-2", "stage-3", "stage-4");
    crop.classList.add(`stage-${stage}`);
    if (previousStage !== stage) {
      const seed = app.state.seeds.find((item) => item.key === plot.seedKey);
      crop.innerHTML = cropIcon(plot, seed);
      crop.dataset.cropStage = String(stage);
    }
  }

  tileEl.classList.remove("empty", "growing", "ready", "crop-stage-0", "crop-stage-1", "crop-stage-2", "crop-stage-3", "crop-stage-4");
  tileEl.classList.add(plot.status, `crop-stage-${stage}`);
  tileEl.classList.toggle("ready", ready);
  tileEl.dataset.stage = String(stage);
  tileEl.dataset.status = plot.status;
}

function patchSummary() {
  if (!app.state) return;
  if (pointsEl) setNodeText(pointsEl, app.state.points);
  if (plotCountEl) setNodeText(plotCountEl, `${app.state.counts.plots}/${maxGardenPlots}`);
  if (readyCountEl) setNodeText(readyCountEl, app.state.counts.readyPlots);
  if (gardenPulseEl) setNodeText(gardenPulseEl, gardenPulseText());
  app.dirty.summary = false;
}

function patchDock() {
  if (!bottomDock || !app.state) return;
  tabOrder.forEach((tab) => {
    const node = app.nodes.get(`dock-${tab}`) || bottomDock.querySelector(`[data-leave="dock-${tab}"]`);
    if (!node) return;
    const meta = tabMeta[tab];
    const count = meta.count();
    node.classList.toggle("active", app.tab === tab);
    node.classList.remove("dock-idle", "dock-hot", "dock-seed", "dock-grow", "dock-market", "dock-alchemy");
    node.classList.add(dockTone(tab));
    setNodeText(node.querySelector("em"), dockHint(tab));
    let badge = node.querySelector("b");
    if (count > 0) {
      if (!badge) {
        badge = document.createElement("b");
        node.appendChild(badge);
      }
      setNodeText(badge, count > 99 ? "99+" : count);
    } else if (badge) {
      badge.remove();
    }
  });
  syncActiveTab();
  app.dirty.dock = false;
}

function patchOwner() {
  if (!ownerPanel || !app.state || !app.nodes.get("owner-headline")) return;
  const readyCount = readyPlotCount();
  const emptyCount = emptyPlotCount();
  const seed = selectedSeed();
  const headline = ownerPanelHeadline(readyCount, emptyCount, nextMaturePlot());
  const action = ownerPanelAction(readyCount, emptyCount, seed);
  setNodeText(app.nodes.get("owner-headline"), headline.title);
  setNodeText(app.nodes.get("owner-detail"), headline.detail);
  setNodeText(app.nodes.get("owner-ready"), readyCount);
  setNodeText(app.nodes.get("owner-empty"), emptyCount);
  setNodeText(app.nodes.get("owner-seed"), seed ? seed.inventory : 0);
  const actionEl = app.nodes.get("owner-action");
  if (actionEl) {
    actionEl.dataset.action = action.action;
    setOptionalDataset(actionEl, "plot", action.plotNo);
    setOptionalDataset(actionEl, "seed", action.seedKey);
  }
  setNodeText(app.nodes.get("owner-action-label"), action.label);
  setNodeText(app.nodes.get("owner-action-detail"), action.detail);
  app.dirty.owner = false;
}

function setOptionalDataset(node, key, value) {
  if (!node) return;
  if (value === undefined || value === null || value === "") {
    delete node.dataset[key];
  } else {
    node.dataset[key] = String(value);
  }
}

function setNodeText(node, text) {
  if (!node) return;
  const next = String(text == null ? "" : text);
  if (node.textContent !== next) node.textContent = next;
}

function plotTileStatusText(plot) {
  if (!plot) return "";
  if (app.toolMode === "plant" && plot.status === "empty") return "点此播种";
  if (app.toolMode === "harvest" && plot.status === "ready") return "点此收获";
  if (plot.status === "empty") return "空闲";
  if (plot.status === "ready") return "可收获";
  return formatRemaining(plot.remainingSeconds);
}

function updateText(selector, text) {
  document.querySelectorAll(selector).forEach((node) => {
    const next = String(text == null ? "" : text);
    if (node.textContent !== next) node.textContent = next;
  });
}

function updateProgress(selector, progress) {
  document.querySelectorAll(selector).forEach((node) => {
    node.style.setProperty("--value", `${progress}%`);
  });
}

function quickPlotSubtitle(plot) {
  if (!plot) return "";
  if (plot.status === "empty") {
    const activeSeed = selectedSeed();
    return activeSeed ? `手上 ${activeSeed.seedName} · 库存 ${activeSeed.inventory}` : "先去备种";
  }
  if (plot.status === "ready") return "成熟可收，点击入袋";
  const plantedSeed = app.state.seeds.find((seed) => seed.key === plot.seedKey);
  return `${formatRemaining(plot.remainingSeconds)} 后成熟 · ${plantedSeed ? plantedSeed.seedName : "灵种"}`;
}

function ensureSelections() {
  if (!app.state) return;
  if (!hasPlot(app.selectedPlotNo)) app.selectedPlotNo = preferredPlotNo();
  if (!app.state.seeds.some((seed) => seed.key === app.selectedSeedKey)) {
    app.selectedSeedKey = preferredSeedKey();
  } else {
    const currentSeed = app.state.seeds.find((seed) => seed.key === app.selectedSeedKey);
    if (currentSeed && currentSeed.inventory <= 0 && app.state.seeds.some((seed) => seed.inventory > 0)) {
      app.selectedSeedKey = preferredSeedKey();
    }
  }
  if (!app.state.herbs.some((herb) => herb.key === app.selectedHerbKey)) {
    const stocked = app.state.herbs.find((herb) => herb.inventory > 0);
    app.selectedHerbKey = stocked ? stocked.key : (app.state.herbs[0] ? app.state.herbs[0].key : null);
  }
  if (!app.state.recipes.some((recipe) => recipe.key === app.selectedRecipeKey)) {
    const ready = app.state.recipes.find((recipe) => recipe.unlocked && canAlchemy(recipe));
    app.selectedRecipeKey = ready ? ready.key : (app.state.recipes[0] ? app.state.recipes[0].key : null);
  }
}

function selectedPlot() {
  if (!app.state) return null;
  return app.state.plots.find((item) => item.plotNo === app.selectedPlotNo) || app.state.plots[0] || null;
}

function selectedSeed() {
  if (!app.state) return null;
  return app.state.seeds.find((item) => item.key === app.selectedSeedKey) || app.state.seeds.find((seed) => seed.key === preferredSeedKey()) || null;
}

function selectedHerb() {
  if (!app.state) return null;
  return app.state.herbs.find((item) => item.key === app.selectedHerbKey) || app.state.herbs[0] || null;
}

function marketSelectedHerb() {
  if (!app.state) return null;
  const selected = app.state.herbs.find((item) => item.key === app.selectedHerbKey);
  if (selected) return selected;
  const matched = firstMatchedMarketOffer();
  if (matched) return app.state.herbs.find((item) => item.key === matched.seedKey) || null;
  const activeOffer = app.state.market.find((offer) => offer.left > 0);
  if (activeOffer) return app.state.herbs.find((item) => item.key === activeOffer.seedKey) || null;
  return app.state.herbs.find((herb) => herb.inventory > 0) || app.state.herbs[0] || null;
}

function selectedRecipe() {
  if (!app.state) return null;
  return app.state.recipes.find((item) => item.key === app.selectedRecipeKey) || app.state.recipes[0] || null;
}

function readMarketSellQuantity(seedKey) {
  const input = Array.from(content.querySelectorAll("[data-sell-qty]")).find((node) => node.dataset.sellQty === seedKey);
  if (!input) return 0;
  const qty = Math.floor(Number(input.value || 0));
  const max = Math.floor(Number(input.max || 0));
  if (qty <= 0) return 0;
  if (max > 0 && qty > max) return max;
  return qty;
}

function herbSellPreview(herb, requestedQty) {
  const inventory = Math.max(0, Number(herb.inventory || 0));
  const qty = Math.max(0, Math.min(inventory, Math.floor(Number(requestedQty || 0))));
  const urgentQty = herb.urgent ? Math.min(qty, Math.max(0, Number(herb.marketLeft || 0))) : 0;
  const baseQty = Math.max(0, qty - urgentQty);
  const urgentPrice = Number(herb.marketPrice || 0);
  const basePrice = Number(herb.basePrice || 0);
  return {
    urgentQty,
    baseQty,
    total: urgentQty * urgentPrice + baseQty * basePrice,
  };
}

function herbMarketPercent(herb) {
  if (!herb || !herb.urgent) return 0;
  const limit = Math.max(0, Number(herb.marketLimit || 0));
  if (limit <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((Number(herb.marketLeft || 0) / limit) * 100)));
}

function renderMissingMaterialGuide(recipe) {
  if (!recipe.unlocked) return "";
  const missing = recipe.materials.filter((mat) => !mat.enough);
  if (missing.length === 0) return "";
  return `
    <div class="missing-guide">
      <span>寻药清单</span>
      <div>
        ${missing.map((mat) => `
          <button type="button" data-action="find-material" data-item="${escapeAttr(mat.itemName)}">
            ${escapeHtml(mat.itemName)}
            <em>缺 ${Math.max(0, mat.need - mat.owned)}</em>
          </button>
        `).join("")}
      </div>
    </div>
  `;
}

function firstPlotNo() {
  return app.state && app.state.plots.length > 0 ? app.state.plots[0].plotNo : null;
}

function preferredPlotNo() {
  if (!app.state || app.state.plots.length === 0) return null;
  const ready = app.state.plots.find((plot) => plot.status === "ready");
  if (ready) return ready.plotNo;
  const empty = app.state.plots.find((plot) => plot.status === "empty");
  return empty ? empty.plotNo : firstPlotNo();
}

function preferredSeedKey() {
  if (!app.state || app.state.seeds.length === 0) return null;
  const stocked = app.state.seeds.find((seed) => seed.inventory > 0);
  if (stocked) return stocked.key;
  const buyable = app.state.seeds.find((seed) => seed.purchasable && seed.leftToday > 0 && seed.price <= app.state.points);
  return buyable ? buyable.key : app.state.seeds[0].key;
}

function hasPlot(plotNo) {
  return app.state && app.state.plots.some((plot) => plot.plotNo === plotNo);
}

function emptyPlotCount() {
  return app.state ? app.state.plots.filter((plot) => plot.status === "empty").length : 0;
}

function readyPlotCount() {
  return app.state ? app.state.plots.filter((plot) => plot.status === "ready").length : 0;
}

function nextReadyPlot() {
  return app.state ? app.state.plots.find((plot) => plot.status === "ready") || null : null;
}

function nextEmptyPlot() {
  return app.state ? app.state.plots.find((plot) => plot.status === "empty") || null : null;
}

function hasEmptyPlot() {
  return emptyPlotCount() > 0;
}

function firstMatchedMarketOffer() {
  if (!app.state) return null;
  const offers = Array.isArray(app.state.market) ? app.state.market : [];
  const herbs = Array.isArray(app.state.herbs) ? app.state.herbs : [];
  return offers.find((offer) => {
    if (offer.left <= 0) return false;
    const herb = herbs.find((item) => item.key === offer.seedKey);
    return herb && herb.inventory > 0;
  }) || null;
}

function farmGuidePlan(seed, readyCount, emptyCount) {
  const ready = nextReadyPlot();
  if (readyCount > 0 && ready) {
    return {
      kind: "harvest",
      tone: "guide-hot",
      icon: "🧺",
      title: `${readyCount} 块灵田成熟`,
      detail: `先收 ${ready.plotNo} 号 ${ready.herbName}，避免熟田闲置`,
      actionLabel: "去收获",
    };
  }
  const empty = nextEmptyPlot();
  if (emptyCount > 0 && seed && seed.inventory > 0 && empty) {
    return {
      kind: "plant",
      tone: "guide-seed",
      icon: "🌰",
      title: `${emptyCount} 块空田可播`,
      detail: `用 ${seed.seedName} 补上 ${empty.plotNo} 号田，保持轮作`,
      actionLabel: "去播种",
    };
  }
  if (emptyCount > 0) {
    return {
      kind: "seed",
      tone: "guide-seed",
      icon: "🏪",
      title: "空田缺少灵种",
      detail: "先到种子货架补货，再回来一键播种",
      actionLabel: "买种子",
    };
  }
  const offer = firstMatchedMarketOffer();
  if (offer) {
    return {
      kind: "market",
      tone: "guide-market",
      icon: "⚖",
      title: "药铺急收可对上库存",
      detail: `${offer.herbName} 还有 ${offer.left} 株额度，可先核对回收`,
      actionLabel: "看药铺",
    };
  }
  const recipe = app.state.recipes.find((item) => item.unlocked && canAlchemy(item));
  if (recipe) {
    return {
      kind: "alchemy",
      tone: "guide-alchemy",
      icon: "🔥",
      title: "丹炉材料已齐",
      detail: `${recipe.productName} 可以开炉炼制`,
      actionLabel: "去炼丹",
    };
  }
  const next = nextMaturePlot();
  if (next) {
    return {
      kind: "wait",
      tone: "guide-calm",
      icon: "⏳",
      title: "灵田正在生长",
      detail: `${next.plotNo} 号田还需 ${formatRemaining(next.remainingSeconds)}`,
      actionLabel: "巡园",
    };
  }
  return {
    kind: "wait",
    tone: "guide-calm",
    icon: "🌿",
    title: "今日园务清爽",
    detail: "药园暂无紧急动作，可查看商店或丹方",
    actionLabel: "巡园",
  };
}

function timelinePlots() {
  if (!app.state) return [];
  const ready = app.state.plots.filter((plot) => plot.status === "ready");
  const growing = app.state.plots
    .filter((plot) => plot.status === "growing")
    .sort((a, b) => Number(a.remainingSeconds || 0) - Number(b.remainingSeconds || 0));
  const empty = app.state.plots.filter((plot) => plot.status === "empty");
  return [...ready, ...growing, ...empty].slice(0, 4);
}

function maturityBoardTitle(rows) {
  if (rows.some((plot) => plot.status === "ready")) return "有灵草已经成熟";
  if (rows.some((plot) => plot.status === "growing")) return "下一批成熟排队中";
  if (rows.some((plot) => plot.status === "empty")) return "空田等待播种";
  return "暂无灵田记录";
}

function timelinePlotTitle(plot) {
  if (plot.status === "empty") return `${plot.plotNo} 号空田`;
  return `${plot.plotNo} 号 ${escapeHtml(plot.herbName)}`;
}

function timelinePlotMeta(plot) {
  if (plot.status === "ready") return "现在可收获";
  if (plot.status === "empty") return "可安排播种";
  return `${formatRemaining(plot.remainingSeconds)} · ${formatShortTime(plot.maturesAt)}`;
}

function seedShelfModes() {
  return [
    { key: "all", label: "全部", count: app.state.seeds.length },
    { key: "stocked", label: "袋中", count: app.state.seeds.filter((seed) => seed.inventory > 0).length },
    { key: "buyable", label: "可买", count: app.state.seeds.filter((seed) => seed.purchasable && seed.leftToday > 0 && seed.price <= app.state.points).length },
  ];
}

function herbShelfModes() {
  return [
    { key: "all", label: "全部", count: app.state.herbs.length },
    { key: "stocked", label: "有货", count: app.state.herbs.filter((herb) => herb.inventory > 0).length },
  ];
}

function recipeShelfModes() {
  return [
    { key: "all", label: "全部", count: app.state.recipes.length },
    { key: "ready", label: "可炼", count: app.state.recipes.filter((recipe) => recipe.unlocked && canAlchemy(recipe)).length },
    { key: "locked", label: "未悟", count: app.state.recipes.filter((recipe) => !recipe.unlocked).length },
  ];
}

function seedModeLabel(mode) {
  if (mode === "stocked") return "袋中灵种";
  if (mode === "buyable") return "可买灵种";
  return "全部灵种";
}

function herbModeLabel(mode) {
  if (mode === "stocked") return "有货仓格";
  return "全部药草";
}

function recipeModeLabel(mode) {
  if (mode === "ready") return "可炼丹方";
  if (mode === "locked") return "未悟丹方";
  return "全部丹方";
}

function filteredSeeds() {
  if (!app.state) return [];
  if (app.seedShelfMode === "stocked") return app.state.seeds.filter((seed) => seed.inventory > 0);
  if (app.seedShelfMode === "buyable") return app.state.seeds.filter((seed) => seed.purchasable && seed.leftToday > 0 && seed.price <= app.state.points);
  return app.state.seeds;
}

function filteredHerbs() {
  if (!app.state) return [];
  if (app.herbShelfMode === "stocked") return app.state.herbs.filter((herb) => herb.inventory > 0);
  return app.state.herbs;
}

function filteredRecipes() {
  if (!app.state) return [];
  if (app.recipeShelfMode === "ready") return app.state.recipes.filter((recipe) => recipe.unlocked && canAlchemy(recipe));
  if (app.recipeShelfMode === "locked") return app.state.recipes.filter((recipe) => !recipe.unlocked);
  return app.state.recipes;
}

function seedShopGuide(seed) {
  const emptyCount = emptyPlotCount();
  const stocked = app.state.seeds.find((item) => item.inventory > 0);
  if (emptyCount > 0 && stocked) {
    return {
      kind: "guide-plant",
      icon: "🌰",
      title: "袋中已有可播灵种",
      detail: `${stocked.seedName} x${stocked.inventory}，可先回灵田补上 ${emptyCount} 块空田`,
      action: "use-seed",
      seedKey: stocked.key,
      label: "去播种",
    };
  }
  const buyable = app.state.seeds.find((item) => item.purchasable && item.leftToday > 0 && item.price <= app.state.points);
  if (buyable) {
    return {
      kind: "guide-buy",
      icon: "🏪",
      title: "今日还有可买灵种",
      detail: `${buyable.seedName} ${buyable.price} 积分，限购剩 ${buyable.leftToday}`,
      action: "select-seed",
      seedKey: buyable.key,
      label: "看货架",
    };
  }
  if (seed && seed.price > app.state.points) {
    return {
      kind: "guide-wait",
      icon: "💰",
      title: "当前积分不足",
      detail: `${seed.seedName} 还差 ${seed.price - app.state.points} 积分`,
      action: "set-seed-mode",
      label: "看全部",
    };
  }
  return {
    kind: "guide-wait",
    icon: "🧺",
    title: "今日货架已巡完",
    detail: "可回灵田查看成长，或去药铺核对库存",
    action: "open-market",
    label: "去药铺",
  };
}

function herbWarehouseGuide(herb) {
  const matchedOffer = firstMatchedMarketOffer();
  if (matchedOffer) {
    const matchedHerb = app.state.herbs.find((item) => item.key === matchedOffer.seedKey);
    const qty = matchedHerb ? Math.min(matchedHerb.inventory, matchedOffer.left) : 0;
    return {
      kind: "guide-market",
      icon: "⚖",
      title: "急收行情匹配库存",
      detail: `${matchedOffer.herbName} 可按急收优先处理 ${qty} 株`,
      action: "select-herb",
      seedKey: matchedOffer.seedKey,
      label: "看柜台",
    };
  }
  const stocked = app.state.herbs.find((item) => item.inventory > 0);
  if (stocked) {
    return {
      kind: "guide-stock",
      icon: "🧺",
      title: "仓库里还有可处理灵草",
      detail: `${stocked.herbName} 库存 ${stocked.inventory} 株，可回收或留作炼丹`,
      action: "select-herb",
      seedKey: stocked.key,
      label: "看仓格",
    };
  }
  const missingRecipe = app.state.recipes.find((recipe) => recipe.unlocked && recipe.materials.some((mat) => !mat.enough));
  if (missingRecipe) {
    return {
      kind: "guide-recipe",
      icon: "🔥",
      title: "丹炉缺少材料",
      detail: `${missingRecipe.productName} 还需补齐草药`,
      action: "open-recipes",
      label: "看丹方",
    };
  }
  return {
    kind: "guide-empty",
    icon: "🌿",
    title: "仓库暂时清爽",
    detail: "回灵田播种收获后，灵草会进入这里",
    action: "open-seeds",
    label: "去备种",
  };
}

function alchemyGuide(recipe) {
  const ready = app.state.recipes.find((item) => item.unlocked && canAlchemy(item));
  if (ready) {
    return {
      kind: "guide-ready",
      icon: "🔥",
      title: "炉火可开，材料已齐",
      detail: `${ready.productName} 可以炼制，先核对炉火费和库存`,
      action: "select-recipe",
      recipeKey: ready.key,
      label: "看丹方",
    };
  }
  const missing = app.state.recipes.find((item) => item.unlocked && item.materials.some((mat) => !mat.enough));
  if (missing) {
    const mat = missing.materials.find((item) => !item.enough);
    return {
      kind: "guide-missing",
      icon: "🧺",
      title: "丹方缺少材料",
      detail: mat ? `${missing.productName} 缺 ${mat.itemName} x${Math.max(0, mat.need - mat.owned)}` : `${missing.productName} 材料未齐`,
      action: "open-herbs",
      label: "寻药草",
    };
  }
  const locked = app.state.recipes.find((item) => !item.unlocked);
  if (locked) {
    return {
      kind: "guide-locked",
      icon: "📜",
      title: "还有丹方未参悟",
      detail: `${locked.name} 需要 ${locked.unlockPrice} 积分参悟`,
      action: "select-recipe",
      recipeKey: locked.key,
      label: "看卷轴",
    };
  }
  return {
    kind: "guide-calm",
    icon: "丹",
    title: "丹炉暂时待命",
    detail: recipe ? "可回灵田收草，或去仓库核对材料" : "暂无可处理丹方",
    action: "open-herbs",
    label: "看仓库",
  };
}

function materialPercent(mat) {
  if (!mat || mat.need <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((mat.owned / mat.need) * 100)));
}

function nextMaturePlot() {
  if (!app.state) return null;
  return app.state.plots
    .filter((plot) => plot.status === "growing")
    .sort((a, b) => Number(a.remainingSeconds || 0) - Number(b.remainingSeconds || 0))[0] || null;
}

function recentPlotActionKind(plotNo, seedKey) {
  if (!app.lastAction || Date.now() - app.lastAction.at > 1400) return false;
  if (app.lastAction.plotNo && app.lastAction.plotNo === plotNo) return app.lastAction.kind;
  if (app.lastAction.seedKey && app.lastAction.seedKey === seedKey && app.lastAction.kind === "seed") return "seed";
  return false;
}

function buildBatchAction(path, body) {
  if (!app.state) return null;
  const now = Date.now();
  if (path === "/api/garden/harvest-all") {
    const plotNos = app.state.plots.filter((plot) => plot.status === "ready").map((plot) => plot.plotNo);
    return plotNos.length > 0 ? { kind: "harvest", plotNos, at: now } : null;
  }
  if (path === "/api/garden/plant-all") {
    const seed = app.state.seeds.find((item) => item.key === (body && body.seedKey));
    const limit = seed ? Math.max(0, Number(seed.inventory || 0)) : 0;
    const plotNos = app.state.plots
      .filter((plot) => plot.status === "empty")
      .slice(0, limit)
      .map((plot) => plot.plotNo);
    return plotNos.length > 0 ? { kind: "plant", plotNos, at: now } : null;
  }
  return null;
}

function activeBatchPlotKind(plotNo) {
  if (!app.batchAction || Date.now() - app.batchAction.at > 2200) return false;
  return app.batchAction.plotNos.includes(plotNo) ? app.batchAction.kind : false;
}

function batchPlotDelay(plotNo) {
  if (!app.batchAction) return 0;
  const index = app.batchAction.plotNos.indexOf(plotNo);
  return Math.max(0, index) * 95;
}

function cropIcon(plot, seed) {
  if (!plot || plot.status === "empty") return `<span class="crop-logo crop-empty"></span>`;
  if (plot.status === "ready") {
    return `<span class="ready-crop-logo">${itemLogo("herb", plot.seedKey || plot.herbName, plot.herbName || (seed && seed.herbName) || "")}<span class="harvest-crown"></span></span>`;
  }
  const stage = cropStage(plot);
  return `<span class="growing-crop-logo stage-mark-${stage}">${itemLogo("herb", plot.seedKey || plot.herbName, plot.herbName || (seed && seed.herbName) || "")}<span class="growth-glint"></span></span>`;
}

function cropStage(plot) {
  if (!plot || plot.status === "empty") return 0;
  if (plot.status === "ready") return 4;
  const progress = progressValue(plot);
  if (progress < 35) return 1;
  if (progress < 75) return 2;
  return 3;
}

function progressValue(plot) {
  const seed = app.state.seeds.find((item) => item.key === plot.seedKey);
  if (!seed || seed.growSeconds <= 0) return 0;
  const done = seed.growSeconds - plot.remainingSeconds;
  return Math.max(0, Math.min(100, Math.round((done / seed.growSeconds) * 100)));
}

function formatRemaining(seconds) {
  const value = Math.max(0, Number(seconds || 0));
  const hours = Math.floor(value / 3600);
  const minutes = Math.ceil((value % 3600) / 60);
  if (hours > 0) return `${hours}小时${minutes}分`;
  return `${minutes}分`;
}

function formatShortTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  const hours = String(date.getHours()).padStart(2, "0");
  const minutes = String(date.getMinutes()).padStart(2, "0");
  return `${hours}:${minutes}`;
}

function canAlchemy(recipe) {
  return recipe.unlocked && recipe.materials.every((mat) => mat.enough);
}

function seedIcon(seed) {
  return itemLogo("seed", seed.key, seed.seedName || seed.herbName || "");
}

function herbIcon(herb) {
  return itemLogo("herb", herb.key, herb.herbName || "");
}

function pillIcon(recipe) {
  return itemLogo("pill", recipe.key, recipe.productName || recipe.name || "丹");
}

function recipeIcon(recipe) {
  const key = String((recipe && recipe.key) || "juling");
  const palette = gardenPillPalette(key);
  return recipeDiagramSVG(key, palette);
}

function furnaceMarkIcon(recipe) {
  const key = String((recipe && recipe.key) || "idle");
  const palette = recipe ? gardenPillPalette(key) : ["#8eb6a0", "#d7e9bd", "#e7cc75", "#31584a"];
  return furnaceMarkSVG(key, palette);
}

function alchemyIdleIcon() {
  return `
    <svg class="alchemy-idle-icon" viewBox="0 0 64 64" aria-hidden="true">
      <circle cx="32" cy="32" r="20" fill="none" stroke="#b8d8bf" stroke-width="2" stroke-dasharray="4 5"/>
      <path d="M32 13v10M32 41v10M13 32h10M41 32h10" stroke="#e2cd7b" stroke-width="3" stroke-linecap="round"/>
      <path d="M23 32c4-8 14-8 18 0-4 8-14 8-18 0Z" fill="#789d84" stroke="#d9e8bd" stroke-width="2"/>
      <circle cx="32" cy="32" r="4" fill="#f0d27b"/>
    </svg>
  `;
}

function uiIcon(name) {
  const icons = {
    field: `<svg class="ui-icon ui-icon-field" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16v10.5A2.5 2.5 0 0 1 17.5 20h-11A2.5 2.5 0 0 1 4 17.5V7Z" fill="#9d6c3d"/><path d="M4 8.5h16M5.5 12h13M6 15.5h12" stroke="#6f4726" stroke-width="1.4" stroke-linecap="round"/><path d="M7 6c2.3-2.5 7.5-2.5 10 0" stroke="#2f7d4d" stroke-width="2" stroke-linecap="round"/><path d="M12 5.8V3.5" stroke="#2f7d4d" stroke-width="2" stroke-linecap="round"/></svg>`,
    seed: `<svg class="ui-icon ui-icon-seed" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 9h12l1.6 8.1A3 3 0 0 1 16.7 21H7.3a3 3 0 0 1-2.9-3.9L6 9Z" fill="#8b5a32"/><path d="M6.2 9c1.1-4 3.1-6 5.8-6s4.7 2 5.8 6c-2.8 1.3-8.8 1.3-11.6 0Z" fill="#f0c45a"/><path d="M12 11c2.4 2.2 2.4 5.8 0 8-2.4-2.2-2.4-5.8 0-8Z" fill="#2f7d4d"/><path d="M8.5 15.2c2-1.7 5-1.7 7 0-2 1.7-5 1.7-7 0Z" fill="#7fb84f"/></svg>`,
    herb: `<svg class="ui-icon ui-icon-herb" viewBox="0 0 24 24" aria-hidden="true"><path d="M12 20V9" stroke="#7a4d2b" stroke-width="2" stroke-linecap="round"/><path d="M12 12C6.5 11.7 4.5 8.4 5.2 4c4.5.2 7 2.8 6.8 8Z" fill="#5aa05b"/><path d="M12 13c5.5-.3 7.5-3.6 6.8-8-4.5.2-7 2.8-6.8 8Z" fill="#2f7d4d"/><path d="M12 18c-3.5-.3-5.2-2-5.7-4.6 3.1-.2 5 1.2 5.7 4.6Z" fill="#8fc35a"/></svg>`,
    recipe: `<svg class="ui-icon ui-icon-recipe" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 3h10.8A2.2 2.2 0 0 1 19 5.2V21l-3-1.4-3 1.4-3-1.4L7 21V6.2A3.2 3.2 0 0 0 6 3Z" fill="#f1d39a"/><path d="M6 3a3 3 0 0 0 0 6h11" fill="none" stroke="#9a6335" stroke-width="1.6" stroke-linecap="round"/><path d="M10 10h5M10 14h4" stroke="#7b4b2a" stroke-width="1.5" stroke-linecap="round"/><path d="M8.5 4.5h7" stroke="#fff2c8" stroke-width="1.4" stroke-linecap="round"/></svg>`,
    harvest: `<svg class="ui-icon ui-icon-harvest" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 10h12l-1.3 8.2A3 3 0 0 1 13.8 21H10a3 3 0 0 1-2.9-2.8L6 10Z" fill="#9a6335"/><path d="M8 10c.4-4 2-6 4-6s3.6 2 4 6" fill="none" stroke="#76512b" stroke-width="2" stroke-linecap="round"/><path d="M8 13h8" stroke="#f0c45a" stroke-width="1.6" stroke-linecap="round"/><path d="M11 8c-.2-3 1.6-5 4.8-5 .1 3.2-1.6 5.1-4.8 5Z" fill="#2f7d4d"/></svg>`,
    hand: `<svg class="ui-icon ui-icon-hand" viewBox="0 0 24 24" aria-hidden="true"><path d="M8.2 12.2V5.7a1.4 1.4 0 0 1 2.8 0v5.1-6.2a1.4 1.4 0 0 1 2.8 0v6.2-4.9a1.4 1.4 0 0 1 2.8 0v7.4l.9-1.4a1.5 1.5 0 0 1 2.5 1.6l-2.6 4.2A5 5 0 0 1 13.1 20h-1.4a5 5 0 0 1-4.8-3.6L6 13.2a1.5 1.5 0 0 1 2.2-1Z" fill="#f0c58f" stroke="#9a6335" stroke-width="1.3" stroke-linejoin="round"/></svg>`,
    market: `<svg class="ui-icon ui-icon-market" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 9h16l-1.4 10H5.4L4 9Z" fill="#c69a61"/><path d="M6 9c.8-3.6 2.8-5.5 6-5.5S17.2 5.4 18 9" fill="none" stroke="#73512f" stroke-width="2" stroke-linecap="round"/><path d="M8 13h8M9 16h6" stroke="#fff0bf" stroke-width="1.4" stroke-linecap="round"/><path d="M10 6h4" stroke="#2f7d4d" stroke-width="2" stroke-linecap="round"/></svg>`,
    shop: `<svg class="ui-icon ui-icon-shop" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 10h14v10H5V10Z" fill="#e7c787"/><path d="M4 7h16l-1.5 4h-13L4 7Z" fill="#b45a4a"/><path d="M7 7l.8 4M12 7v4M17 7l-.8 4" stroke="#fff1c5" stroke-width="1.3" stroke-linecap="round"/><path d="M8 15h8M9 18h6" stroke="#7a4d2b" stroke-width="1.5" stroke-linecap="round"/></svg>`,
    clock: `<svg class="ui-icon ui-icon-clock" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="8" fill="#eef6f8" stroke="#327f82" stroke-width="2"/><path d="M12 7v5l3.5 2" stroke="#327f82" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M6 5.5 4.8 4.3M18 5.5l1.2-1.2" stroke="#b5852c" stroke-width="1.8" stroke-linecap="round"/></svg>`,
  };
  return icons[name] || `<span class="ui-icon ui-icon-text" aria-hidden="true">•</span>`;
}

function itemLogo(type, key, name) {
	const itemKey = String(key || "").replace(/^mock_/, "");
	const variant = logoVariant(itemKey || name);
	const category = type === "herb" ? herbCategory(name) : "";
	const palette = gardenItemPalette(itemKey, variant);
	if (type === "seed") return seedLogoSVG(variant, palette, itemKey);
	if (type === "pill") return pillLogoSVG(itemKey, gardenPillPalette(itemKey));
	return herbLogoSVG(category, variant, palette, itemKey);
}

function logoVariant(key) {
	return (Math.abs(hashText(key)) % 6) + 1;
}

function logoPalette(variant) {
	const palettes = [
		["#2f7d4d", "#8fc35a", "#f3d27e", "#9a6435"],
		["#327f82", "#82c6ba", "#f0ca6e", "#8a6637"],
		["#7b5ca8", "#b895d6", "#f4cf86", "#734d31"],
		["#b45a4a", "#e08b6d", "#f5d587", "#77452d"],
		["#557b38", "#9abd63", "#e9c96b", "#725633"],
		["#2f6d92", "#7bb9d4", "#f0d17a", "#6a5735"],
	];
	return palettes[(variant - 1) % palettes.length];
}

function gardenItemPalette(key, variant) {
	const palettes = {
		ninglu: ["#2f8c82", "#8bd8bd", "#d9f4d5", "#76543b"],
		qingling: ["#34754b", "#83b958", "#e4dc78", "#75513a"],
		chiyang: ["#b74435", "#ed7c42", "#f4c64d", "#74422e"],
		yuehua: ["#536ea7", "#91a9dc", "#dce7f7", "#6c503f"],
		xuanshen: ["#6f6236", "#a69a54", "#d6b76b", "#6d482e"],
		ziyuzhi: ["#744e9e", "#b27bc1", "#e6c991", "#68432f"],
		longxue: ["#982f35", "#d85a45", "#efbd55", "#67432d"],
		tianxin: ["#4f7c9b", "#a6d4dc", "#f0e6bc", "#6a503a"],
	};
	return palettes[key] || logoPalette(variant);
}

function seedLogoSVG(variant, palette, key) {
	const [primary, accent, gold, earth] = palette;
	const emblem = gardenSeedEmblemSVG(key, primary, accent, gold);
	return `
		<svg class="item-logo seed-logo logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">
			<path class="svg-shadow" d="M17 58h30c6 0 10-4 9-10L51 22H13L8 48c-1 6 3 10 9 10Z" fill="rgba(60,38,20,.18)"/>
			<path d="M16 56h32c5 0 8-4 7-9l-5-27H14L9 47c-1 5 2 9 7 9Z" fill="${earth}"/>
			<path d="M14 20c3-7 9-11 18-11s15 4 18 11c-8 4-28 4-36 0Z" fill="${gold}"/>
			<path d="M18 24h28l3 20c1 4-2 7-6 7H21c-4 0-7-3-6-7l3-20Z" fill="${earth}" opacity=".9"/>
			<path d="M21 28h22" stroke="#f8e3a6" stroke-width="3" stroke-linecap="round" opacity=".75"/>
			<circle cx="32" cy="39" r="11" fill="#f7edc5" opacity=".92"/>
			${emblem}
		</svg>
	`;
}

function gardenSeedEmblemSVG(key, primary, accent, gold) {
	if (key === "chiyang") return `<g transform="translate(32 39)"><circle r="4" fill="${gold}"/><path d="M0-9c5 2 6 6 0 9-6-3-5-7 0-9ZM9 0c-2 5-6 6-9 0 3-6 7-5 9 0ZM0 9c-5-2-6-6 0-9 6 3 5 7 0 9ZM-9 0c2-5 6-6 9 0-3 6-7 5-9 0Z" fill="${accent}"/></g>`;
	if (key === "ziyuzhi") return `<path d="M23 40c2-8 8-12 17-9 5 2 7 5 7 9-6 5-17 5-24 0Z" fill="${primary}"/><path d="M30 40h9l2 9H28l2-9Z" fill="${gold}"/>`;
	if (key === "longxue") return `<path d="M27 33c6-4 12 0 10 6 6-2 10 4 6 9-5 5-15 3-18-4-2-5-1-8 2-11Z" fill="${primary}"/><path d="M31 32c3-4 7-5 11-3" fill="none" stroke="${gold}" stroke-width="3" stroke-linecap="round"/>`;
	if (key === "tianxin") return `<g transform="translate(32 40)" fill="${accent}"><path d="M0-10c7 6 7 13 0 19-7-6-7-13 0-19Z"/><path d="M-11-4c8 0 13 5 12 13-8 0-13-5-12-13Z"/><path d="M11-4c-8 0-13 5-12 13 8 0 13-5 12-13Z"/></g><circle cx="32" cy="40" r="4" fill="${gold}"/>`;
	return `<path d="M32 29c7 7 7 15 0 21-7-6-7-14 0-21Z" fill="${primary}"/><path d="M23 40c5-5 13-5 18 0-5 5-13 5-18 0Z" fill="${accent}"/>`;
}

function herbLogoSVG(category, variant, palette, key) {
	const illustrated = gardenHerbIllustrationSVG(key, palette, variant);
	if (illustrated) return illustrated;
	const [primary, accent, gold, earth] = palette;
	if (category === "herb-root") {
		return `
			<svg class="item-logo herb-logo herb-root logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">
				<path d="M15 55c13 4 30 4 41 0" stroke="rgba(75,53,27,.16)" stroke-width="7" stroke-linecap="round"/>
				<path d="M33 12c9 8 10 27 1 42-10-13-12-32-1-42Z" fill="${gold}"/>
				<path d="M30 24c-5 5-9 9-16 8M37 30c5 3 8 8 14 8M31 42c-4 2-7 6-13 7" stroke="${earth}" stroke-width="4" stroke-linecap="round" opacity=".72"/>
				<path d="M22 17c7-7 16-7 24 0M26 14c-3-5-7-6-12-5M41 15c3-5 8-7 13-5" stroke="${primary}" stroke-width="5" stroke-linecap="round"/>
			</svg>
		`;
	}
	if (category === "herb-fungus") {
		return `
			<svg class="item-logo herb-logo herb-fungus logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">
				<path d="M14 55c12 4 27 4 38 0" stroke="rgba(75,53,27,.16)" stroke-width="7" stroke-linecap="round"/>
				<path d="M24 32h16l5 23H19l5-23Z" fill="#ead7a5"/>
				<path d="M11 31c3-15 14-22 30-18 9 2 15 9 16 18-11 9-34 9-46 0Z" fill="${primary}"/>
				<path d="M18 28c9 5 25 5 34 0" stroke="#f4dca4" stroke-width="5" stroke-linecap="round" opacity=".65"/>
				<circle cx="27" cy="20" r="4" fill="#f7e7bd"/><circle cx="41" cy="23" r="3" fill="#f7e7bd"/>
			</svg>
		`;
	}
	if (category === "herb-flower") {
		return `
			<svg class="item-logo herb-logo herb-flower logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">
				<path d="M32 33v24" stroke="${primary}" stroke-width="5" stroke-linecap="round"/>
				<path d="M31 45c-7-8-15-8-21-3 8 7 16 7 21 3ZM34 42c8-8 15-8 21-3-7 7-15 8-21 3Z" fill="${accent}"/>
				<g fill="${gold}">
					<path d="M32 12c8 6 8 14 0 20-8-6-8-14 0-20Z"/>
					<path d="M18 24c9-3 16 1 17 10-9 3-16-1-17-10Z"/>
					<path d="M46 24c-9-3-16 1-17 10 9 3 16-1 17-10Z"/>
				</g>
				<circle cx="32" cy="31" r="7" fill="${primary}"/>
			</svg>
		`;
	}
	return `
		<svg class="item-logo herb-logo herb-leaf logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">
			<path d="M12 55c14 4 31 4 43 0" stroke="rgba(75,53,27,.16)" stroke-width="7" stroke-linecap="round"/>
			<path d="M31 50c-1-17 5-30 21-39 3 20-4 33-21 39Z" fill="${primary}"/>
			<path d="M30 50C18 39 15 27 22 13c15 9 19 21 8 37Z" fill="${accent}"/>
			<path d="M31 50c3-13 9-23 18-32M30 50c-3-12-5-22-7-31" stroke="#e4f2c9" stroke-width="3" stroke-linecap="round" opacity=".7"/>
			<path d="M31 49v9" stroke="${earth}" stroke-width="5" stroke-linecap="round"/>
		</svg>
	`;
}

function gardenHerbIllustrationSVG(key, palette, variant) {
	const [primary, accent, gold, earth] = palette;
	const shadow = `<ellipse cx="32" cy="55" rx="22" ry="5" fill="rgba(53,38,24,.18)"/>`;
	if (key === "ninglu") {
		return `<svg class="item-logo herb-logo herb-ninglu logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M31 54C25 38 24 25 29 10c6 17 6 31 2 44ZM32 52c4-17 11-29 22-38-1 18-8 31-22 38ZM29 51C17 41 12 30 13 17c13 8 19 19 16 34Z" fill="${primary}"/><path d="M29 47c-8-9-11-18-12-26M33 47c5-13 11-22 18-29" fill="none" stroke="${accent}" stroke-width="3" stroke-linecap="round"/><circle cx="18" cy="21" r="5" fill="#bfeef0" stroke="#f4ffff" stroke-width="2"/><circle cx="48" cy="20" r="4" fill="#bfeef0" stroke="#f4ffff" stroke-width="2"/></svg>`;
	}
	if (key === "qingling") {
		return `<svg class="item-logo herb-logo herb-qingling logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M32 54V20" stroke="${earth}" stroke-width="5" stroke-linecap="round"/><path d="M31 25C16 26 9 18 11 8c13-1 21 5 20 17ZM33 31c14 0 21-7 20-18-13 0-21 6-20 18ZM31 40c-12 1-18-5-19-14 11-1 18 4 19 14ZM33 46c11 0 17-5 18-14-11-1-17 4-18 14Z" fill="${primary}"/><path d="M15 13c5 3 9 6 14 10M49 18c-5 3-9 6-14 10" fill="none" stroke="${accent}" stroke-width="3" stroke-linecap="round"/></svg>`;
	}
	if (key === "chiyang") {
		return `<svg class="item-logo herb-logo herb-chiyang logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M32 56V33" stroke="#55733d" stroke-width="5" stroke-linecap="round"/><path d="M31 45c-8-7-15-6-20 0 7 6 14 7 20 0ZM34 42c8-7 15-6 20 0-7 6-14 7-20 0Z" fill="#6a9d4b"/><g transform="translate(32 25)"><path d="M0-19c9 5 11 13 0 19-11-6-9-14 0-19ZM19 0c-5 9-13 11-19 0 6-11 14-9 19 0ZM0 19c-9-5-11-13 0-19 11 6 9 14 0 19ZM-19 0c5-9 13-11 19 0-6 11-14 9-19 0Z" fill="${accent}"/><path d="M13-13c3 9-2 15-13 13-2-11 4-16 13-13ZM13 13c-9 3-15-2-13-13 11-2 16 4 13 13ZM-13 13c-3-9 2-15 13-13 2 11-4 16-13 13ZM-13-13c9-3 15 2 13 13-11 2-16-4-13-13Z" fill="${primary}"/><circle r="8" fill="${gold}"/></g></svg>`;
	}
	if (key === "yuehua") {
		return `<svg class="item-logo herb-logo herb-yuehua logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M29 56c14-12 17-25 8-39-6-9-3-14 5-18" fill="none" stroke="${primary}" stroke-width="5" stroke-linecap="round"/><path d="M33 47c-12-1-18-7-17-16 11 0 18 5 17 16ZM39 32c10-2 15-8 13-16-10 1-15 7-13 16ZM31 20c-8-2-12-7-10-14 8 1 12 6 10 14Z" fill="${accent}"/><path d="M45 5a14 14 0 1 0 11 23A17 17 0 1 1 45 5Z" fill="${gold}" opacity=".9"/></svg>`;
	}
	if (key === "xuanshen") {
		return `<svg class="item-logo herb-logo herb-xuanshen logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M31 17c10 10 12 25 4 39-4-7-6-13-7-19-3 7-8 12-15 15 5-10 8-20 7-30l11-5Z" fill="${gold}" stroke="${earth}" stroke-width="2"/><path d="M34 32c7 4 11 9 15 17M25 37c-5 4-8 9-12 14" fill="none" stroke="${earth}" stroke-width="4" stroke-linecap="round"/><path d="M31 18C22 9 15 9 9 15c7 7 14 8 22 3ZM32 18c8-10 16-11 23-5-6 8-14 10-23 5Z" fill="${primary}"/></svg>`;
	}
	if (key === "ziyuzhi") {
		return `<svg class="item-logo herb-logo herb-ziyuzhi logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M23 31h18l6 24H17l6-24Z" fill="#e7cf9e" stroke="${earth}" stroke-width="2"/><path d="M7 31C10 13 23 5 42 10c10 3 16 10 16 21-13 10-37 10-51 0Z" fill="${primary}"/><path d="M14 27c11 6 28 6 38 0" fill="none" stroke="${accent}" stroke-width="7" stroke-linecap="round"/><path d="M18 20c9-6 23-6 32 0" fill="none" stroke="#ead9f3" stroke-width="3" stroke-linecap="round" opacity=".7"/></svg>`;
	}
	if (key === "longxue") {
		return `<svg class="item-logo herb-logo herb-longxue logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M31 18c-3-9 2-15 11-16 1 9-3 15-11 16Z" fill="#3d7b45"/><path d="M33 20c11-7 21-2 20 9 8 2 11 10 6 17-7 11-27 13-39 4-10-7-10-20 0-26 4-3 9-4 13-4Z" fill="${primary}" stroke="#6f2630" stroke-width="2"/><path d="M27 24c8 4 16 4 24 0" fill="none" stroke="${accent}" stroke-width="4" stroke-linecap="round"/><circle cx="26" cy="36" r="4" fill="#f5bd66" opacity=".7"/></svg>`;
	}
	if (key === "tianxin") {
		return `<svg class="item-logo herb-logo herb-tianxin logo-${variant}" viewBox="0 0 64 64" aria-hidden="true">${shadow}<path d="M32 54V31" stroke="#4f8269" stroke-width="5" stroke-linecap="round"/><g transform="translate(32 31)"><path d="M0-24c10 8 11 18 0 28-11-10-10-20 0-28Z" fill="#eef6f4"/><path d="M-22-12c13 1 21 9 19 22-13-1-20-9-19-22ZM22-12C9-11 1-3 3 10c13-1 20-9 19-22Z" fill="${accent}"/><path d="M-17 7C-8 2-1 5 0 16-9 4-16 3-17 7ZM17 7C8 2 1 5 0 16c9 4 16 3 17-9Z" fill="${primary}"/><circle cy="3" r="7" fill="${gold}"/></g></svg>`;
	}
	return "";
}

function gardenPillPalette(key) {
	const palettes = {
		juling: ["#28785c", "#76c68c", "#e7ce72", "#153f34"],
		zhuji: ["#a84b31", "#e58b4a", "#f0cc68", "#64321f"],
		jiangchen: ["#5475a7", "#9fb8de", "#e8d99a", "#364869"],
		jiuzhuan: ["#594989", "#b087c6", "#edc968", "#382e5d"],
		jiuqu: ["#8a5b2f", "#c7954f", "#e8cb77", "#54361f"],
		butian: ["#3c8294", "#8ec7cc", "#efd47a", "#285564"],
	};
	return palettes[key] || ["#447a64", "#91b78c", "#e4c875", "#315344"];
}

function pillLogoSVG(key, palette) {
	const [primary, accent, gold, dark] = palette;
	return `
		<svg class="item-logo pill-logo pill-${escapeAttr(key)}" viewBox="0 0 64 64" aria-hidden="true">
			<ellipse cx="32" cy="55" rx="19" ry="5" fill="rgba(49,35,25,.18)"/>
			<circle cx="32" cy="30" r="24" fill="${dark}" opacity=".26"/>
			<circle cx="32" cy="28" r="22" fill="${primary}" stroke="${gold}" stroke-width="3"/>
			<circle cx="32" cy="28" r="17" fill="${accent}" opacity=".34"/>
			<path d="M18 22c5-10 18-14 28-7-10-2-18 1-24 9-3 4-5 4-4-2Z" fill="#fff7d1" opacity=".62"/>
			${pillMotifSVG(key, palette)}
			<circle cx="32" cy="28" r="21" fill="none" stroke="#fff3bd" stroke-width="1" opacity=".32"/>
		</svg>
	`;
}

function pillMotifSVG(key, palette) {
	const [primary, accent, gold, dark] = palette;
	if (key === "juling") {
		return `<path d="M20 32c2-10 11-15 20-11 6 3 8 10 3 15-5 5-14 4-16-2-2-5 3-9 8-7" fill="none" stroke="#e5f5b4" stroke-width="3" stroke-linecap="round"/><path d="M29 36c-5-6-4-12 2-17 6 7 6 13-2 17Z" fill="${gold}"/><circle cx="43" cy="24" r="3" fill="#f6e89d"/>`;
	}
	if (key === "zhuji") {
		return `<path d="M19 38 32 16l13 22Z" fill="${dark}" opacity=".74"/><path d="m24 34 8-12 8 12Z" fill="${gold}"/><path d="M19 39h26M23 44h18" stroke="#fff0ad" stroke-width="3" stroke-linecap="round"/><circle cx="32" cy="27" r="3" fill="${accent}"/>`;
	}
	if (key === "jiangchen") {
		return `<path d="M20 18c8 2 14 7 17 15-7-5-14-5-21-1 2-6 3-10 4-14Z" fill="#eaf4ff" opacity=".82"/><path d="M43 17 39 38M34 23l-3 18M25 31l-2 12" stroke="${gold}" stroke-width="2.5" stroke-linecap="round"/><circle cx="42" cy="43" r="3" fill="#f8e9ae"/><circle cx="30" cy="46" r="2" fill="#f8e9ae"/>`;
	}
	if (key === "jiuzhuan") {
		return `<circle cx="32" cy="28" r="10" fill="none" stroke="${gold}" stroke-width="3"/><path d="M19 20c9-8 23-6 29 4M46 37c-8 9-22 10-29 1" fill="none" stroke="#f3e6ab" stroke-width="2.5" stroke-linecap="round"/><g fill="${gold}"><circle cx="20" cy="19" r="2"/><circle cx="28" cy="14" r="2"/><circle cx="38" cy="14" r="2"/><circle cx="46" cy="20" r="2"/><circle cx="49" cy="29" r="2"/><circle cx="45" cy="39" r="2"/><circle cx="36" cy="44" r="2"/><circle cx="26" cy="43" r="2"/><circle cx="18" cy="36" r="2"/></g><path d="M27 28c3-5 8-5 11 0-3 5-8 5-11 0Z" fill="${dark}"/>`;
	}
	if (key === "jiuqu") {
		return `<path d="M31 15c7 7 7 14 2 20-4 4-4 8 1 13" fill="none" stroke="#f1ddb0" stroke-width="5" stroke-linecap="round"/><path d="M29 25c-5 3-8 7-9 13M35 31c5 2 8 6 10 11M30 42c-4 1-7 4-9 7" fill="none" stroke="${gold}" stroke-width="3" stroke-linecap="round"/><path d="M31 17c-6-5-11-4-15 0 5 5 10 5 15 0ZM33 17c6-5 11-4 15 0-5 5-10 5-15 0Z" fill="${accent}"/>`;
	}
	if (key === "butian") {
		return `<path d="M20 19c8-5 18-4 25 3-8 1-13 5-15 12-2-6-5-11-10-15Z" fill="#dff7ef" opacity=".82"/><path d="m32 8-3 13 6 5-7 8 5 5-4 10M31 22l-9 4M29 35l-9 6M34 27l10-5M33 40l10 4" fill="none" stroke="${gold}" stroke-width="2.5" stroke-linecap="round"/><circle cx="21" cy="42" r="3" fill="#eef8dd"/>`;
	}
	return `<path d="M22 28c5-8 15-8 20 0-5 8-15 8-20 0Z" fill="${gold}"/><circle cx="32" cy="28" r="4" fill="${dark}"/>`;
}

function recipeDiagramSVG(key, palette) {
	const [primary, accent, gold, dark] = palette;
	return `
		<svg class="recipe-icon recipe-${escapeAttr(key)}" viewBox="0 0 72 72" aria-hidden="true">
			<path d="M12 11h48v50H12Z" fill="#f4dfaa" stroke="#a77a40" stroke-width="2"/>
			<path d="M9 12c0-4 3-7 7-7h40c4 0 7 3 7 7-9 3-45 3-54 0ZM9 60c9-3 45-3 54 0 0 4-3 7-7 7H16c-4 0-7-3-7-7Z" fill="#d7ae68" stroke="#8d6335" stroke-width="2"/>
			<circle cx="36" cy="35" r="17" fill="${primary}" opacity=".12" stroke="${primary}" stroke-width="2" stroke-dasharray="3 3"/>
			<path d="M27 35h18l-2 11H29l-2-11Z" fill="${dark}"/><path d="M29 34c1-5 4-7 7-7s6 2 7 7" fill="none" stroke="${gold}" stroke-width="2.5" stroke-linecap="round"/>
			${recipeMotifSVG(key, palette)}
			<path d="M18 53h36" stroke="#b58d52" stroke-width="2" stroke-linecap="round" opacity=".55"/>
		</svg>
	`;
}

function recipeMotifSVG(key, palette) {
	const [primary, accent, gold, dark] = palette;
	const positions = key === "jiuzhuan" || key === "butian"
		? [[19, 25], [53, 25], [19, 47], [53, 47]]
		: [[20, 27], [52, 27], [36, 18]];
	const nodes = positions.map(([x, y], index) => `<circle cx="${x}" cy="${y}" r="4" fill="${index % 2 === 0 ? accent : gold}" stroke="#fff1bd" stroke-width="1.5"/>`).join("");
	const links = positions.map(([x, y]) => `<path d="M${x} ${y} 31 35" stroke="${primary}" stroke-width="1.5" stroke-dasharray="2 2" opacity=".72"/>`).join("");
	let core = `<path d="M33 39c2-4 5-4 7 0-2 4-5 4-7 0Z" fill="${gold}"/>`;
	if (key === "juling") core = `<path d="M35 43c-5-6-4-12 2-16 5 6 5 11-2 16Z" fill="${gold}"/><path d="M28 37c4-4 10-4 14 0-4 4-10 4-14 0Z" fill="${accent}"/>`;
	if (key === "zhuji") core = `<path d="m28 43 8-15 8 15Z" fill="${dark}"/><path d="m32 40 4-7 4 7Z" fill="${gold}"/>`;
	if (key === "jiangchen") core = `<path d="M30 29c7 2 11 6 13 12-6-3-11-3-16 0 1-5 2-8 3-12Z" fill="${accent}"/><path d="m43 29-3 15M34 34l-2 11" stroke="${gold}" stroke-width="2" stroke-linecap="round"/>`;
	if (key === "jiuzhuan") core = `<circle cx="36" cy="37" r="8" fill="none" stroke="${gold}" stroke-width="2"/><g fill="${dark}"><circle cx="36" cy="28" r="1.7"/><circle cx="43" cy="31" r="1.7"/><circle cx="45" cy="38" r="1.7"/><circle cx="41" cy="45" r="1.7"/><circle cx="33" cy="46" r="1.7"/><circle cx="27" cy="41" r="1.7"/><circle cx="28" cy="33" r="1.7"/></g>`;
	if (key === "jiuqu") core = `<path d="M35 27c5 6 4 11 0 15-3 3-2 6 1 9M34 36c-5 2-7 5-9 9M37 40c4 2 6 5 8 9" fill="none" stroke="${gold}" stroke-width="3" stroke-linecap="round"/>`;
	if (key === "butian") core = `<path d="m37 25-3 9 4 4-5 7 3 5M34 34l-7 3M34 45l-6 4M38 38l7-4" fill="none" stroke="${gold}" stroke-width="2.3" stroke-linecap="round"/>`;
	return `${links}${nodes}${core}`;
}

function furnaceMarkSVG(key, palette) {
	const [primary, accent, gold, dark] = palette;
	return `
		<svg class="furnace-mark mark-${escapeAttr(key)}" viewBox="0 0 64 48" aria-hidden="true">
			<ellipse cx="32" cy="24" rx="22" ry="15" fill="${dark}" opacity=".35"/>
			<circle cx="32" cy="24" r="12" fill="none" stroke="${gold}" stroke-width="2" stroke-dasharray="3 3"/>
			<path d="M18 24h28M32 11v26M22 15l20 18M42 15 22 33" stroke="${accent}" stroke-width="1.5" opacity=".58"/>
			<circle cx="32" cy="24" r="4" fill="${primary}" stroke="#fff0b4" stroke-width="2"/>
		</svg>
	`;
}

function herbCategory(name) {
  const value = String(name || "");
  if (/[芝菌菇]/.test(value)) return "herb-fungus";
  if (/[根参]/.test(value)) return "herb-root";
  if (/[花蕊葩]/.test(value)) return "herb-flower";
  if (/[叶草]/.test(value)) return "herb-leaf";
  return "herb-sprig";
}

function hashText(text) {
  return String(text || "").split("").reduce((sum, ch) => ((sum * 31) + ch.charCodeAt(0)) | 0, 7);
}

function actionKind(path) {
  if (path.includes("harvest")) return "harvest";
  if (path.includes("plant") || path.includes("buy-seed")) return "seed";
  if (path.includes("sell-herb")) return "market";
  if (path.includes("alchemy") || path.includes("recipe")) return "alchemy";
  return "default";
}

function actionBusyText(path) {
  if (path.includes("harvest-all")) return "正在收成熟灵草";
  if (path.includes("harvest")) return "正在收获入袋";
  if (path.includes("plant-all")) return "正在批量播种";
  if (path.includes("plant")) return "正在播种灵田";
  if (path.includes("buy-seed")) return "正在买入种子";
  if (path.includes("open-plot")) return "正在开垦新田";
  if (path.includes("sell-herb")) return "正在回收灵草";
  if (path.includes("alchemy")) return "正在开炉炼丹";
  if (path.includes("recipe")) return "正在参悟丹方";
  return "正在处理";
}

function showActionBurst(text, kind = "default") {
  const burst = document.createElement("div");
  burst.className = `action-burst ${kind}`;
  burst.innerHTML = `
    <span class="burst-dot"></span>
    <strong>${escapeHtml(text)}</strong>
    <span class="burst-dot"></span>
  `;
  document.body.appendChild(burst);
  window.setTimeout(() => burst.remove(), 1100);
}

function handleTapFeedback(event) {
  const target = event.target.closest("button, .btn, .dock-tab");
  if (!target || target.disabled || target.getAttribute("aria-disabled") === "true") return;
  if (!document.body.contains(target)) return;
  if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  showTapRipple(target, event);
}

function showTapRipple(target, event) {
  const rect = target.getBoundingClientRect();
  if (!rect.width || !rect.height) return;
  const size = Math.max(rect.width, rect.height) * 1.2;
  const ripple = document.createElement("span");
  ripple.className = "tap-ripple";
  ripple.style.width = `${size}px`;
  ripple.style.height = `${size}px`;
  ripple.style.left = `${event.clientX - rect.left}px`;
  ripple.style.top = `${event.clientY - rect.top}px`;
  target.classList.add("tap-ripple-host");
  target.appendChild(ripple);
  window.setTimeout(() => {
    ripple.remove();
    if (!target.querySelector(".tap-ripple")) target.classList.remove("tap-ripple-host");
  }, 560);
}

function haptic(type) {
  if (!tg || !tg.HapticFeedback) return;
  if (type === "selection") tg.HapticFeedback.selectionChanged();
  if (type === "impact") tg.HapticFeedback.impactOccurred("light");
  if (type === "success") tg.HapticFeedback.notificationOccurred("success");
  if (type === "error") tg.HapticFeedback.notificationOccurred("error");
}

function escapeHtml(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function escapeAttr(value) {
  return escapeHtml(value);
}
