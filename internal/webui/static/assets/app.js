/* uani built-in frontend */
(() => {
  "use strict";

  const view = document.getElementById("view");
  const titleEl = document.getElementById("page-title");
  const subtitleEl = document.getElementById("page-subtitle");
  const actionsEl = document.getElementById("topbar-actions");
  const toastStack = document.getElementById("toast-stack");
  const tokenDialog = document.getElementById("token-dialog");
  const tokenInput = document.getElementById("token-input");
  const modal = document.getElementById("modal");
  const loadingDialog = document.getElementById("loading-dialog");
  const noticeDialog = document.getElementById("notice-dialog");
  const loadingText = document.getElementById("loading-text");
  const modalBody = document.getElementById("modal-body");
  const imageDialog = document.getElementById("image-dialog");
  const imagePreview = document.getElementById("image-preview");
  const imageCaption = document.getElementById("image-caption");
  const sidebar = document.getElementById("sidebar");

  function setSidebarOpen(open) {
    sidebar.classList.toggle("open", !!open);
    document.body.classList.toggle("sidebar-open", !!open);
  }

  const state = {
    token: localStorage.getItem("uani_token") || "",
    route: "subscriptions",
    version: "uani",
    subscriptions: [],
    subscriptionFilter: "all",
    subscriptionQuery: "",
    mirrors: [],
    activeMirror: "",
    settingsTab: "bangumi",
    calendar: {},
    timeline: { days: [], day: "" },
    recommend: { tag: "", query: "", subjects: [], cache: {}, categoriesLoaded: false },
    files: { tab: "tasks", tasks: [], files: [], rules: [], catalog: [], catalogLoaded: false, settings: null, loaded: false, taskGroupOpen: {} },
    detail: { subjectId: 0, subject: null, local: null, name: "", tab: "overview", sections: {}, collectionType: 0, collectionLoaded: false, navItems: [], navIndex: -1 },
    watch: { name: "", loading: false, sources: [], selected: null, roads: [], error: "" },
    player: null,
    playerContext: null,
    image: { items: [], index: 0 },
    preloaded: [],
    timer: null,
  };

  const routes = {
    subscriptions: { title: "追番", subtitle: "按 Bangumi 收藏状态筛选，管理追番与播放", render: renderSubscriptions },
    calendar: { title: "放送日历", subtitle: "本周新番更新时间表", render: renderCalendar },
    timeline: { title: "Kazumi 时间表", subtitle: "Bangumi 当季番剧放送时间表", render: renderKazumiTimeline },
    recommend: { title: "Kazumi 推荐", subtitle: "Bangumi 热门与高分番剧", render: renderKazumiRecommend },
    files: { title: "Kazumi Files", subtitle: "下载任务、媒体文件与规则文件", render: renderFiles },
    settings: { title: "设置", subtitle: "管理令牌、Bangumi 同步与运行信息", render: renderSettings },
  };

  // ---------- helpers ----------
  const esc = (value) => String(value == null ? "" : value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[ch]));
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

  function toast(message, type = "info", duration = 3600) {
    const node = document.createElement("div");
    node.className = `toast ${type}`;
    node.textContent = message;
    toastStack.appendChild(node);
    setTimeout(() => { node.style.opacity = "0"; node.style.transform = "translateY(8px)"; node.style.transition = ".2s"; }, Math.max(1200, duration - 300));
    setTimeout(() => node.remove(), duration);
  }

  function formatSize(bytes) {
    bytes = Number(bytes || 0);
    if (bytes <= 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    return `${(bytes / Math.pow(1024, i)).toFixed(i ? 1 : 0)} ${units[i]}`;
  }

  function formatTime(ts) {
    if (!ts) return "-";
    let value = Number(ts);
    if (!Number.isFinite(value) || value <= 0) return "-";
    if (value > 1e12) {
      // already milliseconds
    } else if (value > 1e10) {
      value *= 1;
    } else {
      value *= 1000;
    }
    const d = new Date(value);
    if (Number.isNaN(d.getTime())) return "-";
    return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  }

  function dayName(day) {
    if (!day || String(day).toLowerCase() === "unknown") return "未知";
    return { mon: "周一", tue: "周二", wed: "周三", thu: "周四", fri: "周五", sat: "周六", sun: "周日" }[String(day).toLowerCase()] || day;
  }

  function coverURL(value) {
    if (!value) return "";
    let raw = String(value).trim();
    if (!raw) return "";
    if (raw.startsWith("//")) raw = "https:" + raw;
    if (raw.startsWith("http")) return raw;
    return raw.startsWith("/") ? raw : `/${raw}`;
  }

  function proxyImageURL(value) {
    if (!value) return "";
    let raw = String(value).trim();
    if (!raw) return "";
    if (raw.startsWith("//")) raw = "https:" + raw;
    if (/^https?:\/\/lain\.bgm\.tv\//i.test(raw) || /^https?:\/\/api\.bgm\.tv\/v0\/(subjects|characters|persons)\/\d+\/image(?:\?.*)?$/i.test(raw)) {
      return `/api/image?url=${encodeURIComponent(raw)}`;
    }
    return "";
  }

  function coverImgTag(url, alt = "", extraClass = "cover", extraAttrs = "", eager = false) {
    if (!url) return "";
    const raw = String(url).trim();
    const proxy = proxyImageURL(raw);
    const loading = eager ? 'loading="eager" fetchpriority="high"' : 'loading="lazy"';
    // Prefer the built-in image proxy so Bangumi covers respect the configured
    // acceleration mode instead of hanging on a slow direct connection.
    if (proxy && proxy !== raw) {
      return `<img class="${esc(extraClass)}" ${loading} decoding="async" referrerpolicy="no-referrer" src="${esc(proxy)}" alt="${esc(alt)}" ${extraAttrs} data-fallback="${esc(raw)}" onerror="if(!this.dataset.tried&&this.dataset.fallback){this.dataset.tried=1;this.src=this.dataset.fallback}else{this.style.display='none'}" />`;
    }
    return `<img class="${esc(extraClass)}" ${loading} decoding="async" referrerpolicy="no-referrer" src="${esc(raw)}" alt="${esc(alt)}" ${extraAttrs} onerror="this.style.display='none'" />`;
  }

  const preloadedImages = new Set();

  function preloadImages(urls) {
    const list = (urls || []).map((value) => String(value || "").trim()).filter(Boolean).slice(0, 24);
    for (const url of list) {
      if (preloadedImages.has(url)) continue;
      preloadedImages.add(url);
      const image = new Image();
      image.decoding = "async";
      image.loading = "lazy";
      image.src = url;
      state.preloaded.push(image);
      if (state.preloaded.length > 80) state.preloaded.shift();
    }
  }

  function preloadCovers(items) {
    if (document.hidden) return;
    const urls = (items || [])
      .map((item) => {
        const cover = coverURL(item && item.cover);
        if (!cover) return "";
        if (cover.startsWith("/") && !cover.startsWith("/api/image")) return cover;
        return proxyImageURL(cover);
      })
      .filter(Boolean)
      .slice(0, 16);
    if (!urls.length) return;
    const run = () => preloadImages(urls);
    if (typeof requestIdleCallback === "function") requestIdleCallback(run, { timeout: 800 });
    else setTimeout(run, 0);
  }

  function showNotice(title, text, type = "info", action = null) {
    const icon = document.getElementById("notice-icon");
    const titleNode = document.getElementById("notice-title");
    const textNode = document.getElementById("notice-text");
    const actions = noticeDialog ? noticeDialog.querySelector(".dialog-actions") : null;
    if (icon) { icon.textContent = type === "good" ? "✓" : type === "bad" ? "!" : type === "warn" ? "!" : "i"; icon.className = `notice-icon ${type}`; }
    if (titleNode) titleNode.textContent = title;
    if (textNode) textNode.textContent = text || "";
    if (actions) {
      actions.innerHTML = action
        ? `<button class="btn ghost" type="button" id="notice-ok">关闭</button><button class="btn primary" type="button" data-action="${esc(action.action)}">${esc(action.label)}</button>`
        : `<button class="btn primary" id="notice-ok">知道了</button>`;
      const ok = document.getElementById("notice-ok");
      if (ok) ok.onclick = () => closeDialog(noticeDialog);
    }
    toast(`${title}${text ? "：" + text : ""}`, type === "info" ? "warn" : type, 5200);
    openDialog(noticeDialog);
    const ok = document.getElementById("notice-ok");
    if (ok) setTimeout(() => ok.focus(), 0);
  }

  function showChoice(title, text, choices) {
    const icon = document.getElementById("notice-icon");
    const titleNode = document.getElementById("notice-title");
    const textNode = document.getElementById("notice-text");
    const actions = noticeDialog ? noticeDialog.querySelector(".dialog-actions") : null;
    if (icon) { icon.textContent = "i"; icon.className = "notice-icon info"; }
    if (titleNode) titleNode.textContent = title;
    if (textNode) textNode.textContent = text || "";
    if (actions) {
      actions.innerHTML = choices.map((choice) => `<button class="btn ${choice.primary ? "primary" : "ghost"}" type="button" data-action="${esc(choice.action)}">${esc(choice.label)}</button>`).join("");
    }
    openDialog(noticeDialog);
  }

  let loadingTimer = null;
  let loadingCloseTimer = null;
  let loadingValue = 0;

  function renderLoadingProgress(value) {
    const bar = document.getElementById("loading-progress");
    const label = document.getElementById("loading-percent");
    if (bar) bar.style.width = `${value}%`;
    if (label) label.textContent = `${value}%`;
  }

  function showLoading(text = "加载中…") {
    if (loadingCloseTimer) { clearTimeout(loadingCloseTimer); loadingCloseTimer = null; }
    if (loadingTimer) { clearInterval(loadingTimer); loadingTimer = null; }
    if (loadingText) loadingText.textContent = text;
    loadingValue = 4;
    renderLoadingProgress(loadingValue);
    openDialog(loadingDialog);
    loadingTimer = setInterval(() => {
      loadingValue = Math.min(90, loadingValue + Math.max(1, Math.round((90 - loadingValue) / 12)));
      renderLoadingProgress(loadingValue);
    }, 350);
  }

  function hideLoading() {
    if (loadingTimer) { clearInterval(loadingTimer); loadingTimer = null; }
    loadingValue = 100;
    renderLoadingProgress(100);
    loadingCloseTimer = setTimeout(() => {
      loadingCloseTimer = null;
      closeDialog(loadingDialog);
    }, 180);
  }

  function openDialog(dialog) {
    if (!dialog) return;
    if (dialog === modal && !modalBody.querySelector("#player-video")) stopVideoPlayback();
    if (dialog.open) return;
    try {
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    } catch (_) {
      try { dialog.setAttribute("open", ""); } catch (e) {}
    }
  }
  function closeDialog(dialog) {
    if (dialog === modal) {
      stopVideoPlayback();
      modalBody.classList.remove("player-mode");
    }
    if (typeof dialog.close === "function") dialog.close();
    else dialog.removeAttribute("open");
  }

  async function api(path, { method = "GET", body, admin = false, signal, timeout = 0 } = {}) {
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (admin) {
      if (!state.token) {
        openTokenDialog();
        throw new Error("请先设置管理令牌");
      }
      headers.Authorization = `Bearer ${state.token}`;
    }
    const controller = !signal && timeout > 0 ? new AbortController() : null;
    const fetchSignal = signal || (controller ? controller.signal : undefined);
    let timer = null;
    if (controller) timer = setTimeout(() => controller.abort(), timeout);
    let response;
    try {
      response = await fetch(path, { method, headers, signal: fetchSignal, body: body !== undefined ? JSON.stringify(body) : undefined });
    } catch (err) {
      if (err && err.name === "AbortError") throw new Error(timeout > 0 ? "请求超时，请检查网络或镜像设置" : "请求已取消");
      throw new Error(`网络错误：${err.message}`);
    } finally {
      if (timer) clearTimeout(timer);
    }
    if (response.status === 403 && admin) {
      openTokenDialog();
      throw new Error("管理令牌无效，请重新设置");
    }
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try {
        const payload = await response.json();
        message = payload.detail || payload.message || message;
      } catch (_) {}
      throw new Error(message);
    }
    if (response.status === 204) return {};
    return response.json();
  }

  function setActions(html) {
    actionsEl.innerHTML = html || "";
  }

  function loadingCards(count = 6) {
    return `<div class="grid card-grid">${Array.from({ length: count }, () => '<div class="skeleton" style="height:320px"></div>').join("")}</div>`;
  }

  function emptyState(icon, text) {
    return `<div class="empty"><div class="big">${icon}</div><div>${esc(text)}</div></div>`;
  }

  // Hovering previous/next gives the background prefetch a head start.
  document.addEventListener("mouseover", (event) => {
    const target = event.target.closest("[data-action='detail-nav']");
    if (!target || target.disabled) return;
    const items = state.detail.navItems || [];
    const index = Number(state.detail.navIndex);
    const delta = Number(target.dataset.delta || 0);
    const item = items[index + delta];
    if (item && item.subjectId) prefetchDetailSubject(item.subjectId);
  });

  // Event delegation for dynamic [data-action] (works in the view and dialogs).
  document.addEventListener("click", async (event) => {
    const target = event.target.closest("[data-action]");
    if (!target) return;
    event.preventDefault();
    try {
      await handleAction(target.dataset.action, target);
    } catch (err) {
      toast(err.message || String(err), "bad");
    }
  });

  async function handleAction(action, target) {
    switch (action) {
      case "refresh":
        closeDialog(modal);
        await renderRoute(true);
        break;
      case "calendar-catchup":
        await calendarCatchup();
        break;
      case "calendar-catchup-day":
        await calendarCatchup(target.dataset.names || "");
        break;
      case "add-subscription":
        openAddDialog();
        break;
      case "update-all":
        await updateAll();
        break;
      case "cache-followed":
        await cacheFollowed();
        break;
      case "clear-detail-cache":
        await clearDetailCache();
        break;
      case "export-follows":
        chooseFollowExport();
        break;
      case "export-follows-json":
        closeDialog(noticeDialog);
        await exportFollows();
        break;
      case "export-follows-bundle":
        closeDialog(noticeDialog);
        await exportFollowsBundle();
        break;
      case "close-notice":
        closeDialog(noticeDialog);
        break;
      case "import-follows":
        importFollows();
        break;
      case "export-cache":
        await exportCache();
        break;
      case "import-cache":
        importCache();
        break;
      case "export-downloads":
        await exportDownloads();
        break;
      case "import-downloads":
        importDownloads();
        break;
      case "export-browse-cache":
        await exportBrowseCache();
        break;
      case "import-browse-cache":
        importBrowseCache();
        break;
      case "open-show":
        await openShow(target.dataset.id, target);
        break;
      case "open-local-watch":
        await openLocalWatch(target.dataset.id, target.dataset.name);
        break;
      case "open-image":
        openImageDialog(target.dataset.src, target.dataset.title);
        break;
      case "image-prev":
        stepImage(-1);
        break;
      case "image-next":
        stepImage(1);
        break;
      case "watch-online":
        await startWatchingOnline(target.dataset.name);
        break;
      case "open-kazumi-subject": {
        const id = Number(target.dataset.id);
        const index = Number(target.dataset.cardIndex);
        let navItems = [];
        let navIndex = -1;
        if (state.route === "recommend" && Array.isArray(state.recommend.subjects)) {
          navItems = state.recommend.subjects.map((item) => ({
            subjectId: Number(item.id),
            name: item.name_cn || item.name || "",
            local: { name: item.name_cn || item.name || "", name_cn: item.name_cn, cover: item.cover || "", score: item.score, update_day: "" },
          }));
          navIndex = Number.isInteger(index) && index >= 0 ? index : navItems.findIndex((item) => item.subjectId === id);
        } else if (state.route === "timeline" && Array.isArray(state.timeline.days)) {
          const day = state.timeline.days.find((item) => item.weekday === state.timeline.day) || state.timeline.days[0];
          const list = (day && day.items) || [];
          navItems = list.map((item) => ({
            subjectId: Number(item.id),
            name: item.name_cn || item.name || "",
            local: { name: item.name_cn || item.name || "", name_cn: item.name_cn, cover: item.cover || "", score: item.score, update_day: "" },
          }));
          navIndex = Number.isInteger(index) && index >= 0 ? index : navItems.findIndex((item) => item.subjectId === id);
        }
        const card = navIndex >= 0 ? navItems[navIndex] : null;
        await openSubjectDetail({
          subjectId: id,
          name: card ? card.name : "",
          local: card ? card.local : null,
          navItems,
          navIndex,
        });
        break;
      }
      case "open-calendar-item":
        await openCalendarCard(target);
        break;
      case "detail-tab":
        await switchDetailTab(target.dataset.tab);
        break;
      case "detail-nav":
        await navigateDetail(Number(target.dataset.delta || 0));
        break;
      case "detail-retry":
        state.detail.sections[target.dataset.tab] = null;
        await switchDetailTab(target.dataset.tab, true);
        break;
      case "set-collection":
        await setCollection(Number(target.dataset.type));
        break;
      case "link-bangumi-submit":
        await linkBangumi();
        break;
      case "open-related-subject":
        await openSubjectDetail({ subjectId: Number(target.dataset.id) });
        break;
      case "start-watching":
        await startWatching(target.dataset.name);
        break;
      case "watch-source":
        await loadWatchChapters(Number(target.dataset.index));
        break;
      case "watch-episode":
        await downloadWatchEpisode(Number(target.dataset.road), Number(target.dataset.index));
        break;
      case "watch-download-all":
        await downloadWatchEpisodesBatch(Number(target.dataset.road), true);
        break;
      case "watch-download-selected":
        await downloadWatchEpisodesBatch(Number(target.dataset.road), false);
        break;
      case "watch-select-road": {
        const road = Number(target.dataset.road);
        const checked = !!target.checked;
        $$(".watch-episode-check", modalBody).forEach((box) => {
          if (Number(box.dataset.road) === road) box.checked = checked;
        });
        break;
      }
      case "watch-play":
        await playWatchEpisode(Number(target.dataset.road), Number(target.dataset.index));
        break;
      case "watch-alias":
        await startWatchingOnline(target.dataset.name);
        break;
      case "alias-add":
        await addCustomAlias();
        break;
      case "alias-remove":
        await removeCustomAlias(target.dataset.alias);
        break;
      case "watch-back":
        state.watch.selected = null;
        state.watch.roads = [];
        state.watch.showAllRoads = false;
        renderWatch();
        break;
      case "local-watch-back":
        if (state.detail && (state.detail.subjectId || state.detail.name || state.detail.local)) renderDetail();
        else closeDialog(modal);
        break;
      case "player-back":
        stopVideoPlayback();
        modalBody.classList.remove("player-mode");
        if (state.playerContext && state.playerContext.kind === "online") {
          state.playerContext = null;
          renderWatch();
        } else if (state.playerContext && state.detail && state.detail.local) renderLocalWatch();
        else closeDialog(modal);
        break;
      case "player-online-episode":
        await playWatchEpisode(Number(target.dataset.road), Number(target.dataset.index));
        break;
      case "player-online-road":
        await playWatchEpisode(Number(target.dataset.road), Number(target.dataset.episode));
        break;
      case "player-online-source":
        await switchWatchSource(Number(target.dataset.index), { episodeIndex: Number(target.dataset.episode) });
        break;
      case "detail-back":
        cancelDetailLoad();
        closeDialog(modal);
        break;
      case "watch-show-all-roads":
        state.watch.showAllRoads = true;
        renderWatch();
        break;
      case "add-kazumi-subject":
        await addKazumiSubject(target.dataset.id);
        break;
      case "timeline-day":
        state.timeline.day = target.dataset.day || "";
        renderTimelineContent();
        break;
      case "recommend-tag":
        state.recommend.tag = target.dataset.tag || "";
        $$("#view [data-action='recommend-tag']").forEach((chip) => chip.classList.toggle("active", (chip.dataset.tag || "") === state.recommend.tag));
        await loadRecommendations(state.recommend.query || "", false);
        break;
      case "delete-show":
        await deleteShow(target.dataset.name);
        break;
      case "play-episode":
        openDetailEpisode(target);
        break;
      case "player-episode":
        openPlayer(target.dataset.path, target.dataset.title, false, { ...(state.playerContext || {}), current: target.dataset.episode });
        break;
      case "kazumi-install":
        await installRule(target.dataset.name);
        break;
      case "kazumi-remove":
        await removeRule(target.dataset.name);
        break;
      case "kazumi-update":
        await updateRules(target.dataset.name || "");
        break;
      case "kazumi-rule-file":
        await openRuleFile(target.dataset.name);
        break;
      case "kazumi-save-settings":
        await saveKazumiSettings();
        break;
      case "files-tab":
        state.files.tab = target.dataset.tab;
        if (state.files.tab === "catalog" && !state.files.catalogLoaded) await loadRuleCatalog();
        renderFilesContent();
        break;
      case "go-files-tasks":
        closeDialog(noticeDialog);
        navigate("#/files");
        state.files.tab = "tasks";
        break;
      case "subscription-filter":
        state.subscriptionFilter = target.dataset.type || "all";
        updateSubscriptionFilterUI();
        break;
      case "cancel-task":
        await cancelTask(target.dataset.id);
        break;
      case "retry-task":
        await retryTask(target.dataset.id);
        break;
      case "delete-task":
        await deleteTask(target.dataset.id);
        break;
      case "retry-failed-tasks":
        await retryFailedTasks();
        break;
      case "delete-failed-tasks":
        await deleteFailedTasks();
        break;
      case "go-files-tasks":
        closeDialog(noticeDialog);
        state.files.tab = "tasks";
        navigate("#/files");
        break;
      case "kazumi-browse-refresh":
        await refreshKazumiBrowseCache();
        break;
      case "kazumi-browse-clear":
        await clearKazumiBrowseCacheAction();
        break;
      case "play-file":
        openPlayer(`/kazumi-files/${encodeURI(target.dataset.path)}`, target.dataset.name, true);
        break;
      case "delete-file":
        await deleteFile(target.dataset.path);
        break;
      case "save-rule-file":
        await saveRuleFile();
        break;
      case "bangumi-sync-run":
        await runBangumiSync();
        break;
      case "bangumi-logout":
        await bangumiLogout();
        break;
      case "save-bangumi":
        await saveBangumiSettings();
        break;
      case "save-webdav":
        await saveWebDAV();
        break;
      case "webdav-sync":
        await syncWebDAV();
        break;
      case "add-bangumi-mirror":
        await addBangumiMirror();
        break;
      case "remove-bangumi-mirror":
        await removeBangumiMirror(target.dataset.url);
        break;
      case "test-bangumi-mirror":
        await testBangumiMirror(target.dataset.url);
        break;
      case "use-bangumi-mirror":
        await setActiveBangumiMirror(target.dataset.url);
        break;
      case "use-bangumi-origin":
        await addBangumiMirror("https://api.bgm.tv");
        break;
      case "open-token":
        openTokenDialog();
        break;
      case "settings-tab":
        setSettingsTab(target.dataset.tab);
        break;
      default:
        break;
    }
  }

  // ---------- routing ----------
  function currentRoute() {
    let raw = (location.hash || "").replace(/^#\/?/, "");
    if (raw === "kazumi") {
      location.replace("#/files");
      raw = "files";
    }
    return routes[raw] ? raw : "subscriptions";
  }

  async function renderRoute(force = false) {
    state.route = currentRoute();
    const route = routes[state.route];
    titleEl.textContent = route.title;
    subtitleEl.textContent = route.subtitle;
    setActions("");
    $$(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.route === state.route));
    sidebar.classList.remove("open");
    document.body.classList.remove("sidebar-open");
    if (state.timer) { clearInterval(state.timer); state.timer = null; }
    view.classList.remove("route-switching");
    // Yield so navigation highlight updates paint before rendering.
    if (!force) await new Promise((resolve) => requestAnimationFrame(() => resolve()));
    try {
      await route.render(force);
    } catch (err) {
      const message = err.message || String(err);
      view.innerHTML = emptyState("!", message) + (message.includes("令牌") ? `<div style="text-align:center;margin-top:-24px;padding-bottom:40px"><button class="btn primary" data-action="open-token">设置管理令牌</button></div>` : "");
    }
  }

  function navigate(hash) {
    location.hash = hash;
  }

  // ---------- subscriptions ----------
  const followStatuses = [
    ["all", "全部"],
    ["3", "在看"],
    ["1", "想看"],
    ["4", "搁置"],
    ["2", "看过"],
    ["5", "抛弃"],
  ];

  async function renderSubscriptions(force = false) {
    setActions(`
      <button class="btn" data-action="refresh">刷新</button>
      <button class="btn" data-action="cache-followed">更新追番信息</button>
      <button class="btn" data-action="export-follows">导出追番</button>
      <button class="btn" data-action="import-follows">导入追番</button>
      <button class="btn" data-action="export-downloads">导出下载库</button>
      <button class="btn" data-action="import-downloads">导入下载库</button>
      <button class="btn" data-action="update-all">追更下载</button>
      <button class="btn primary" data-action="add-subscription">添加订阅</button>
    `);
    const hasCache = state.subscriptions.length > 0;
    if (hasCache) renderSubscriptionList();
    if (!hasCache || force) {
      if (!hasCache) view.innerHTML = `<div class="grid stats">${Array.from({ length: 4 }, () => '<div class="skeleton" style="height:90px"></div>').join("")}</div>${loadingCards(8)}`;
      const payload = await api(force ? "/api/index/index?refresh=1" : "/api/index/index");
      if (state.route !== "subscriptions") return;
      state.version = payload.version || state.version;
      document.getElementById("app-version").textContent = `uani ${state.version}`;
      state.subscriptions = Array.isArray(payload.data) ? payload.data : [];
      preloadCovers(state.subscriptions);
      renderSubscriptionList();
      return;
    }
    // Refresh in background so switching to this page is instant.
    api("/api/index/index").then((payload) => {
      if (state.route !== "subscriptions") return;
      state.version = payload.version || state.version;
      document.getElementById("app-version").textContent = `uani ${state.version}`;
      const next = Array.isArray(payload.data) ? payload.data : [];
      const same = JSON.stringify(next.map((x) => [x.id, x.bangumi_name, x.episode, x.status, x.downloaded, x.collection_type, x.cover])) === JSON.stringify(state.subscriptions.map((x) => [x.id, x.bangumi_name, x.episode, x.status, x.downloaded, x.collection_type, x.cover]));
      state.subscriptions = next;
      if (!same) renderSubscriptionList();
    }).catch(() => {});
  }

  function renderSubscriptionList() {
    const all = state.subscriptions;
    preloadCovers(all);
    const filter = state.subscriptionFilter || "all";
    const total = all.length;
    const updated = all.filter((item) => Number(item.status) === 2).length;
    const episodes = all.reduce((sum, item) => sum + Number(item.episode || 0), 0);
    const covers = all.filter((item) => item.cover).length;
    const counts = { all: all.length, cached: all.filter((item) => item.downloaded).length };
    for (const [value] of followStatuses) {
      if (value !== "all") counts[value] = all.filter((item) => Number(item.collection_type || 3) === Number(value)).length;
    }
    const chips = followStatuses.map(([value, label]) => `
      <button class="chip ${filter === value ? "active" : ""}" data-action="subscription-filter" data-type="${value}">
        ${label} <span class="badge muted">${counts[value] || 0}</span>
      </button>`).join("") + `<button class="chip ${filter === "cached" ? "active" : ""}" data-action="subscription-filter" data-type="cached">已缓存 <span class="badge muted">${counts.cached || 0}</span></button>`;
    view.innerHTML = `
      <div class="grid stats">
        <div class="stat"><div class="value">${total}</div><div class="label">追番总数</div></div>
        <div class="stat"><div class="value">${updated}</div><div class="label">今日有更新</div></div>
        <div class="stat"><div class="value">${episodes}</div><div class="label">已记录集数</div></div>
        <div class="stat"><div class="value">${covers}</div><div class="label">本地封面</div></div>
      </div>
      <div class="panel">
        <div class="toolbar" style="margin-bottom:14px"><div class="searchbox grow"><input id="subscription-search" type="search" placeholder="搜索追番名称…" value="${esc(state.subscriptionQuery || "")}" /></div><button class="btn" id="subscription-search-btn">搜索</button></div>
        <div class="chips" style="margin-bottom:16px">${chips}</div>
        <div id="subscription-cards"></div>
      </div>
    `;
    const cardsBox = document.getElementById("subscription-cards");
    const filtered = filteredSubscriptions();
    if (cardsBox) {
      if (!filtered.length) cardsBox.innerHTML = emptyState("▦", filter === "all" ? "没有匹配的追番" : "当前分类下没有番剧");
      else renderCardsChunked(cardsBox, filtered, showCard);
    }
    const input = document.getElementById("subscription-search");
    if (input) {
      let searchTimer = null;
      const apply = () => {
        clearTimeout(searchTimer);
        searchTimer = setTimeout(() => {
          state.subscriptionQuery = input.value;
          const box = document.getElementById("subscription-cards");
          if (!box) return;
          const list = filteredSubscriptions();
          if (!list.length) {
            box.innerHTML = emptyState("▦", state.subscriptionFilter === "all" ? "没有匹配的追番" : "当前分类下没有番剧");
            return;
          }
          renderCardsChunked(box, list, showCard);
        }, 120);
      };
      input.addEventListener("input", apply);
      input.addEventListener("keydown", (event) => { if (event.key === "Enter") { event.preventDefault(); clearTimeout(searchTimer); state.subscriptionQuery = input.value; const box = document.getElementById("subscription-cards"); if (box) { const list = filteredSubscriptions(); if (!list.length) box.innerHTML = emptyState("▦", "没有匹配的追番"); else renderCardsChunked(box, list, showCard); } } });
      const button = document.getElementById("subscription-search-btn");
      if (button) button.addEventListener("click", () => { clearTimeout(searchTimer); state.subscriptionQuery = input.value; const box = document.getElementById("subscription-cards"); if (box) { const list = filteredSubscriptions(); if (!list.length) box.innerHTML = emptyState("▦", "没有匹配的追番"); else renderCardsChunked(box, list, showCard); } });
    }
  }

  function filteredSubscriptions() {
    const filter = state.subscriptionFilter || "all";
    const query = String(state.subscriptionQuery || "").trim().toLowerCase();
    return state.subscriptions.filter((item) => {
      if (filter === "cached") {
        if (!item.downloaded) return false;
      } else if (filter !== "all" && Number(item.collection_type || 3) !== Number(filter)) return false;
      if (!query) return true;
      return [item.bangumi_name, item.display_name].filter(Boolean).some((value) => String(value).toLowerCase().includes(query));
    });
  }

  function filteredCardsHtml() {
    const filtered = filteredSubscriptions();
    if (!filtered.length) return emptyState("▦", state.subscriptionFilter === "all" ? "没有匹配的追番" : "当前分类下没有番剧");
    return `<div class="grid card-grid">${filtered.map((item, index) => showCard(item, index)).join("")}</div>`;
  }

  function renderCardsChunked(container, items, renderItem, isAlive = () => state.route === "subscriptions") {
    const total = items.length;
    if (!total) {
      container.innerHTML = emptyState("▦", "没有匹配的追番");
      return;
    }
    const chunk = 24;
    container.innerHTML = `<div class="grid card-grid" id="cards-grid"></div>`;
    const grid = container.querySelector("#cards-grid");
    let index = 0;
    const step = () => {
      if (!isAlive() || !grid || !grid.isConnected) return;
      const end = Math.min(index + chunk, total);
      let html = "";
      for (; index < end; index++) html += renderItem(items[index], index);
      grid.insertAdjacentHTML("beforeend", html);
      if (index < total) requestAnimationFrame(step);
    };
    requestAnimationFrame(step);
  }

  function renderGridChunked(grid, items, renderItem, isAlive = () => true) {
    if (!grid) return;
    const total = items.length;
    let index = 0;
    const chunk = 18;
    const token = String((Number(grid.dataset.renderToken) || 0) + 1);
    grid.dataset.renderToken = token;
    const step = () => {
      if (grid.dataset.renderToken !== token || !isAlive() || !grid.isConnected) return;
      const end = Math.min(index + chunk, total);
      let html = "";
      for (; index < end; index++) html += renderItem(items[index], index);
      grid.insertAdjacentHTML("beforeend", html);
      if (index < total) requestAnimationFrame(step);
    };
    grid.innerHTML = "";
    requestAnimationFrame(step);
  }

  function updateSubscriptionFilterUI() {
    const cards = document.getElementById("subscription-cards");
    if (!cards) {
      renderSubscriptionList();
      return;
    }
    const filter = state.subscriptionFilter || "all";
    $$("#view [data-action='subscription-filter']").forEach((chip) => {
      chip.classList.toggle("active", (chip.dataset.type || "all") === filter);
    });
    const filtered = filteredSubscriptions();
    if (!filtered.length) {
      cards.innerHTML = emptyState("▦", state.subscriptionFilter === "all" ? "没有匹配的追番" : "当前分类下没有番剧");
      return;
    }
    renderCardsChunked(cards, filtered, showCard);
  }

  function episodeSummary(item, downloadedOnly = false) {
    const source = [
      ...(!downloadedOnly && Array.isArray(item.episodes) ? item.episodes : []),
      ...(Array.isArray(item.kazumi_episodes) ? item.kazumi_episodes : []),
      ...Object.keys(item.player || {}),
    ];
    const episodes = [...new Set(source.map(Number).filter((n) => n > 0))].sort((a, b) => a - b);
    if (!episodes.length) return "0 集";
    const parts = [];
    let start = episodes[0], prev = episodes[0];
    for (let i = 1; i <= episodes.length; i++) {
      const value = episodes[i];
      if (i === episodes.length || value !== prev + 1) {
        parts.push(start === prev ? `${start}` : `${start}-${prev}`);
        start = value;
      }
      prev = value;
    }
    return `${episodes.length} 集 · ${parts.slice(0, 10).join(",")}${parts.length > 10 ? "…" : ""}`;
  }

  function showCard(item, index = -1) {
    const update = Number(item.status) === 2 ? '<span class="badge good">有更新</span>' : "";
    const kazumiCount = Array.isArray(item.kazumi_episodes) ? item.kazumi_episodes.length : 0;
    const cached = item.downloaded ? `<span class="badge good">已下载${kazumiCount ? ` ${kazumiCount} 集` : ""}</span>` : "";
    const follow = { 1: "想看", 2: "看过", 3: "在看", 4: "搁置", 5: "抛弃" }[Number(item.collection_type || 3)] || "在看";
    const cover = coverURL(item.cover);
    return `
      <article class="show-card" data-action="open-show" data-id="${esc(item.id || item.bangumi_name)}" data-card-index="${index}">
        <div class="cover-wrap">
          <div class="cover-fallback">${esc((item.bangumi_name || "?").slice(0, 1))}</div>
          ${coverImgTag(cover, item.bangumi_name || "", "cover", "", index >= 0 && index < 8)}
        </div>
        <div class="show-meta">
          <div class="show-name">${esc(item.bangumi_name)}</div>
          <div class="show-sub"><span>${esc(item.display_name || "")}</span><span>记录到 EP ${Number(item.episode || 0)}</span></div>
          <div class="show-sub"><span class="badge muted">${follow}</span>${update}${cached}<span>${esc(dayName(item.update_day))}</span></div>
          <div class="show-sub"><span>${item.downloaded ? "已下载/已缓存" : "已安装"} ${esc(episodeSummary(item, true))}</span></div>
          ${item.downloaded ? `<div class="show-sub"><button class="btn small primary" data-action="open-local-watch" data-id="${esc(item.id || "")}" data-name="${esc(item.bangumi_name || "")}">开始观看</button></div>` : ""}
        </div>
      </article>`;
  }

  async function openShow(id, target) {
    const list = filteredSubscriptions();
    let navIndex = target ? Number(target.dataset.cardIndex) : -1;
    if (!Number.isInteger(navIndex) || navIndex < 0 || navIndex >= list.length) navIndex = -1;
    const local = navIndex >= 0 ? list[navIndex] : (state.subscriptions.find((entry) => String(entry.id || entry.bangumi_name) === String(id)) || null);
    const navItems = list.map((entry) => ({ name: entry.bangumi_name, local: entry }));
    const name = (local && local.bangumi_name) || "";
    if (local) openSubjectDetail({ name, local, navItems, navIndex });
    const hasLocalDetail = local && (local.player && Object.keys(local.player).length || local.downloaded || Array.isArray(local.kazumi_episodes));
    if (hasLocalDetail) return;
    try {
      const payload = await api(`/api/player/${encodeURIComponent(id)}`, { timeout: 10000 });
      const item = payload.data || local || {};
      if (!local) {
        await openSubjectDetail({ name: item.bangumi_name || name, local: item, navItems, navIndex });
        return;
      }
      if (navIndex >= 0) navItems[navIndex] = { name: item.bangumi_name || navItems[navIndex].name, local: item };
      if (state.detail && state.detail.name === (item.bangumi_name || name)) {
        const before = JSON.stringify({ player: state.detail.local && state.detail.local.player, ep: state.detail.local && state.detail.local.episode, d: state.detail.local && state.detail.local.downloaded, ke: state.detail.local && state.detail.local.kazumi_episodes });
        state.detail.local = { ...(state.detail.local || {}), ...item };
        const after = JSON.stringify({ player: state.detail.local.player, ep: state.detail.local.episode, d: state.detail.local.downloaded, ke: state.detail.local.kazumi_episodes });
        if (before !== after) renderDetail();
      }
    } catch (err) {
      if (!local) toast(err.message || String(err), "bad");
    }
  }

  function stopVideoPlayback() {
    const player = state.player;
    state.player = null;
    if (!player) return;
    try { if (player.hls) player.hls.destroy(); } catch (_) {}
    const video = player.video;
    if (video) {
      try { video.pause(); } catch (_) {}
      video.removeAttribute("src");
      try { video.load(); } catch (_) {}
    }
  }

  function collectGalleryImages() {
    const root = modal.open ? modalBody : view;
    return $$('[data-action="open-image"]', root)
      .map((node) => ({
        src: node.dataset.src || node.getAttribute("src") || node.currentSrc || "",
        title: node.dataset.title || node.getAttribute("alt") || "",
      }))
      .filter((item) => item.src);
  }

  function openImageDialog(src, title = "") {
    if (!src) return;
    const items = collectGalleryImages();
    let index = items.findIndex((item) => item.src === src);
    if (index < 0) {
      items.unshift({ src, title });
      index = 0;
    }
    state.image = { items, index };
    renderImageDialog();
    openDialog(imageDialog);
  }

  function renderImageDialog() {
    const items = state.image.items || [];
    const index = Math.max(0, Math.min(Number(state.image.index) || 0, items.length - 1));
    const item = items[index] || {};
    state.image.index = index;
    if (imagePreview) {
      imagePreview.src = item.src || "";
      imagePreview.alt = item.title || "";
    }
    if (imageCaption) {
      const title = item.title || "图片";
      imageCaption.textContent = items.length > 1 ? `${title} · ${index + 1} / ${items.length}` : title;
    }
    const many = items.length > 1;
    const prev = document.getElementById("image-prev");
    const next = document.getElementById("image-next");
    if (prev) prev.classList.toggle("hidden", !many);
    if (next) next.classList.toggle("hidden", !many);
  }

  function stepImage(delta) {
    const items = state.image.items || [];
    if (items.length < 2 || !delta) return;
    state.image.index = (state.image.index + delta + items.length) % items.length;
    renderImageDialog();
  }

  document.addEventListener("keydown", (event) => {
    if (!imageDialog || !imageDialog.open) return;
    if (event.key === "ArrowLeft") { event.preventDefault(); stepImage(-1); }
    else if (event.key === "ArrowRight") { event.preventDefault(); stepImage(1); }
  });


  function playerEpisodesTemplate(context) {
    if (!context) return "";
    if (context.kind === "online") return onlinePlayerEpisodesTemplate(context);
    const episodes = Array.isArray(context.episodes) ? context.episodes : [];
    if (!episodes.length) return "";
    const current = String(context.current || "");
    return `
      <div class="player-episodes">
        <div class="panel-head" style="margin-top:16px">
          <h3>本地剧集</h3>
          <span class="badge good">已缓存 ${episodes.length} 集</span>
        </div>
        <div class="episode-grid">${episodes.map((entry) => {
          const episode = String(entry.episode);
          return `<button class="episode-btn ${episode === current ? "active" : ""}" data-action="player-episode" data-path="${esc(entry.path)}" data-title="${esc(entry.title || `第 ${episode} 集`)}" data-episode="${esc(episode)}">第 ${esc(episode)} 集</button>`;
        }).join("")}</div>
      </div>`;
  }

  function onlinePlayerEpisodesTemplate(context) {
    const roads = Array.isArray(context.roads) ? context.roads : [];
    if (!roads.length) return emptyState("◌", "该来源没有可用剧集");
    const roadIndex = Math.max(0, Math.min(Number(context.roadIndex) || 0, roads.length - 1));
    const road = roads[roadIndex];
    const episodeIndex = Math.max(0, Math.min(Number(context.episodeIndex) || 0, (road.urls || []).length - 1));
    const sources = (state.watch.sources || [])
      .map((item, index) => ({ ...item, index }))
      .filter((item) => item.source && !item.error);
    const currentSource = Number(context.sourceIndex);
    const sourceChips = sources.length > 1 ? `
      <div class="chips" style="margin-bottom:10px">
        <span class="muted small" style="align-self:center">切换来源：</span>
        ${sources.map((item) => `<button class="chip ${item.index === currentSource ? "active" : ""}" data-action="player-online-source" data-index="${item.index}" data-episode="${episodeIndex}" title="${esc(item.source)}">${esc(item.rule)}${item.name ? ` · ${esc(item.name)}` : ""}</button>`).join("")}
      </div>` : "";
    const roadChips = roads.length > 1 ? `
      <div class="chips" style="margin-bottom:10px">
        <span class="muted small" style="align-self:center">切换线路：</span>
        ${roads.map((item, index) => `<button class="chip ${index === roadIndex ? "active" : ""}" data-action="player-online-road" data-road="${index}" data-episode="${episodeIndex}">${esc(item.name || `线路 ${index + 1}`)}</button>`).join("")}
      </div>` : "";
    return `
      <div class="player-episodes">
        <div class="panel-head" style="margin-top:16px">
          <h3>在线剧集</h3>
          <span class="badge">${esc(road.name || `线路 ${roadIndex + 1}`)}</span>
          <span class="muted small">${(road.urls || []).length} 集</span>
        </div>
        ${sourceChips}
        ${roadChips}
        <div class="episode-grid">${(road.urls || []).map((url, index) => `
          <button class="episode-btn ${index === episodeIndex ? "active" : ""}" data-action="player-online-episode" data-road="${roadIndex}" data-index="${index}" title="${esc(url)}">${esc((road.names || [])[index] || `第 ${index + 1} 集`)}</button>`).join("")}</div>
      </div>`;
  }

  function detailPlayerContext() {
    const local = state.detail.local || {};
    const name = local.bangumi_name || local.name || state.detail.name || "本地视频";
    const episodes = localPlayerEntries()
      .map((episode) => {
        const entry = local.player[episode] || {};
        return { episode, path: entry.path || "", title: `${name} EP${episode}` };
      })
      .filter((entry) => entry.path);
    return { name, episodes, current: "" };
  }

  function onlinePlayerContext(roadIndex, episodeIndex) {
    const source = state.watch.selected || {};
    return {
      kind: "online",
      name: state.watch.name || "",
      rule: source.rule || "",
      sourceName: source.name || "",
      sourceIndex: (state.watch.sources || []).indexOf(source),
      roads: state.watch.roads || [],
      roadIndex: Number(roadIndex) || 0,
      episodeIndex: Number(episodeIndex) || 0,
    };
  }

  function openDetailEpisode(target) {
    const context = detailPlayerContext();
    context.current = String(target.dataset.episode || "");
    openPlayer(target.dataset.path, target.dataset.title, false, context);
  }

  function openPlayer(path, title, absolute = false, context = null) {
    stopVideoPlayback();
    modalBody.classList.add("player-mode");
    state.playerContext = context && (context.kind === "online" || Array.isArray(context.episodes)) ? context : null;
    const contextBadge = !state.playerContext ? "" : state.playerContext.kind === "online"
      ? `<span class="badge good">${esc(state.playerContext.rule || "在线")}${state.playerContext.sourceName ? ` · ${esc(state.playerContext.sourceName)}` : ""}</span>`
      : `<span class="badge good">已缓存 ${state.playerContext.episodes.length} 集</span>`;
    const isAbsolute = absolute || /^https?:\/\//i.test(path) || /^\/(?:kazumi-files|bangumi)\//.test(path);
    let src = isAbsolute ? path : `/bangumi${path.startsWith("/") ? "" : "/"}${path}`;
    if (!/^https?:\/\//i.test(src)) {
      src = src.split("/").map((segment, index) => index === 0 ? segment : encodeURIComponent(segment)).join("/");
    }
    const isHLS = /\.m3u8(\?|$)/i.test(src);
    modalBody.innerHTML = `
      <div class="panel-head player-head"><button class="btn small" data-action="player-back">← 返回</button><h2>${esc(title || "播放")}</h2>${contextBadge}<div class="spacer"></div><a class="btn small" href="${esc(src)}" target="_blank" rel="noopener">新窗口打开</a></div>
      <video class="player" id="player-video" controls autoplay></video>
      <p class="muted small">${isHLS ? "HLS 流媒体需要浏览器支持；已尝试使用 hls.js。" : "浏览器不支持的格式（如 MKV）请使用新窗口打开或本地播放器。"}</p>
      ${playerEpisodesTemplate(state.playerContext)}
    `;
    if (!modal.open && !modal.hasAttribute("open")) openDialog(modal);
    const video = document.getElementById("player-video");
    if (!video) return;
    const attach = () => {
      const onError = () => {
        try {
          video.removeEventListener("error", onError);
          const w = window.open(src, "_blank", "noopener,noreferrer");
          if (w) w.opener = null;
          else showNotice("播放失败", "无法直接播放该视频，已尝试在新窗口打开。若仍失败，可复制链接在播放器中打开。", "warn");
        } catch (_) {
          showNotice("播放失败", "无法直接播放该视频，请使用“新窗口打开”按钮。", "warn");
        }
      };
      video.addEventListener("error", onError);
      if (isHLS && window.Hls && window.Hls.isSupported()) {
        const hls = new window.Hls();
        hls.loadSource(src);
        hls.attachMedia(video);
        state.player = { video, hls };
      } else {
        video.src = src;
        state.player = { video };
      }
    };
    if (isHLS && !window.Hls && !window.__hlsLoading) {
      window.__hlsLoading = true;
      const script = document.createElement("script");
      script.src = "https://cdn.jsdelivr.net/npm/hls.js@1.5.20/dist/hls.min.js";
      script.onload = () => { window.__hlsLoading = false; attach(); };
      script.onerror = () => { window.__hlsLoading = false; attach(); };
      document.head.appendChild(script);
      return;
    }
    attach();
  }

  async function deleteShow(name) {
    if (!confirm(`确认取消订阅「${name}」？`)) return;
    await api("/api/admin/delete", { method: "POST", body: { bangumi: name }, admin: true });
    closeDialog(modal);
    toast("已取消订阅", "good");
    await renderRoute(true);
  }

  async function exportFollows() {
    const payload = await api("/api/admin/bangumi/export", { admin: true });
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = `uani-follows-${new Date().toISOString().slice(0, 10)}.json`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
    toast("追番信息已导出", "good");
  }

  function chooseFollowExport() {
    showChoice("导出追番", "可以只导出追番列表，也可以把已缓存的封面和番剧文字信息一起打包。", [
      { label: "取消", action: "close-notice" },
      { label: "仅追番 JSON", action: "export-follows-json" },
      { label: "含封面/文字 ZIP", action: "export-follows-bundle", primary: true },
    ]);
  }

  async function exportFollowsBundle() {
    const date = new Date().toISOString().slice(0, 10);
    const filename = `uani-follows-${date}.zip`;
    const handle = await pickArchiveTarget(filename);
    if (handle === null) return;
    showLoading("正在打包追番（封面 + 文字）…");
    try {
      const response = await fetchArchive("/api/admin/bangumi/export/bundle");
      if (await saveArchiveResponse(response, handle, filename)) {
        toast("追番备份已导出（含封面与文字）", "good");
      }
    } finally {
      hideLoading();
    }
  }

  async function exportCache() {
    showLoading("正在导出缓存数据…");
    try {
      const payload = await api("/api/admin/cache/export", { admin: true });
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: "application/json" });
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download = `uani-cache-${new Date().toISOString().slice(0, 10)}.json`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      setTimeout(() => URL.revokeObjectURL(link.href), 1000);
      toast("缓存数据已导出", "good");
    } finally {
      hideLoading();
    }
  }

  function importCache() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".json,application/json";
    input.onchange = async () => {
      const file = input.files && input.files[0];
      if (!file) return;
      if (!confirm("导入缓存会覆盖同一条目的本地缓存，但不会修改追番、订阅和已下载文件，继续吗？")) return;
      showLoading("正在导入缓存数据…");
      try {
        const text = await file.text();
        const snapshot = JSON.parse(text);
        const payload = await api("/api/admin/cache/import", { method: "POST", body: snapshot, admin: true });
        showNotice("导入完成", payload.message || "缓存数据已导入", "good");
        state.recommend.cache = {};
        state.recommend.categoriesLoaded = false;
        state.timeline.days = [];
        state.timeline.day = "";
        detailMemory.clear();
        await renderRoute(false);
      } catch (err) {
        showNotice("导入失败", err.message || String(err), "bad");
      } finally {
        hideLoading();
      }
    };
    input.click();
  }

  function archiveHeaders() {
    if (!state.token) {
      openTokenDialog();
      throw new Error("请先设置管理令牌");
    }
    return { Authorization: `Bearer ${state.token}` };
  }

  async function fetchArchive(path) {
    const response = await fetch(path, { headers: archiveHeaders() });
    if (response.status === 403) {
      openTokenDialog();
      throw new Error("管理令牌无效，请重新设置");
    }
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try {
        const payload = await response.json();
        message = payload.detail || payload.message || message;
      } catch (_) {}
      throw new Error(message);
    }
    return response;
  }

  async function pickArchiveTarget(filename) {
    if (!window.showSaveFilePicker) return undefined;
    try {
      return await window.showSaveFilePicker({ suggestedName: filename });
    } catch (err) {
      if (err && err.name === "AbortError") return null;
      return undefined;
    }
  }

  async function saveArchiveResponse(response, handle, filename) {
    if (handle && response.body) {
      const writable = await handle.createWritable();
      await response.body.pipeTo(writable);
      return true;
    }
    const blob = await response.blob();
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
    return true;
  }

  async function uploadArchive(path, file) {
    const response = await fetch(path, {
      method: "POST",
      headers: { ...archiveHeaders(), "Content-Type": "application/zip" },
      body: file,
    });
    if (response.status === 403) {
      openTokenDialog();
      throw new Error("管理令牌无效，请重新设置");
    }
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try {
        const payload = await response.json();
        message = payload.detail || payload.message || message;
      } catch (_) {}
      throw new Error(message);
    }
    return response.json();
  }

  async function exportDownloads() {
    const date = new Date().toISOString().slice(0, 10);
    const filename = `uani-downloads-${date}.zip`;
    const handle = await pickArchiveTarget(filename);
    if (handle === null) return;
    showLoading("正在打包下载库（视频 + 封面 + 信息）…");
    try {
      const response = await fetchArchive("/api/admin/kazumi/downloads/export");
      if (await saveArchiveResponse(response, handle, filename)) {
        toast("下载库压缩包已导出（含视频）", "good");
      }
    } finally {
      hideLoading();
    }
  }

  function importDownloads() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".zip,application/zip";
    input.onchange = async () => {
      const file = input.files && input.files[0];
      if (!file) return;
      if (!confirm("导入下载库压缩包会恢复视频文件、已缓存封面和番剧信息，并为缺失的已下载番剧补全追番条目，继续吗？")) return;
      showLoading("正在导入下载库（视频 + 封面 + 信息）…");
      try {
        const payload = await uploadArchive("/api/admin/kazumi/downloads/import", file);
        showNotice("导入完成", payload.message || "下载库已导入", "good");
        state.subscriptions = [];
        state.files.loaded = false;
        state.files.tasks = [];
        state.files.files = [];
        state.files.taskGroupOpen = {};
        detailMemory.clear();
        await renderSubscriptions(true);
      } catch (err) {
        showNotice("导入失败", err.message || String(err), "bad");
      } finally {
        hideLoading();
      }
    };
    input.click();
  }

  async function exportBrowseCache() {
    const date = new Date().toISOString().slice(0, 10);
    const filename = `uani-kazumi-cache-${date}.zip`;
    const handle = await pickArchiveTarget(filename);
    if (handle === null) return;
    showLoading("正在打包 Kazumi 缓存（封面 + 文字）…");
    try {
      const response = await fetchArchive("/api/admin/kazumi/browse/export");
      if (await saveArchiveResponse(response, handle, filename)) {
        toast("Kazumi 时间表/推荐缓存已导出（含封面与文字）", "good");
      }
    } finally {
      hideLoading();
    }
  }

  function importBrowseCache() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".zip,application/zip";
    input.onchange = async () => {
      const file = input.files && input.files[0];
      if (!file) return;
      if (!confirm("导入会覆盖本地 Kazumi 时间表/推荐缓存，并恢复其中的封面和番剧文字信息，继续吗？")) return;
      showLoading("正在导入 Kazumi 缓存（封面 + 文字）…");
      try {
        const payload = await uploadArchive("/api/admin/kazumi/browse/import", file);
        showNotice("导入完成", payload.message || "Kazumi 缓存已导入", "good");
        state.timeline.days = [];
        state.timeline.day = "";
        state.recommend.cache = {};
        state.recommend.subjects = [];
        state.recommend.categoriesLoaded = false;
        await renderRoute(false);
      } catch (err) {
        showNotice("导入失败", err.message || String(err), "bad");
      } finally {
        hideLoading();
      }
    };
    input.click();
  }

  function importFollows() {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".json,.zip,application/json,application/zip";
    input.onchange = async () => {
      const file = input.files && input.files[0];
      if (!file) return;
      const isZip = /\.zip$/i.test(file.name) || /zip/i.test(file.type);
      showLoading(isZip ? "正在导入追番备份（封面 + 文字）…" : "正在导入追番信息…");
      try {
        let payload;
        if (isZip) {
          payload = await uploadArchive("/api/admin/bangumi/import/bundle", file);
        } else {
          const text = await file.text();
          const snapshot = JSON.parse(text);
          payload = await api("/api/admin/bangumi/import", { method: "POST", body: snapshot, admin: true });
        }
        showNotice("导入完成", payload.message || "追番信息已导入", "good");
        await renderSubscriptions(true);
      } catch (err) {
        showNotice("导入失败", err.message || String(err), "bad");
      } finally {
        hideLoading();
      }
    };
    input.click();
  }

  async function cacheFollowed() {
    showLoading("正在下载追番完整信息…");
    try {
      const payload = await api("/api/admin/bangumi/cache", { method: "POST", body: { refresh: false }, admin: true });
      toast(payload.message || "追番信息更新完成", "good");
      await renderSubscriptions();
    } finally {
      hideLoading();
    }
  }

  async function clearDetailCache() {
    if (!confirm("清除番剧详情、角色、吐槽等缓存？不会删除追番、订阅、下载记录和已下载文件。")) return;
    showLoading("正在清理缓存…");
    try {
      const payload = await api("/api/admin/bangumi/cache/clear", { method: "POST", admin: true });
      toast(payload.message || "缓存已清除", "good");
    } finally {
      hideLoading();
    }
  }

  async function updateAll() {
    showLoading("正在启动追更…");
    try {
      await api("/api/admin/update", { method: "POST", body: { not_ignore: true }, admin: true });
      toast("已开始追更，正在后台检查并下载可用的新剧集", "good");
      setTimeout(() => { if (state.route === "subscriptions") renderSubscriptions(true); }, 4000);
      setTimeout(() => { if (state.route === "subscriptions") renderSubscriptions(true); }, 12000);
    } finally {
      hideLoading();
    }
  }

  function openAddDialog() {
    modalBody.innerHTML = `
      <h2>添加订阅</h2>
      <div class="chips" id="add-tabs">
        <button class="chip active" data-tab="name">按名称</button>
        <button class="chip" data-tab="bgm">bgm.tv 条目</button>
      </div>
      <div id="add-name-body">
        <label class="field"><span>番剧名称</span><input id="add-name" type="text" placeholder="例如：葬送的芙莉莲" /></label>
        <p class="muted small">名称可部分匹配当前数据源中的番剧。</p>
      </div>
      <div id="add-bgm-body" class="hidden">
        <label class="field"><span>bgm.tv 条目 ID 或链接</span><input id="add-bgm" type="text" placeholder="253 或 https://bgm.tv/subject/253" /></label>
        <p class="muted small">匹配不到数据源时会创建手动订阅。</p>
      </div>
      <div class="dialog-actions">
        <button class="btn ghost" type="button" id="add-cancel">取消</button>
        <button class="btn primary" type="button" id="add-confirm">添加</button>
      </div>
    `;
    openDialog(modal);
    let mode = "name";
    $$("#add-tabs .chip", modalBody).forEach((chip) => {
      chip.addEventListener("click", () => {
        mode = chip.dataset.tab;
        $$("#add-tabs .chip", modalBody).forEach((item) => item.classList.toggle("active", item === chip));
        document.getElementById("add-name-body").classList.toggle("hidden", mode !== "name");
        document.getElementById("add-bgm-body").classList.toggle("hidden", mode !== "bgm");
      });
    });
    document.getElementById("add-cancel").onclick = () => closeDialog(modal);
    document.getElementById("add-confirm").onclick = async () => {
      try {
        const body = mode === "bgm"
          ? { bgm_url: document.getElementById("add-bgm").value.trim() }
          : { name: document.getElementById("add-name").value.trim() };
        if (mode === "bgm" && !body.bgm_url) throw new Error("请输入 bgm.tv 条目");
        if (mode === "name" && !body.name) throw new Error("请输入番剧名称");
        await api("/api/admin/add", { method: "POST", body, admin: true });
        closeDialog(modal);
        toast("订阅已添加", "good");
        await renderSubscriptions();
      } catch (err) {
        toast(err.message, "bad");
      }
    };
  }

  // ---------- calendar ----------
  async function renderCalendar(force = false) {
    setActions(`<button class="btn" data-action="refresh">刷新</button>`);
    const hasCache = Object.keys(state.calendar || {}).length > 0;
    if (hasCache) renderCalendarContent();
    if (!hasCache || force) {
      if (!hasCache) view.innerHTML = loadingCards(7);
      const data = await api("/api/calendar");
      if (state.route !== "calendar") return;
      state.calendar = data || {};
      preloadCovers(Object.values(state.calendar).flat());
      renderCalendarContent();
      return;
    }
    api("/api/calendar").then((data) => {
      if (state.route !== "calendar") return;
      state.calendar = data || {};
      renderCalendarContent();
    }).catch(() => {});
  }

  async function calendarCatchup(namesArg = "") {
    let names = [];
    if (namesArg) {
      names = namesArg.split("|").map((name) => name.trim()).filter(Boolean);
    } else {
      const data = state.calendar || {};
      for (const items of Object.values(data)) {
        for (const item of (Array.isArray(items) ? items : [])) {
          if (item.followed && item.bangumi_name) names.push(item.bangumi_name);
        }
      }
    }
    names = [...new Set(names)];
    if (!names.length) {
      toast("本周没有已订阅的番剧可追更", "warn");
      return;
    }
    showLoading(`正在启动追更 ${names.length} 部番剧…`);
    try {
      await api("/api/admin/update", { method: "POST", body: { names, not_ignore: true }, admin: true });
      toast(`已开始追更 ${names.length} 部番剧，后台处理中`, "good");
      setTimeout(() => { if (state.route === "calendar") renderCalendar(true); if (state.route === "subscriptions") renderSubscriptions(true); }, 4000);
      setTimeout(() => { if (state.route === "calendar") renderCalendar(true); if (state.route === "subscriptions") renderSubscriptions(true); }, 12000);
    } finally {
      hideLoading();
    }
  }

  function renderCalendarContent() {
    const data = state.calendar || {};
    const order = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"];
    const todayKey = order[(new Date().getDay() + 6) % 7];
    const allFollowedNames = [];
    const weekMissing = [];
    for (const day of order) {
      for (const item of (Array.isArray(data[day]) ? data[day] : [])) {
        if (item.followed && item.bangumi_name) allFollowedNames.push(item.bangumi_name);
      }
    }
    const uniqueFollowed = [...new Set(allFollowedNames)];
    setActions(`<button class="btn" data-action="refresh">刷新</button>${uniqueFollowed.length ? `<button class="btn primary" data-action="calendar-catchup">追更本周 (${uniqueFollowed.length})</button>` : ""}`);

    view.innerHTML = `<div class="roads">${order.map((day) => {
      const items = Array.isArray(data[day]) ? data[day] : [];
      const followedNames = items.filter((item) => item.followed && item.bangumi_name).map((item) => item.bangumi_name);
      return `
        <section class="panel" id="day-${day}">
          <div class="panel-head">
            <h2>${dayName(day)}</h2>
            <span class="badge ${day === todayKey ? "good" : "muted"}">${items.length} 部</span>
            ${followedNames.length ? `<button class="btn small" data-action="calendar-catchup-day" data-names="${esc(followedNames.join("|"))}">追更当日</button>` : ""}
          </div>
          ${items.length ? `<div class="grid card-grid">${items.map((item, index) => {
            const installed = Number(item.installed_count || (Array.isArray(item.installed_episodes) ? item.installed_episodes.length : 0) || 0);
            const downloaded = !!item.downloaded;
            const badges = [];
            if (item.followed) badges.push(`<span class="badge ${installed > 0 ? "good" : "warn"}">已装 ${installed} 集</span>`);
            else badges.push('<span class="badge muted">未订阅</span>');
            if (downloaded) badges.push('<span class="badge good">已缓存</span>');
            return `
            <article class="show-card" data-action="open-calendar-item" data-name="${esc(item.name)}" data-cover="${esc(item.cover || "")}" data-day="${esc(item.update_day || "")}" data-list-day="${esc(day)}" data-card-index="${index}" data-episode="${Number(item.episode || 0)}" data-subtitles="${esc((item.subtitle_group || []).join(","))}" data-status="${Number(item.status || 0)}">
              <div class="cover-wrap">
                <div class="cover-fallback">${esc((item.name || "?").slice(0, 1))}</div>
                ${coverImgTag(coverURL(item.cover), "", "cover", "", index < 8)}
              </div>
              <div class="show-meta">
                <div class="show-name">${esc(item.name)}</div>
                <div class="show-sub"><span>源 ${Number(item.episode || 0)}</span><span>${esc(dayName(item.update_day || ""))}</span></div>
                <div class="chips" style="margin-top:6px">${badges.join("")}</div>
              </div>
            </article>`;
          }).join("")}</div>` : `<p class="muted small">今天没有放送。</p>`}
        </section>`;
    }).join("")}</div>`;
  }


  function calendarDetailOption(item) {
    return {
      name: item.name,
      local: {
        name: item.name,
        cover: item.cover || "",
        update_day: item.update_day || "",
        episode: Number(item.episode || 0),
        subtitle_group: Array.isArray(item.subtitle_group) ? item.subtitle_group : [],
        status: Number(item.status || 0),
      },
    };
  }

  async function openCalendarCard(target) {
    const day = target.dataset.listDay || "";
    const items = Array.isArray(state.calendar[day]) ? state.calendar[day] : [];
    const navItems = items.map(calendarDetailOption);
    let navIndex = Number(target.dataset.cardIndex);
    if (!Number.isInteger(navIndex) || navIndex < 0 || navIndex >= navItems.length) navIndex = -1;
    const current = navIndex >= 0
      ? navItems[navIndex]
      : {
        name: target.dataset.name,
        local: {
          name: target.dataset.name,
          cover: target.dataset.cover,
          update_day: target.dataset.day,
          episode: Number(target.dataset.episode || 0),
          subtitle_group: (target.dataset.subtitles || "").split(",").filter(Boolean),
          status: Number(target.dataset.status || 0),
        },
      };
    await openSubjectDetail({ ...current, navItems, navIndex });
  }


  // ---------- kazumi timeline / recommend ----------
  function kazumiCard(item, index = -1) {
    const name = item.name_cn || item.name || "未知番剧";
    const cover = coverURL(item.cover);
    const score = Number(item.score || 0);
    const scoreText = score > 0 ? score.toFixed(1) : "暂无评分";
    const tags = (item.meta_tags || []).slice(0, 2).join(" · ");
    const badgeClass = score >= 7.5 ? "good" : score > 0 ? "" : "muted";
    return `
      <article class="show-card" data-action="open-kazumi-subject" data-id="${esc(item.id)}" data-card-index="${index}">
        <div class="cover-wrap">
          <div class="cover-fallback">${esc(name.slice(0, 1))}</div>
          ${coverImgTag(cover, name, "cover", "", index >= 0 && index < 8)}
        </div>
        <div class="show-meta">
          <div class="show-name">${esc(name)}</div>
          <div class="show-sub"><span class="badge ${badgeClass}">${esc(scoreText)}</span><span>${esc(tags)}</span></div>
        </div>
      </article>`;
  }

  async function renderKazumiTimeline(force = false) {
    setActions(`<button class="btn" data-action="refresh">刷新</button><button class="btn" data-action="kazumi-browse-refresh">更新缓存</button><button class="btn" data-action="export-browse-cache">导出缓存</button><button class="btn" data-action="import-browse-cache">导入缓存</button><button class="btn" data-action="kazumi-browse-clear">清除缓存</button>`);
    const hasCache = state.timeline.days.length > 0;
    if (hasCache) renderTimelineContent();
    const url = force ? "/api/kazumi/timeline?refresh=1" : "/api/kazumi/timeline";
    if (!hasCache || force) {
      if (!hasCache) view.innerHTML = loadingCards(12);
      try {
        const payload = await api(url, { timeout: 12000 });
        if (state.route !== "timeline") return;
        state.timeline.days = payload.days || [];
        const today = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][new Date().getDay()];
        if (!state.timeline.day || !state.timeline.days.some((day) => day.weekday === state.timeline.day)) {
          state.timeline.day = today;
        }
        renderTimelineContent();
      } catch (err) {
        if (state.route !== "timeline") return;
        if (!hasCache) view.innerHTML = emptyState("!", err.message || String(err));
      }
      return;
    }
    api("/api/kazumi/timeline", { timeout: 12000 }).then((payload) => {
      if (state.route !== "timeline") return;
      const days = payload.days || [];
      const next = JSON.stringify(days.map((d) => [d.weekday, (d.items || []).map((i) => i.id)]));
      const prev = JSON.stringify((state.timeline.days || []).map((d) => [d.weekday, (d.items || []).map((i) => i.id)]));
      state.timeline.days = days;
      if (next !== prev) renderTimelineContent();
    }).catch(() => {});
  }

  function renderTimelineContent() {
    const days = state.timeline.days;
    if (!days.length) {
      view.innerHTML = emptyState("◫", "时间表暂时不可用，请稍后刷新");
      return;
    }
    const day = days.find((item) => item.weekday === state.timeline.day) || days[0];
    preloadCovers(day.items || []);
    const chips = days.map((item) => `
      <button class="chip ${item.weekday === day.weekday ? "active" : ""}" data-action="timeline-day" data-day="${esc(item.weekday)}">
        ${esc(item.label)} <span class="badge muted">${item.items.length}</span>
      </button>`).join("");
    view.innerHTML = `
      <section class="panel">
        <div class="panel-head"><h2>${esc(day.label)}放送</h2><span class="badge">${day.items.length} 部</span><div class="spacer"></div><span class="muted small">数据来自 Bangumi · 点击卡片查看详情</span></div>
        <div class="chips" style="margin-bottom:18px">${chips}</div>
        ${day.items.length ? `<div class="grid card-grid" id="timeline-grid"></div>` : emptyState("◌", "今天没有放送安排")}
      </section>`;
    if (day.items.length) {
      const expected = day.weekday;
      renderGridChunked(document.getElementById("timeline-grid"), day.items, kazumiCard, () => state.route === "timeline" && (state.timeline.days.find((d) => d.weekday === state.timeline.day) || state.timeline.days[0] || {}).weekday === expected);
    }
  }

  const recommendTags = ["", "日常", "原创", "校园", "搞笑", "奇幻", "百合", "恋爱", "悬疑", "热血", "后宫", "机战", "轻改", "偶像", "治愈", "异世界"];

  let recommendSearchTimer = null;
  let recommendRequestId = 0;
  let recommendAbort = null;
  let recommendCategoriesGeneration = 0;

  async function renderKazumiRecommend(force = false) {
    setActions(`<button class="btn" data-action="refresh">刷新</button><button class="btn" data-action="kazumi-browse-refresh">更新缓存</button><button class="btn" data-action="export-browse-cache">导出缓存</button><button class="btn" data-action="import-browse-cache">导入缓存</button><button class="btn" data-action="kazumi-browse-clear">清除缓存</button>`);
    const hasCache = state.recommend.subjects.length > 0;
    if (hasCache) renderRecommendPage();
    if (!state.recommend.categoriesLoaded) loadRecommendationCategories().catch(() => {});
    if (!hasCache || force) {
      if (!hasCache) view.innerHTML = `<section class="panel">${loadingCards(1)}<p class="muted small" style="text-align:center">正在请求 Bangumi，正在尝试直连与已配置的镜像…</p></section>`;
      loadRecommendations(state.recommend.query || "", !hasCache || force, false).catch(() => {});
      return;
    }
    loadRecommendations(state.recommend.query || "", false, true).catch(() => {});
  }

  // Load every cached recommendation category in one request. After the
  // browse cache has been refreshed this makes category chips switch without
  // touching the network.
  async function loadRecommendationCategories(retry = 0, generation = null) {
    if (retry === 0) {
      state.recommend.categoriesLoaded = true;
      generation = ++recommendCategoriesGeneration;
    }
    if (generation !== recommendCategoriesGeneration) return;
    let payload;
    try {
      payload = await api("/api/kazumi/recommend/all", { timeout: 10000 });
    } catch (err) {
      if (retry === 0) state.recommend.categoriesLoaded = false;
      throw err;
    }
    if (generation !== recommendCategoriesGeneration) return;
    const categories = payload && payload.categories ? payload.categories : {};
    let current = null;
    let known = 0;
    for (const [tag, subjects] of Object.entries(categories)) {
      if (!Array.isArray(subjects) || !subjects.length) continue;
      known++;
      const key = `${tag}|`;
      const existing = state.recommend.cache[key];
      if (existing && existing.subjects && existing.subjects.length >= subjects.length) continue;
      state.recommend.cache[key] = { at: Date.now(), subjects: subjects.slice() };
      if (!state.recommend.query && tag === (state.recommend.tag || "")) {
        current = state.recommend.subjects = subjects.slice();
        // A slow network request for this category is now unnecessary.
        if (recommendAbort) {
          try { recommendAbort.abort(); } catch (_) {}
          recommendAbort = null;
        }
      }
    }
    if (current) {
      preloadCovers(current);
      if (state.route === "recommend" && document.getElementById("recommend-grid")) {
        const grid = document.getElementById("recommend-grid");
        grid.style.opacity = "1";
        renderGridChunked(grid, current, kazumiCard, () => state.route === "recommend");
        const count = document.querySelector("#view .panel-head .badge");
        if (count) count.textContent = `${current.length} 部`;
        const status = document.getElementById("recommend-status");
        if (status) status.textContent = "";
      }
    }
    // The backend warms missing categories in the background. Poll a few
    // times so the chips become instant as the caches fill up.
    if (known < recommendTags.length && retry < 3) {
      const delay = [1200, 3000, 6000][retry];
      setTimeout(() => {
        loadRecommendationCategories(retry + 1, generation).catch(() => {});
      }, delay);
    }
  }

  async function loadRecommendations(keyword = "", fullRender = false, silent = false) {
    const tag = state.recommend.tag || "";
    state.recommend.query = keyword;
    const requestId = ++recommendRequestId;
    const cacheKey = `${tag}|${keyword}`;
    const cache = (state.recommend.cache && state.recommend.cache[cacheKey]) || null;
    const now = Date.now();
    const url = `/api/kazumi/recommend?limit=24&tag=${encodeURIComponent(tag)}&keyword=${encodeURIComponent(keyword)}`;
    const status = document.getElementById("recommend-status");
    const grid = document.getElementById("recommend-grid");

    // Stale-while-revalidate: show cached results instantly when switching.
    if (cache && cache.subjects) {
      state.recommend.subjects = cache.subjects.slice();
      preloadCovers(state.recommend.subjects);
      const gridEl = document.getElementById("recommend-grid");
      const statusEl = document.getElementById("recommend-status");
      if (fullRender || !gridEl) {
        renderRecommendPage();
      } else {
        const title = document.querySelector("#view .panel-head h2");
        const count = document.querySelector("#view .panel-head .badge");
        gridEl.style.opacity = "0.92";
        if (!state.recommend.subjects.length) gridEl.innerHTML = emptyState("★", keyword ? "没有找到匹配的番剧，试试其他关键词" : "没有找到推荐番剧");
        else renderGridChunked(gridEl, state.recommend.subjects, kazumiCard, () => state.route === "recommend");
        if (title) title.textContent = keyword ? "搜索结果" : "热门番组";
        if (count) count.textContent = `${state.recommend.subjects.length} 部`;
        if (statusEl && !silent) statusEl.innerHTML = `<span class="spin"></span> 正在更新内容…`;
      }
      if (now - (cache.at || 0) < 120000) {
        const s = document.getElementById("recommend-status");
        const g = document.getElementById("recommend-grid");
        if (s) s.textContent = "";
        if (g) g.style.opacity = "1";
        return;
      }
    } else if (fullRender) {
      view.innerHTML = `<section class="panel">${loadingCards(1)}<p class="muted small" style="text-align:center">正在请求 Bangumi，正在尝试直连与已配置的镜像…</p></section>`;
    } else if (!silent) {
      if (status) status.innerHTML = `<span class="spin"></span> 正在加载内容…`;
      if (grid) grid.style.opacity = "0.7";
      if (!grid && !status) renderRecommendPage();
    } else {
      if (status) status.textContent = "";
      if (grid) grid.style.opacity = "1";
    }

    let payload;
    try {
      if (recommendAbort) recommendAbort.abort();
      recommendAbort = new AbortController();
      payload = await api(url, { signal: recommendAbort.signal, timeout: keyword ? 12000 : 10000 });
    } catch (err) {
      const message = err.message || String(err);
      if (message === "请求已取消") return;
      if (requestId === recommendRequestId) {
        if (cache && cache.subjects) {
          const statusEl = document.getElementById("recommend-status");
          const gridEl = document.getElementById("recommend-grid");
          if (statusEl) statusEl.innerHTML = `<span class="badge warn">网络较慢</span> <span class="muted small">已显示缓存结果</span>`;
          if (gridEl) gridEl.style.opacity = "1";
        } else {
          showNotice("搜索失败", message, "bad");
          const statusEl = document.getElementById("recommend-status");
          const gridEl = document.getElementById("recommend-grid");
          if (fullRender) {
            view.innerHTML = `<section class="panel">${emptyState("!", `搜索失败：${message}`)}</section>`;
          } else {
            if (statusEl) statusEl.innerHTML = `<span class="badge bad">加载失败</span> <span class="muted small">${esc(message)}</span>`;
            if (gridEl) gridEl.style.opacity = "1";
          }
        }
      }
      return;
    }
    if (requestId !== recommendRequestId || state.route !== "recommend") return;
    state.recommend.cache[cacheKey] = { at: Date.now(), subjects: (payload.subjects || []).slice() };
    state.recommend.subjects = payload.subjects || [];
    preloadCovers(state.recommend.subjects);
    if (payload.fallback) showNotice("在线搜索暂不可用", "已显示本地时间表中匹配的番剧。可在设置或 Kazumi Files 中添加可用的 Bangumi 镜像后重试。", "warn");
    if (!fullRender && document.getElementById("recommend-grid")) {
      const gridEl = document.getElementById("recommend-grid");
      const statusEl = document.getElementById("recommend-status");
      if (gridEl) {
        gridEl.style.opacity = "1";
        if (!state.recommend.subjects.length) gridEl.innerHTML = emptyState("★", keyword ? "没有找到匹配的番剧，试试其他关键词" : "没有找到推荐番剧");
        else renderGridChunked(gridEl, state.recommend.subjects, kazumiCard, () => state.route === "recommend");
      }
      if (statusEl) statusEl.textContent = "";
      const count = document.querySelector("#view .panel-head .badge");
      if (count) count.textContent = `${state.recommend.subjects.length} 部`;
      const title = document.querySelector("#view .panel-head h2");
      if (title) title.textContent = keyword ? "搜索结果" : "热门番组";
      preloadCovers(state.recommend.subjects);
      return;
    }
    renderRecommendPage();
  }

  function renderRecommendPage() {
    const tag = state.recommend.tag || "";
    const keyword = state.recommend.query || "";
    const chips = recommendTags.map((item) => `
      <button class="chip ${item === tag ? "active" : ""}" data-action="recommend-tag" data-tag="${esc(item)}">${item ? esc(item) : "热门番组"}</button>`).join("");
    preloadCovers(state.recommend.subjects);
    view.innerHTML = `
      <section class="panel">
        <div class="panel-head"><h2>${keyword ? "搜索结果" : "热门番组"}</h2><span class="badge">${state.recommend.subjects.length} 部</span><div class="spacer"></div><span class="muted small">数据来自 Bangumi · 点击卡片查看详情</span></div>
        <div class="toolbar" style="margin-bottom:14px"><div class="searchbox grow"><input id="recommend-search" type="search" placeholder="搜索 Bangumi 番剧…" value="${esc(keyword)}" /></div><button class="btn" id="recommend-search-btn">搜索</button></div>
        <div class="chips" style="margin-bottom:18px">${chips}</div>
        <div id="recommend-status" class="muted small" style="min-height:20px;margin-bottom:8px"></div>
        <div class="grid card-grid" id="recommend-grid"></div>
      </section>`;
    const grid = document.getElementById("recommend-grid");
    if (grid) {
      if (!state.recommend.subjects.length) grid.outerHTML = emptyState("★", state.recommend.query ? "没有找到匹配的番剧，试试其他关键词" : "没有找到推荐番剧");
      else renderGridChunked(grid, state.recommend.subjects, kazumiCard, () => state.route === "recommend");
    }
    const input = document.getElementById("recommend-search");
    if (input) {
      input.addEventListener("input", () => {
        state.recommend.query = input.value;
        clearTimeout(recommendSearchTimer);
        recommendSearchTimer = setTimeout(() => {
          loadRecommendations(state.recommend.query, false);
        }, 550);
      });
      input.addEventListener("keydown", (event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          clearTimeout(recommendSearchTimer);
          loadRecommendations(state.recommend.query, false);
        }
      });
      const button = document.getElementById("recommend-search-btn");
      if (button) button.addEventListener("click", () => {
        clearTimeout(recommendSearchTimer);
        loadRecommendations(state.recommend.query, false);
      });
    }
  }

  function recommendCardsHtml() {
    if (!state.recommend.subjects.length) {
      return emptyState("★", state.recommend.query ? "没有找到匹配的番剧，试试其他关键词" : "没有找到推荐番剧");
    }
    return `<div class="grid card-grid">${state.recommend.subjects.map((item, index) => kazumiCard(item, index)).join("")}</div>`;
  }

  const collectionTypes = [
    [1, "想看"],
    [2, "看过"],
    [3, "在看"],
    [4, "搁置"],
    [5, "抛弃"],
  ];

  function cancelDetailLoad() {
    if (state.detailAbort) {
      try { state.detailAbort.abort(); } catch (_) {}
      state.detailAbort = null;
    }
    if (detailPrefetchTimer) {
      clearTimeout(detailPrefetchTimer);
      detailPrefetchTimer = null;
    }
    for (const timer of state.detailTimers || []) clearTimeout(timer);
    state.detailTimers = [];
  }

  let detailPrefetchTimer = null;
  let lastDetailOpenAt = 0;

  // In-memory detail cache. It is populated by normal detail requests and by
  // background prefetches for the adjacent cards, so previous/next remains
  // instant after the browse cache has been refreshed.
  const detailMemory = new Map();
  const detailMemoryTTL = 5 * 60 * 1000;
  const detailPrefetching = new Set();

  function rememberedDetail(subjectId) {
    const id = Number(subjectId || 0);
    if (!id) return null;
    const entry = detailMemory.get(id);
    if (!entry) return null;
    if (Date.now() - entry.at > detailMemoryTTL) {
      detailMemory.delete(id);
      return null;
    }
    return entry;
  }

  function rememberDetailSubject(subjectId, subject) {
    const id = Number(subjectId || 0);
    if (!id || !subject) return;
    const current = detailMemory.get(id);
    detailMemory.set(id, {
      subject,
      sections: current ? current.sections || null : null,
      at: Date.now(),
    });
  }

  function rememberDetailSection(subjectId, tab, payload) {
    const id = Number(subjectId || 0);
    const entry = id ? detailMemory.get(id) : null;
    if (!entry) return;
    entry.sections = { ...(entry.sections || {}), [tab]: payload };
    entry.at = Date.now();
  }

  function rememberedDetailSection(subjectId, tab) {
    const entry = rememberedDetail(subjectId);
    const section = entry && entry.sections ? entry.sections[tab] || null : null;
    // Never serve a previous "unavailable" response from memory; opening or
    // retrying the tab should ask the mirrors again.
    if (section && section.unavailable) return null;
    return section;
  }

  function prefetchDetailSubject(subjectId) {
    const id = Number(subjectId || 0);
    if (!id || detailPrefetching.has(id)) return;
    const existing = detailMemory.get(id);
    if (existing && Date.now() - existing.at < detailMemoryTTL) return;
    detailPrefetching.add(id);
    api(`/api/kazumi/subject/${id}?local=1`, { timeout: 6000 }).then((payload) => {
      if (!payload || payload.cached === false || !payload.subject) return;
      const current = detailMemory.get(id);
      detailMemory.set(id, {
        subject: payload.subject,
        sections: current ? current.sections || null : null,
        at: Date.now(),
      });
    }).catch(() => {}).finally(() => detailPrefetching.delete(id));
  }

  function prefetchAdjacentDetail(items, index, delta = 0) {
    const list = Array.isArray(items) ? items : [];
    const current = Number(index);
    if (!list.length || !Number.isInteger(current) || current < 0) return;
    const direction = Number(delta || 0);
    const offsets = direction > 0 ? [1, 2] : direction < 0 ? [-1, -2] : [-2, -1, 1, 2];
    for (const offset of offsets) {
      const item = list[current + offset];
      if (item && item.subjectId) prefetchDetailSubject(item.subjectId);
    }
  }

  function schedulePrefetchAdjacentDetail(items, index, delta = 0) {
    if (detailPrefetchTimer) clearTimeout(detailPrefetchTimer);
    // Wait for a short pause so rapid previous/next clicks do not queue up
    // extra background detail requests, then warm the direction of travel.
    detailPrefetchTimer = setTimeout(() => {
      detailPrefetchTimer = null;
      prefetchAdjacentDetail(items, index, delta);
    }, 220);
  }

  async function openSubjectDetail(options = {}) {
    modalBody.classList.remove("player-mode");
    const local = options.local || null;
    const name = String(options.name || (local && local.bangumi_name) || (local && local.name) || "").trim();
    const subjectId = Number(options.subjectId || 0);
    const navItems = Array.isArray(options.navItems) ? options.navItems : [];
    let navIndex = Number(options.navIndex);
    if (!Number.isInteger(navIndex) || navIndex < 0 || navIndex >= navItems.length) navIndex = -1;
    const localOnly = !!options.localOnly;
    const navDelta = Number(options.navDelta || 0);
    cancelDetailLoad();
    const controller = new AbortController();
    state.detailAbort = controller;
    const timers = [];
    state.detailTimers = timers;
    state.detail = {
      subjectId,
      subject: null,
      local,
      name,
      tab: "overview",
      sections: {},
      collectionType: local && local.bangumi_name ? Number(local.collection_type || 3) : 0,
      collectionLoaded: false,
      navItems,
      navIndex,
      localOnly,
      loading: !!(subjectId || name),
      loadError: "",
    };
    const remembered = subjectId ? rememberedDetail(subjectId) : null;
    if (remembered && remembered.subject) {
      applySubjectMeta({ subject: remembered.subject });
      if (remembered.sections) state.detail.sections = { ...remembered.sections };
      state.detail.loading = false;
    }
    openDialog(modal);
    renderDetail();
    // Warm the previous/next cards. The first open prefetches immediately;
    // rapid clicking is debounced so extra requests do not pile up.
    const now = Date.now();
    const rapidNavigation = now - lastDetailOpenAt < 600;
    lastDetailOpenAt = now;
    if (rapidNavigation) {
      schedulePrefetchAdjacentDetail(navItems, navIndex, navDelta);
    } else {
      prefetchAdjacentDetail(navItems, navIndex, navDelta);
    }
    const requestId = (state.detail._requestId = (state.detail._requestId || 0) + 1);
    const alive = () => state.detail._requestId === requestId && !controller.signal.aborted;
    const localQuery = localOnly ? "?local=1" : "";
    (async () => {
      try {
        if (subjectId) {
          // Local-only navigation can use the remembered copy directly. When
          // the cache was refreshed before browsing, this is the same data
          // the server would return, so avoid another round trip.
          const cached = rememberedDetail(subjectId);
          if (!(localOnly && cached && cached.subject)) {
            const payload = await api(`/api/kazumi/subject/${subjectId}${localQuery}`, { signal: controller.signal, timeout: localOnly ? 4000 : 12000 });
            if (!alive() || state.detail.subjectId !== subjectId) return;
            applySubjectMeta(payload);
            if (payload.subject) rememberDetailSubject(subjectId, payload.subject);
          }
        } else if (name) {
          try {
            const payload = await api(`/api/kazumi/lookup?name=${encodeURIComponent(name)}${localOnly ? "&local=1" : ""}`, { signal: controller.signal, timeout: localOnly ? 4000 : 12000 });
            if (!alive() || state.detail.name !== name) return;
            applySubjectMeta(payload);
            const resolved = Number(payload.subject_id || (payload.subject && payload.subject.id) || 0);
            if (resolved && payload.subject) rememberDetailSubject(resolved, payload.subject);
          } catch (err) {
            if (err && err.name === "AbortError") return;
            if (alive()) state.detail.subject = null;
          }
        }
      } catch (err) {
        if (err && err.name === "AbortError") return;
        if (alive()) state.detail.loadError = err.message || String(err);
      } finally {
        if (alive()) {
          state.detail.loading = false;
          renderDetail();
        }
      }
      if (!alive() || !state.detail.subjectId) return;
      renderDetail();
      if (state.token && !localOnly) loadCollectionType(state.detail.subjectId, controller.signal).catch(() => {});
      // Prefetch sections lazily; local-only navigation never touches the network.
      const hasLocalDetail = localOnly || !!((state.detail.local && (state.detail.local.downloaded || (state.detail.local.player && Object.keys(state.detail.local.player).length))));
      const prefetchDelay = hasLocalDetail ? 500 : 200;
      timers.push(setTimeout(() => {
        if (!alive() || !state.detail.subjectId) return;
        for (const tab of ["characters", "relations"]) {
          if (!state.detail.sections[tab]) prefetchDetailSection(tab, requestId, controller.signal).catch(() => {});
        }
        timers.push(setTimeout(() => {
          if (!alive() || !state.detail.subjectId) return;
          for (const tab of ["staff", "comments"]) {
            if (!state.detail.sections[tab]) prefetchDetailSection(tab, requestId, controller.signal).catch(() => {});
          }
        }, hasLocalDetail ? 700 : 700));
      }, prefetchDelay));
    })();
  }

  async function prefetchDetailSection(tab, requestId = state.detail._requestId, signal = null) {
    const endpoint = { comments: "comments", characters: "characters", relations: "relations", staff: "staff" }[tab];
    if (!endpoint || !state.detail.subjectId || (state.detail.sections[tab] && state.detail.sections[tab].loading)) return;
    const subjectId = state.detail.subjectId;
    const rid = requestId;
    if (!state.detail.sections[tab]) {
      const rememberedSection = rememberedDetailSection(subjectId, tab);
      if (rememberedSection) {
        state.detail.sections[tab] = rememberedSection;
        if (state.detail._requestId === rid && state.detail.tab === tab) renderDetail();
        return;
      }
    }
    const suffix = state.detail.localOnly ? "?local=1" : "";
    state.detail.sections[tab] = { loading: true };
    try {
      const payload = await api(`/api/kazumi/subject/${subjectId}/${endpoint}${suffix}`, signal ? { signal } : {});
      if (state.detail._requestId !== rid || state.detail.subjectId !== subjectId) return;
      state.detail.sections[tab] = payload;
      if (!(payload && payload.unavailable)) rememberDetailSection(subjectId, tab, payload);
    } catch (err) {
      if (err && err.name === "AbortError") return;
      if (state.detail._requestId !== rid || state.detail.subjectId !== subjectId) return;
      // Keep prefetch failures silent/non-sticky so opening the tab can retry.
      state.detail.sections[tab] = null;
    }
    if (state.detail._requestId === rid && state.detail.tab === tab) renderDetail();
  }

  function applySubjectMeta(payload) {
    const subject = payload.subject || null;
    state.detail.subject = subject;
    if (payload.subject_id) state.detail.subjectId = Number(payload.subject_id);
    if (subject && subject.subscription) {
      const current = state.detail.local || {};
      state.detail.local = { ...current, ...subject.subscription };
    }
    const collectionType = Number(subject && subject.collection_type || payload.collection_type || 0);
    if (collectionType > 0) state.detail.collectionType = collectionType;
    else if (state.detail.local && state.detail.local.bangumi_name) state.detail.collectionType = 3;
  }

  function detailName() {
    const subject = state.detail.subject || {};
    const local = state.detail.local || {};
    return subject.name_cn || subject.name || local.bangumi_name || local.name || state.detail.name || "番剧详情";
  }

  function detailCover() {
    const subject = state.detail.subject || {};
    const local = state.detail.local || {};
    const candidates = [subject.cover, local.cover].filter(Boolean);
    const localFirst = candidates.find((value) => String(value).startsWith("/")) || candidates[0] || "";
    return coverURL(localFirst);
  }

  function detailNavigation() {
    const items = state.detail.navItems || [];
    const index = Number(state.detail.navIndex);
    let html = `<div class="detail-nav"><button class="btn small" data-action="detail-back">← 返回</button>`;
    if (items.length >= 2 && index >= 0) {
      html += `
        <button class="btn small" data-action="detail-nav" data-delta="-1" ${index <= 0 ? "disabled" : ""}>‹ 上一部</button>
        <span class="muted small">${index + 1} / ${items.length}</span>
        <button class="btn small" data-action="detail-nav" data-delta="1" ${index >= items.length - 1 ? "disabled" : ""}>下一部 ›</button>`;
    }
    return html + `</div>`;
  }

  async function navigateDetail(delta) {
    const items = state.detail.navItems || [];
    const index = Number(state.detail.navIndex);
    const next = index + Number(delta || 0);
    if (!delta || index < 0 || next < 0 || next >= items.length) return;
    const now = Date.now();
    if (state.detailNavLock && now - state.detailNavLock < 60) return;
    state.detailNavLock = now;
    modalBody.scrollTop = 0;
    // Previous/next navigation is local-only so it never waits for the network.
    await openSubjectDetail({ ...items[next], navItems: items, navIndex: next, localOnly: true, navDelta: Number(delta || 0) });
  }

  function renderDetail() {
    const subject = state.detail.subject || {};
    const local = state.detail.local || {};
    const name = detailName();
    const cover = detailCover();
    const score = Number(subject.score || local.score || 0);
    const metaTags = (subject.meta_tags || []).map((tag) => `<span class="badge muted">${esc(tag)}</span>`).join("");
    const localLine = local.bangumi_name
      ? `<span class="badge">订阅中</span><span class="muted small">更新日 ${esc(dayName(local.update_day))} · EP ${Number(local.episode || 0)}</span>`
      : local.name
        ? `<span class="badge">放送日历</span><span class="muted small">${esc(dayName(local.update_day || ""))} · EP ${Number(local.episode || 0)}</span>`
        : "";
    const collection = state.detail.subjectId ? `
      <div class="collection-switch" style="margin-top:14px">
        <span class="muted small" style="margin-right:6px">Bangumi 收藏</span>
        ${collectionTypes.map(([value, label]) => `<button class="seg-btn ${Number(state.detail.collectionType) === value ? "active" : ""}" data-action="set-collection" data-type="${value}">${label}</button>`).join("")}
        ${!state.detail.collectionType ? `<span class="muted small">需在设置中连接 Bangumi 账号</span>` : ""}
      </div>` : "";
    const tabs = state.detail.subjectId ? `
      <div class="detail-tabs">
        ${[["overview", "概述"], ["comments", "吐槽"], ["characters", "角色"], ["relations", "关联"], ["staff", "制作人员"]].map(([key, label]) => `<button class="tab ${state.detail.tab === key ? "active" : ""}" data-action="detail-tab" data-tab="${key}">${label}</button>`).join("")}
      </div>` : "";
    modalBody.innerHTML = `
      ${detailNavigation()}
      <div class="detail-hero">
        ${cover ? `<img class="cover" referrerpolicy="no-referrer" src="${esc(cover)}" data-action="open-image" data-src="${esc(cover)}" data-title="${esc(name)}" alt="" onerror="this.remove()" />` : `<div class="cover-fallback">${esc(name.slice(0, 1))}</div>`}
        <div style="flex:1;min-width:0">
          <div class="chips" style="margin-bottom:8px">${localLine}</div>
          <h2>${esc(name)}</h2>
          ${subject.name && subject.name !== name ? `<p class="muted" style="margin:0 0 8px">${esc(subject.name)}</p>` : ""}
          <div class="meta">
            ${state.detail.subjectId ? `<div>评分：<strong>${score > 0 ? score.toFixed(1) : "暂无"}</strong>${subject.rank ? ` · 排名 #${subject.rank}` : ""}${subject.total ? ` · ${subject.total} 人评分` : ""}</div>` : ""}
            ${subject.date ? `<div>放送日期：${esc(subject.date)}</div>` : ""}
            ${subject.url ? `<div><a class="mono" href="${esc(subject.url)}" target="_blank" rel="noopener">${esc(subject.url)}</a></div>` : ""}
          </div>
          <div class="chips" style="margin-top:12px">${metaTags}</div>
          ${collection}
          <div style="display:flex;gap:8px;margin-top:14px;flex-wrap:wrap">
            <button class="btn primary" data-action="start-watching" data-name="${esc(name)}">开始观看</button>
            ${state.detail.subjectId && !local.bangumi_name ? `<button class="btn" data-action="add-kazumi-subject" data-id="${esc(state.detail.subjectId)}">添加订阅</button>` : ""}
            ${subject.url ? `<a class="btn ghost" href="${esc(subject.url)}" target="_blank" rel="noopener">打开 bgm.tv</a>` : ""}
            ${local.bangumi_name ? `<button class="btn danger" data-action="delete-show" data-name="${esc(local.bangumi_name)}">取消订阅</button>` : ""}
          </div>
        </div>
      </div>
      ${tabs}
      <div class="detail-body" id="detail-body">${renderDetailBody()}</div>
    `;
  }

  function renderDetailBody() {
    const tab = state.detail.tab;
    if (tab === "overview") return renderDetailOverview();
    const section = state.detail.sections[tab];
    if (!section || section.loading) return `<div class="empty"><span class="spin"></span><div class="muted small" style="margin-top:10px">正在加载…</div></div>`;
    if (section.error) return `<div class="empty"><div class="muted" style="margin-bottom:10px">${esc(section.error)}</div><button class="btn small" data-action="detail-retry" data-tab="${esc(tab)}">重试</button></div>`;
    if (tab === "comments") return renderComments(section);
    if (tab === "characters") return renderCharacters(section);
    if (tab === "relations") return renderRelations(section);
    if (tab === "staff") return renderStaff(section);
    return "";
  }

  function renderDetailOverview() {
    const subject = state.detail.subject || {};
    const local = state.detail.local || {};
    let localBlock = "";
    if (local.bangumi_name || local.name) {
      const player = local.player || {};
      const episodes = Object.keys(player).sort((a, b) => Number(a) - Number(b));
      localBlock = `
        <div class="panel" style="background:rgba(255,255,255,.03);margin-top:16px">
          <div class="panel-head"><h3>本地信息</h3><span class="badge muted">${local.bangumi_name ? "订阅" : "日历"}</span></div>
          <div class="meta">
            ${local.bangumi_name ? `<div>名称：${esc(local.bangumi_name)}</div>` : ""}
            ${local.display_name ? `<div>显示名：${esc(local.display_name)}</div>` : ""}
            <div>更新日：${esc(dayName(local.update_day || ""))}</div>
            <div>当前集数：EP ${Number(local.episode || 0)}</div>
            ${local.bangumi_name ? `<div>已安装：${esc(episodeSummary(local, true))}</div>` : ""}
            ${(local.subtitle_group || []).length ? `<div>字幕组：${esc((local.subtitle_group || []).join(" / "))}</div>` : ""}
            ${local.regex ? `<div>正则过滤：<span class="mono">${esc(local.regex)}</span></div>` : ""}
          </div>
          ${episodes.length ? `<div class="panel-head" style="margin-top:14px"><h3>本地剧集</h3><span class="muted small">${episodes.length} 集</span></div><div class="episode-grid">${episodes.map((ep) => `<button class="episode-btn" data-action="play-episode" data-episode="${esc(ep)}" data-title="${esc(local.bangumi_name || local.name)} EP${esc(ep)}" data-path="${esc(player[ep].path)}">第 ${esc(ep)} 集</button>`).join("")}</div>` : ""}
        </div>`;
    }
    const infoFields = Array.isArray(subject.info_fields) ? subject.info_fields : [];
    const info = infoFields.length ? `
      <div class="info-grid">
        ${infoFields.map((field) => `<div class="info-key">${esc(field.key)}</div><div class="info-value">${esc(field.value || "-")}</div>`).join("")}
      </div>` : "";
    const tags = (subject.tags || []).slice(0, 24).map((tag) => `<span class="chip" style="padding:4px 9px">${esc(tag.name)}${tag.count ? ` <span class="muted">${tag.count}</span>` : ""}</span>`).join("");
    const linkBlock = !state.detail.subjectId ? `
      <div class="panel" style="background:rgba(255,255,255,.03);margin-top:16px">
        <div class="panel-head"><h3>关联 Bangumi 条目</h3></div>
        <p class="muted small" style="margin-top:0">未能自动匹配到 Bangumi 条目，可输入 bgm.tv 条目 ID 或链接后查看完整详情。</p>
        <div class="toolbar">
          <div class="searchbox grow"><input id="link-subject-input" type="text" placeholder="253 或 https://bgm.tv/subject/253" /></div>
          <button class="btn primary" data-action="link-bangumi-submit">关联</button>
        </div>
      </div>` : "";
    const hasContent = !!(subject.summary || info || tags || localBlock);
    const loadingBlock = state.detail.loading && !hasContent ? `
      <div class="panel" style="background:rgba(255,255,255,.03);margin-top:16px">
        <div class="empty" style="padding:18px 10px"><span class="spin"></span><div class="muted small" style="margin-top:10px">正在加载番剧详情，请稍候…</div></div>
      </div>` : "";
    const emptyBlock = !state.detail.loading && !hasContent ? emptyState("◌", state.detail.loadError ? "详情暂时不可用，可稍后重试" : "暂无更多信息") : "";
    return `
      ${localBlock}
      ${linkBlock}
      ${loadingBlock}
      ${subject.summary ? `<div class="divider"></div><div style="white-space:pre-wrap;line-height:1.75;color:var(--muted);font-size:13px">${esc(subject.summary)}</div>` : ""}
      ${info ? `<div class="divider"></div><h3 style="margin:0 0 10px">详细信息</h3>${info}` : ""}
      ${tags ? `<div class="divider"></div><h3 style="margin:0 0 10px">标签</h3><div class="chips">${tags}</div>` : ""}
      ${emptyBlock}
    `;
  }

  function renderComments(section) {
    if (section && section.unavailable) {
      return `
        <div class="empty" style="padding:20px 10px">
          <div class="muted" style="margin-bottom:8px">吐槽暂时不可用，可能是网络或镜像响应超时。</div>
          <div class="muted small" style="margin-bottom:12px;opacity:.8">已自动尝试缓存与镜像；你可以稍后重试。</div>
          <button class="btn small" data-action="detail-retry" data-tab="comments">重试加载吐槽</button>
        </div>`;
    }
    const rawItems = Array.isArray(section) ? section : ((section && section.items) || []);
    const items = rawItems.map((item) => {
      if (!item || typeof item !== "object") return item;
      const updated = item.updated_at || item.updatedAt || item.updated || item.createdAt || item.created_at;
      return {
        ...item,
        comment: item.comment || item.content || "",
        updated_at: updated || 0,
        user: item.user || {},
      };
    });
    if (!items.length) {
      return `
        <div class="empty" style="padding:28px 10px">
          <div class="big">…</div>
          <div class="muted" style="margin-bottom:12px">还没有吐槽</div>
          <button class="btn small" data-action="detail-retry" data-tab="comments">重新加载吐槽</button>
        </div>`;
    }
    return `<div class="list">${items.map((item) => `
      <div class="comment-row fade-in">
        ${item.user && item.user.cover ? `<img class="avatar" src="${esc(coverURL(item.user.cover))}" referrerpolicy="no-referrer" loading="lazy" data-action="open-image" data-src="${esc(coverURL(item.user.cover))}" data-title="${esc(item.user.nickname || item.user.username || "")}" alt="" onerror="this.remove()" />` : `<div class="avatar avatar-fallback">${esc((item.user && (item.user.nickname || item.user.username) || "?").slice(0, 1))}</div>`}
        <div class="grow" style="flex:1;min-width:0">
          <div class="title">${esc(item.user && (item.user.nickname || item.user.username) || "匿名")} ${item.rate ? `<span class="badge warn">${item.rate} 分</span>` : ""}</div>
          <div class="comment-text" style="white-space:pre-wrap;line-height:1.6">${esc(item.comment || "")}</div>
          <div class="sub">${esc(formatTime(item.updated_at))}</div>
        </div>
      </div>`).join("")}</div>`;
  }

  function renderCharacters(section) {
    const items = section.items || [];
    if (!items.length) return emptyState("◌", "没有角色信息");
    return `<div class="person-grid">${items.map((item) => `
      <div class="person-card">
        ${item.cover ? `<img class="person-cover" loading="lazy" src="${esc(coverURL(item.cover))}" data-action="open-image" data-src="${esc(coverURL(item.cover))}" data-title="${esc(item.name || "")}" alt="" onerror="this.remove()" />` : `<div class="person-cover person-fallback">${esc((item.name || "?").slice(0, 1))}</div>`}
        <div class="person-name">${esc(item.name)}</div>
        <div class="sub">${esc(item.relation || "")}</div>
        ${(item.actors || []).length ? `<div class="sub">CV：${esc((item.actors || []).map((actor) => actor.name).join(" / "))}</div>` : ""}
      </div>`).join("")}</div>`;
  }

  function renderRelations(section) {
    const items = section.items || [];
    if (!items.length) return emptyState("◌", "没有关联条目");
    return `<div class="grid card-grid">${items.map((item) => `
      <article class="show-card" data-action="open-related-subject" data-id="${esc(item.id)}">
        <div class="cover-wrap">
          <div class="cover-fallback">${esc((item.name_cn || item.name || "?").slice(0, 1))}</div>
          ${item.cover ? `<img class="cover" loading="lazy" src="${esc(coverURL(item.cover))}" alt="" onerror="this.remove()" />` : ""}
        </div>
        <div class="show-meta"><div class="show-name">${esc(item.name_cn || item.name)}</div><div class="show-sub"><span class="badge muted">${esc(item.relation || "")}</span></div></div>
      </article>`).join("")}</div>`;
  }

  function renderStaff(section) {
    const items = section.items || [];
    if (!items.length) return emptyState("◌", "没有制作人员信息");
    return `<div class="list">${items.map((item) => `
      <div class="row">
        ${item.cover ? `<img class="avatar" src="${esc(coverURL(item.cover))}" data-action="open-image" data-src="${esc(coverURL(item.cover))}" data-title="${esc(item.name || "")}" alt="" onerror="this.remove()" />` : `<div class="avatar avatar-fallback">${esc((item.name || "?").slice(0, 1))}</div>`}
        <div class="grow" style="flex:1;min-width:0"><div class="title">${esc(item.name)}</div><div class="sub">${esc(item.relation || "")}${item.eps ? ` · ${esc(item.eps)}` : ""}${(item.career || []).length ? ` · ${esc((item.career || []).join(", "))}` : ""}</div></div>
      </div>`).join("")}</div>`;
  }

  async function switchDetailTab(tab, force = false) {
    if (!state.detail.subjectId) return;
    state.detail.tab = tab;
    const endpoint = { comments: "comments", characters: "characters", relations: "relations", staff: "staff" }[tab];
    if (!endpoint) {
      renderDetail();
      return;
    }
    const existing = state.detail.sections[tab];
    if (!force && existing) {
      // Auto-retry failed prefetch when user opens the tab.
      if (existing.error) {
        state.detail.sections[tab] = null;
      } else if (existing.loading) {
        renderDetail();
        return;
      } else {
        renderDetail();
        return;
      }
    }
    if (state.detail.sections[tab]) {
      renderDetail();
      return;
    }
    const subjectId = state.detail.subjectId;
    // A forced retry (the retry button) must always hit the network instead of
    // replaying the in-memory section that just failed.
    const rememberedSection = force ? null : rememberedDetailSection(subjectId, tab);
    if (rememberedSection) {
      state.detail.sections[tab] = rememberedSection;
      renderDetail();
      return;
    }
    const params = [];
    // Retrying a failed section must leave local-only mode: otherwise the
    // server keeps returning the local (possibly empty) cache and the retry
    // can never succeed.
    if (state.detail.localOnly && !force) params.push("local=1");
    if (force) {
      params.push("refresh=1");
      params.push(`t=${Date.now()}`);
    }
    const suffix = params.length ? `?${params.join("&")}` : "";
    state.detail.sections[tab] = { loading: true };
    renderDetail();
    try {
      const payload = await api(`/api/kazumi/subject/${subjectId}/${endpoint}${suffix}`);
      if (state.detail.subjectId !== subjectId) return;
      state.detail.sections[tab] = payload;
      if (!(payload && payload.unavailable)) rememberDetailSection(subjectId, tab, payload);
      if (force && tab === "comments" && payload && !payload.unavailable &&
          Array.isArray(payload.items) && payload.items.length === 0) {
        toast("该条目暂时没有吐槽，可稍后重试", "warn");
      }
    } catch (err) {
      if (state.detail.subjectId !== subjectId) return;
      state.detail.sections[tab] = { error: err.message || String(err) };
    }
    if (state.detail.tab === tab) renderDetail();
  }

  async function loadCollectionType(subjectId, signal = null) {
    try {
      const payload = await api(`/api/admin/bangumi/collection/${subjectId}`, { admin: true, signal });
      if (state.detail.subjectId !== subjectId) return;
      const type = Number(payload.type || 0);
      if (type > 0) state.detail.collectionType = type;
      state.detail.collectionLoaded = true;
      renderDetail();
    } catch (_) {}
  }

  async function setCollection(type) {
    if (!state.detail.subjectId) return;
    await api("/api/admin/bangumi/collection", { method: "POST", body: { subject_id: state.detail.subjectId, type }, admin: true });
    state.detail.collectionType = type;
    state.detail.collectionLoaded = true;
    const name = state.detail.local && state.detail.local.bangumi_name;
    if (name) {
      for (const item of state.subscriptions) {
        if (item.bangumi_name === name) item.collection_type = type;
      }
      if (state.route === "subscriptions") renderSubscriptionList();
    }
    renderDetail();
    toast(`已同步为「${(collectionTypes.find((item) => item[0] === type) || [0, type])[1]}」`, "good");
  }

  async function linkBangumi() {
    const input = document.getElementById("link-subject-input");
    const value = (input && input.value || "").trim();
    if (!value) throw new Error("请输入 bgm.tv 条目 ID 或链接");
    const name = state.detail.name || detailName();
    const payload = await api("/api/admin/bangumi/link", { method: "POST", body: { name, subject: value }, admin: true });
    toast("已关联 Bangumi 条目", "good");
    await openSubjectDetail({ subjectId: Number(payload.subject_id || 0), local: state.detail.local, name });
  }

  function localPlayerEntries() {
    const player = (state.detail.local && state.detail.local.player) || {};
    return Object.keys(player)
      .filter((episode) => player[episode] && player[episode].path)
      .sort((a, b) => Number(a) - Number(b));
  }

  async function openLocalWatch(id, name) {
    if (!id) throw new Error("缺少追番 ID");
    const payload = await api(`/api/player/${encodeURIComponent(id)}`);
    const item = payload.data || {};
    const title = item.bangumi_name || name || "";
    state.detail = {
      subjectId: 0,
      subject: null,
      local: item,
      name: title,
      tab: "overview",
      sections: {},
      collectionType: item.bangumi_name ? Number(item.collection_type || 3) : 0,
      collectionLoaded: false,
      navItems: [],
      navIndex: -1,
      localOnly: true,
      loading: false,
      loadError: "",
    };
    if (!localPlayerEntries().length) {
      toast("这部番还没有下载完成的视频", "warn");
      return;
    }
    renderLocalWatch();
  }

  function renderLocalWatch() {
    const local = state.detail.local || {};
    const name = local.bangumi_name || state.detail.name || "已下载剧集";
    const episodes = localPlayerEntries();
    if (!episodes.length) {
      toast("这部番还没有下载完成的视频", "warn");
      return;
    }
    modalBody.innerHTML = `
      <div class="panel-head">
        <button class="btn small" data-action="local-watch-back">← 返回</button>
        <h2>开始观看</h2><span class="badge good">本地已下载 ${episodes.length} 集</span>
        <div class="spacer"></div>
        <button class="btn small" data-action="watch-online" data-name="${esc(name)}">在线搜索更多来源</button>
      </div>
      <p class="muted small">直接播放 Kazumi Files / 本地目录中已经下载好的剧集，不再进行在线搜索。</p>
      <div class="episode-grid">${episodes.map((episode) => {
        const entry = local.player[episode] || {};
        return `<button class="episode-btn" data-action="play-episode" data-episode="${esc(episode)}" data-title="${esc(name)} EP${esc(episode)}" data-path="${esc(entry.path || "")}">第 ${esc(episode)} 集</button>`;
      }).join("")}</div>
    `;
    openDialog(modal);
  }

  async function startWatching(name) {
    if (localPlayerEntries().length) {
      renderLocalWatch();
      return;
    }
    await startWatchingOnline(name);
  }

  async function startWatchingOnline(name) {
    state.watch = { name, loading: true, sources: [], selected: null, roads: [], error: "", progress: { total: 0, done: 0 } };
    openDialog(modal);
    renderWatch();
    let rules = [];
    try {
      const payload = await api("/api/admin/kazumi/rules", { admin: true });
      rules = (payload.rules || []).map((item) => item.name || item).filter(Boolean);
    } catch (err) {
      state.watch.error = err.message || String(err);
      state.watch.loading = false;
      renderWatch();
      showNotice("搜索失败", state.watch.error, "bad");
      return;
    }
    if (!rules.length) {
      state.watch.loading = false;
      renderWatch();
      showNotice("未找到播放源", "还没有安装播放规则。可先在 Kazumi Files 安装规则后重试。", "bad");
      return;
    }
    state.watch.progress = { total: rules.length, done: 0 };
    renderWatch();
    const keyword = String(name || "").trim();
    const normalizedKeyword = normalizeWatchName(keyword);
    const limit = 4;
    let cursor = 0;
    const workers = Array.from({ length: Math.min(limit, rules.length) }, async () => {
      while (cursor < rules.length) {
        const index = cursor++;
        const rule = rules[index];
        try {
          const result = await api("/api/admin/kazumi/search", { method: "POST", body: { rule, keyword }, admin: true });
          const items = Array.isArray(result.results) ? result.results : [];
          if (!items.length) {
            state.watch.sources.push({ rule, name: "", source: "", has_match: false, error: "无结果" });
          } else {
            items.slice(0, 5).forEach((item) => {
              const sourceName = item.name || "";
              state.watch.sources.push({
                rule,
                name: sourceName,
                source: item.src || "",
                has_match: watchNameMatches(normalizedKeyword, sourceName),
              });
            });
          }
        } catch (err) {
          state.watch.sources.push({ rule, name: "", source: "", error: err.message || String(err), has_match: false });
        } finally {
          state.watch.progress.done += 1;
          state.watch.sources.sort((a, b) => Number(b.has_match) - Number(a.has_match) || String(a.rule).localeCompare(String(b.rule)) || String(a.name || "").localeCompare(String(b.name || "")));
          if (!state.watch.selected) renderWatch();
        }
      }
    });
    await Promise.allSettled(workers);
    state.watch.loading = false;
    if (!state.watch.selected) renderWatch();
    if (!state.watch.sources.some((item) => item.source && !item.error)) {
      showNotice("未找到播放源", "已安装规则没有找到可用来源。可以尝试弹窗中的别名，或先在 Kazumi Files 安装更多规则。", "bad");
    }
  }

  function normalizeWatchName(value) {
    return String(value || "").toLowerCase().replace(/[^a-z0-9\u4e00-\u9fff]/g, "");
  }

  function watchNameMatches(keyword, name) {
    const normalized = normalizeWatchName(name);
    if (!keyword || !normalized) return false;
    return normalized.includes(keyword) || keyword.includes(normalized);
  }

  function renderWatch() {
    const watch = state.watch;
    const title = watch.selected ? `${watch.name} · ${watch.selected.rule}` : watch.name;
    let body = "";
    if (watch.selected) {
      body = watchRoadsTemplate(watch.roads);
    } else if (watch.error && !(watch.sources || []).length) {
      body = emptyState("!", watch.error);
    } else {
      const sources = watch.sources || [];
      const rawAliases = [
        ...((state.detail.subject && state.detail.subject.aliases) || []),
        ...((state.detail.local && state.detail.local.aliases) || []),
      ];
      const seenAliases = new Set([String(watch.name || "").toLowerCase()]);
      const aliases = rawAliases
        .map((value) => String(value || "").trim())
        .filter((value) => {
          const key = value.toLowerCase();
          if (!value || seenAliases.has(key)) return false;
          seenAliases.add(key);
          return true;
        })
        .slice(0, 8);
      const aliasChips = aliases.length ? `<div class="chips" style="margin-bottom:12px"><span class="muted small" style="align-self:center">使用别名搜索：</span>${aliases.map((value) => `<button class="chip" data-action="watch-alias" data-name="${esc(value)}">${esc(value)}</button>`).join("")}</div>` : "";
      const local = state.detail.local || {};
      const customAliases = local.custom_aliases || [];
      const aliasManage = local.bangumi_name ? `<div class="toolbar" style="margin-bottom:12px">
        <div class="searchbox grow"><input id="alias-input" type="text" placeholder="添加自定义别名…" /></div>
        <button class="btn small" data-action="alias-add">添加别名</button>
        ${customAliases.length ? `<div class="chips">${customAliases.map((value) => `<span class="chip">${esc(value)} <button class="alias-remove" data-action="alias-remove" data-alias="${esc(value)}" title="删除别名">×</button></span>`).join("")}</div>` : ""}
      </div>` : "";
      const progress = watch.progress || { total: 0, done: 0 };
      const progressBar = watch.loading ? `
        <div class="panel" style="margin-bottom:12px;padding:10px 12px;background:rgba(255,255,255,.03)">
          <div style="display:flex;align-items:center;gap:10px"><span class="spin"></span><div class="muted small">正在并行搜索播放来源… ${progress.done}/${progress.total || "?"}</div></div>
        </div>` : "";
      if (!sources.length && watch.loading) {
        body = aliasManage + aliasChips + progressBar + emptyState("⌕", "正在搜索已安装规则，请稍候…");
      } else if (!sources.length) {
        body = aliasManage + aliasChips + emptyState("⌕", "已安装规则都没有找到可用来源，可尝试上面的别名，或先在 Kazumi Files 安装更多规则");
      } else {
        body = aliasManage + aliasChips + progressBar + `<div class="list">${sources.map((item, index) => `
          <div class="row">
            <div class="grow">
              <div class="title">${esc(item.rule)} <span class="badge ${item.has_match ? "good" : "muted"}">${item.has_match ? "匹配" : "结果"}</span></div>
              <div class="sub">${item.error ? esc(item.error) : esc(item.name || "无结果")}</div>
            </div>
            <div class="actions">
              ${item.error || !item.source ? `<span class="badge ${watch.loading ? "muted" : "bad"}">${watch.loading && !item.error && !item.source ? "等待中" : "不可用"}</span>` : `<button class="btn small primary" data-action="watch-source" data-index="${index}">选集</button>`}
            </div>
          </div>`).join("")}</div>`;
      }
    }
    const scrollTop = modalBody.scrollTop;
    const preserveScroll = !watch.selected && (watch.loading || (watch.sources || []).length > 0);
    modalBody.innerHTML = `
      <div class="panel-head">
        <h2>开始观看</h2>
        <span class="badge muted">${esc(title)}</span>
        ${watch.loading ? `<span class="badge">${(watch.progress || {}).done || 0}/${(watch.progress || {}).total || 0}</span>` : ""}
        <div class="spacer"></div>
        ${watch.selected ? `<button class="btn small" data-action="watch-back">返回来源</button>` : ""}
      </div>
      <div class="fade-in">${body}</div>
    `;
    if (preserveScroll) modalBody.scrollTop = scrollTop;
  }

  function watchRoadsTemplate(roads) {
    if (!roads || !roads.length) return emptyState("◌", "该来源没有可用剧集");
    const bestIndex = roads.reduce((best, road, index, list) => (road.urls.length > list[best].urls.length ? index : best), 0);
    return `<div class="roads">${roads.map((road, roadIndex) => {
      const hidden = roads.length > 1 && roadIndex !== bestIndex && !state.watch.showAllRoads;
      return `
      <div class="road-block ${hidden ? "hidden" : ""}" data-road-block="${roadIndex}">
        <div class="road-head">
          <label class="muted small" style="display:flex;align-items:center;gap:6px"><input type="checkbox" data-action="watch-select-road" data-road="${roadIndex}" /> 全选</label>
          <span class="badge">${esc(road.name)}</span>
          <span class="muted small">${road.urls.length} 集</span>
          <div class="spacer"></div>
          <button class="btn small" data-action="watch-download-selected" data-road="${roadIndex}">下载所选</button>
          <button class="btn small primary" data-action="watch-download-all" data-road="${roadIndex}">下载全部</button>
        </div>
        <div class="episode-list">${road.urls.map((url, index) => `
          <div class="episode-item">
            <label class="episode-label" style="display:flex;align-items:center;gap:8px;flex:1;min-width:0">
              <input type="checkbox" class="watch-episode-check" data-road="${roadIndex}" data-index="${index}" />
              <span>${esc(road.names[index] || `第${index + 1}集`)}</span>
            </label>
            <div class="episode-actions">
              <button class="btn small" data-action="watch-play" data-road="${roadIndex}" data-index="${index}">播放</button>
              <button class="btn small primary" data-action="watch-episode" data-road="${roadIndex}" data-index="${index}">下载</button>
            </div>
          </div>`).join("")}</div>
      </div>`;
    }).join("")}</div>`;
  }

  async function playWatchEpisode(roadIndex, episodeIndex) {
    const item = state.watch.selected;
    if (!item) throw new Error("请先选择来源");
    const road = state.watch.roads[roadIndex];
    if (!road) throw new Error("线路不存在");
    const source = road.urls[episodeIndex];
    const title = road.names[episodeIndex] || state.watch.name;
    showLoading("正在解析播放地址…");
    let payload;
    try {
      payload = await api("/api/admin/kazumi/resolve", { method: "POST", body: { rule: item.rule, source }, admin: true });
    } catch (err) {
      showNotice("解析失败", err.message || String(err), "bad");
      return;
    } finally {
      hideLoading();
    }
    if (payload && payload.media && payload.url) {
      try {
        openPlayer(payload.url, title, true, onlinePlayerContext(roadIndex, episodeIndex));
        return;
      } catch (_) {}
    }
    const opened = payload && payload.url ? window.open(payload.url, "_blank", "noopener,noreferrer") : null;
    if (opened) opened.opener = null;
    if (payload && payload.url) {
      if (!opened) {
        modalBody.scrollTop = 0;
        modalBody.innerHTML = `
          <div class="panel-head"><h2>${esc(title)}</h2></div>
          <p class="muted small">浏览器拦截了新窗口，请点击下面的按钮打开${payload.media ? "视频" : "播放页"}。</p>
          <div class="dialog-actions">
            <a class="btn primary" href="${esc(payload.url)}" target="_blank" rel="noopener">打开${payload.media ? "视频" : "播放页"}</a>
            <button class="btn" data-action="watch-back">返回</button>
          </div>`;
        openDialog(modal);
      }
      return;
    }
    showNotice("无播放地址", "未解析到可用的视频地址，可返回列表尝试其他来源。", "warn");
  }

  async function switchWatchSource(index, options = {}) {
    const item = state.watch.sources[index];
    if (!item || !item.source) throw new Error("来源不可用");
    const wantedEpisode = Math.max(0, Number(options.episodeIndex) || 0);
    showLoading("正在切换来源…");
    let roads = [];
    try {
      const payload = await api("/api/admin/kazumi/chapters", { method: "POST", body: { rule: item.rule, source: item.source }, admin: true });
      roads = payload.roads || [];
    } catch (err) {
      showNotice("切换来源失败", err.message || String(err), "bad");
      return;
    } finally {
      hideLoading();
    }
    if (!roads.length) {
      showNotice("来源不可用", "该来源没有可用剧集，请换一个来源。", "warn");
      return;
    }
    state.watch.selected = item;
    state.watch.roads = roads;
    state.watch.showAllRoads = false;
    const bestRoad = roads.reduce((best, road, idx, list) => ((road.urls || []).length > (list[best].urls || []).length ? idx : best), 0);
    const episodeIndex = Math.min(wantedEpisode, Math.max(0, (roads[bestRoad].urls || []).length - 1));
    await playWatchEpisode(bestRoad, episodeIndex);
  }

  async function loadWatchChapters(index) {
    const item = state.watch.sources[index];
    if (!item || !item.source) throw new Error("来源不可用");
    state.watch.selected = item;
    state.watch.roads = [];
    renderWatch();
    showLoading("正在加载剧集…");
    try {
      const payload = await api("/api/admin/kazumi/chapters", { method: "POST", body: { rule: item.rule, source: item.source }, admin: true });
      state.watch.roads = payload.roads || [];
    } catch (err) {
      state.watch.roads = [];
      toast(err.message || String(err), "bad");
    } finally {
      hideLoading();
    }
    renderWatch();
  }

  async function downloadWatchEpisode(roadIndex, episodeIndex) {
    await queueWatchDownload(roadIndex, episodeIndex, { notify: true });
  }

  async function queueWatchDownload(roadIndex, episodeIndex, options = {}) {
    const item = state.watch.selected;
    if (!item) throw new Error("请先选择来源");
    const road = state.watch.roads[roadIndex];
    if (!road) throw new Error("线路不存在");
    const source = road.urls[episodeIndex];
    const title = road.names[episodeIndex] || state.watch.name;
    const episode = episodeFromName(title, episodeIndex + 1);
    const local = state.detail.local || {};
    const downloadName = local.bangumi_name || state.watch.name;
    await api("/api/admin/kazumi/download", {
      method: "POST",
      admin: true,
      body: { rule: item.rule, name: downloadName, bangumi_id: state.detail.subjectId || undefined, title, episode, source },
    });
    if (options.notify) {
      toast(`已加入下载：${state.watch.name} 第 ${episode} 集（${item.rule}）`, "good");
      showNotice("开始下载", `${downloadName} 第 ${episode} 集已加入下载队列（${item.rule}）。`, "good", { action: "go-files-tasks", label: "查看下载" });
    }
    return episode;
  }

  async function downloadWatchEpisodesBatch(roadIndex, all = false) {
    const road = state.watch.roads[roadIndex];
    if (!road) throw new Error("线路不存在");
    let indexes = road.urls.map((_, index) => index);
    if (!all) {
      indexes = indexes.filter((index) => {
        const box = modalBody.querySelector(`.watch-episode-check[data-road="${roadIndex}"][data-index="${index}"]`);
        return box && box.checked;
      });
    }
    if (!indexes.length) {
      toast("请先选择要下载的剧集", "warn");
      return;
    }
    showLoading(`正在加入 ${indexes.length} 个下载任务…`);
    let ok = 0;
    let failed = 0;
    try {
      for (const index of indexes) {
        try {
          await queueWatchDownload(roadIndex, index, { notify: false });
          ok += 1;
        } catch (_) {
          failed += 1;
        }
      }
    } finally {
      hideLoading();
    }
    const msg = failed ? `已加入 ${ok} 个下载任务，${failed} 个失败` : `已加入 ${ok} 个下载任务`;
    showNotice(failed ? "批量下载部分完成" : "批量下载已加入队列", msg, failed ? "warn" : "good", { action: "go-files-tasks", label: "查看下载" });
    toast(msg, failed ? "warn" : "good");
  }

  function aliasOwnerName() {
    const local = state.detail.local || {};
    return local.bangumi_name || state.detail.name || state.watch.name;
  }

  async function addCustomAlias() {
    const input = document.getElementById("alias-input");
    const alias = (input && input.value || "").trim();
    if (!alias) throw new Error("请输入别名");
    const name = aliasOwnerName();
    await api("/api/admin/bangumi/alias", { method: "POST", body: { name, alias }, admin: true });
    const local = state.detail.local || {};
    local.custom_aliases = Array.from(new Set([...(local.custom_aliases || []), alias]));
    local.aliases = Array.from(new Set([...(local.aliases || []), alias]));
    state.detail.local = local;
    toast(`已添加别名：${alias}`, "good");
    await startWatchingOnline(alias);
  }

  async function removeCustomAlias(alias) {
    if (!alias) return;
    const name = aliasOwnerName();
    await api(`/api/admin/bangumi/alias?name=${encodeURIComponent(name)}&alias=${encodeURIComponent(alias)}`, { method: "DELETE", admin: true });
    const local = state.detail.local || {};
    local.custom_aliases = (local.custom_aliases || []).filter((value) => value !== alias);
    local.aliases = (local.aliases || []).filter((value) => value !== alias);
    state.detail.local = local;
    toast("别名已删除", "good");
    if (state.watch.name) await startWatchingOnline(state.watch.name);
    else renderWatch();
  }

  async function addKazumiSubject(id) {
    await api("/api/admin/add", { method: "POST", body: { bgm_id: id }, admin: true });
    toast("已添加订阅", "good");
    closeDialog(modal);
    await renderRoute(true);
  }

  // ---------- kazumi ----------
  function modeOptions(options, current) {
    return (options || []).map((item) => `<option value="${esc(item.value)}" ${item.value === current ? "selected" : ""}>${esc(item.label)}</option>`).join("");
  }

  function installedRulesTemplate(rules) {
    if (!rules.length) return emptyState("K", "还没有安装规则，请到「规则仓库」安装");
    return `<div class="panel"><div class="panel-head"><h2>已安装规则</h2><span class="badge muted">${rules.length}</span><div class="spacer"></div><button class="btn small" data-action="kazumi-update">更新全部</button></div>
      <div class="list">${rules.map((rule) => `
        <div class="row">
          <div class="grow">
            <div class="title">${esc(rule.name)} <span class="badge muted">${esc(rule.version || "-")}</span></div>
            <div class="sub">${esc(rule.api || "")} · ${rule.antiCrawlerEnabled ? "反爬" : "普通"} · ${esc(formatTime(rule.updated_at))}</div>
          </div>
          <div class="actions">
            <button class="btn small" data-action="kazumi-update" data-name="${esc(rule.name)}">更新</button>
            <button class="btn small" data-action="kazumi-rule-file" data-name="${esc(rule.name)}">文件</button>
            <button class="btn small danger" data-action="kazumi-remove" data-name="${esc(rule.name)}">删除</button>
          </div>
        </div>`).join("")}</div></div>`;
  }

  function catalogTemplate(items) {
    if (!items.length) return emptyState("⌕", "仓库中没有匹配的规则");
    return `<div class="panel"><div class="panel-head"><h2>规则仓库</h2><div class="spacer"></div><span class="muted small">${esc((state.files.settings && state.files.settings.rule_repository) || "")}</span></div>
      <div class="searchbox" style="margin-bottom:12px"><input id="catalog-filter" type="search" placeholder="搜索规则名称或作者…" /></div>
      <div class="list" id="catalog-list">${items.map((item) => `
        <div class="row">
          <div class="grow"><div class="title">${esc(item.name)} <span class="badge muted">${esc(item.version || "-")}</span></div><div class="sub">${esc(item.author || "KazumiRules")} · ${item.installed ? "已安装 " + esc(item.installed_version || "") : "未安装"}</div></div>
          <div class="actions">${item.installed ? '<span class="badge good">已安装</span>' : `<button class="btn small primary" data-action="kazumi-install" data-name="${esc(item.name)}">安装</button>`}</div>
        </div>`).join("")}</div></div>`;
  }

  function networkSettingsTemplate(settings) {
    const value = settings || {};
    const mirrors = bangumiMirrorRows(state.mirrors, state.activeMirror);
    return `<div class="panel"><div class="panel-head"><h2>网络加速</h2></div>
      <div class="grid form-grid">
        <label class="field"><span>番剧条目</span><select id="setting-bangumi">${modeOptions(value.bangumi_modes, value.bangumi_acceleration)}</select></label>
        <label class="field"><span>规则仓库镜像</span><select id="setting-rules">${modeOptions(value.rules_modes, value.rules_mirror)}</select></label>
        <label class="field"><span>图片加速</span><select id="setting-image">${modeOptions(value.image_modes, value.image_acceleration)}</select></label>
        <label class="field"><span>自定义规则仓库</span><input id="setting-repo" type="text" value="${esc(value.rule_repository || "")}" placeholder="https://raw.githubusercontent.com/..." /></label>
      </div>
      <div class="divider"></div>
      <div class="panel-head"><h3>Bangumi 镜像</h3><span class="badge muted">可添加多个</span></div>
      <p class="muted small">搜索、吐槽和详情接口都会并发尝试这些地址；留空使用默认镜像。</p>
      <div class="list">${mirrors}</div>
      <div class="toolbar" style="margin-top:12px">
        <div class="searchbox grow"><input id="bangumi-mirror-new" type="text" placeholder="https://next.bangumi.lat" /></div>
        <button class="btn" data-action="add-bangumi-mirror">添加</button>
        <button class="btn" data-action="use-bangumi-origin">原地址</button>
      </div>
      <p class="muted small" id="mirror-result" style="margin-bottom:0"></p>
      <div class="dialog-actions" style="justify-content:flex-start;margin-top:14px">
        <button class="btn primary" data-action="kazumi-save-settings">保存设置</button>
      </div></div>`;
  }

  function bindCatalogFilter() {
    const filter = document.getElementById("catalog-filter");
    if (!filter) return;
    filter.addEventListener("input", () => {
      const keyword = filter.value.toLowerCase().trim();
      const items = state.files.catalog.filter((item) => !keyword || item.name.toLowerCase().includes(keyword) || String(item.author || "").toLowerCase().includes(keyword));
      const list = document.getElementById("catalog-list");
      if (list) list.innerHTML = items.map((item) => `
        <div class="row">
          <div class="grow"><div class="title">${esc(item.name)} <span class="badge muted">${esc(item.version || "-")}</span></div><div class="sub">${esc(item.author || "KazumiRules")} · ${item.installed ? "已安装 " + esc(item.installed_version || "") : "未安装"}</div></div>
          <div class="actions">${item.installed ? '<span class="badge good">已安装</span>' : `<button class="btn small primary" data-action="kazumi-install" data-name="${esc(item.name)}">安装</button>`}</div>
        </div>`).join("");
    });
  }

  async function loadRuleCatalog() {
    const payload = await api("/api/admin/kazumi/catalog", { admin: true });
    state.files.catalog = payload.rules || [];
    state.files.catalogLoaded = true;
  }

  async function refreshFiles(tab) {
    await loadFiles();
    if (tab === "catalog") await loadRuleCatalog();
    renderFilesContent();
  }

  async function installRule(name) {
    await api("/api/admin/kazumi/rules", { method: "POST", body: { names: [name] }, admin: true });
    toast(`已安装 ${name}`, "good");
    await refreshFiles(state.files.tab);
  }

  async function removeRule(name) {
    if (!confirm(`删除规则 ${name}？`)) return;
    await api(`/api/admin/kazumi/rules/${encodeURIComponent(name)}`, { method: "DELETE", admin: true });
    toast("规则已删除", "good");
    await refreshFiles(state.files.tab);
  }

  async function updateRules(name) {
    await api("/api/admin/kazumi/rules/update", { method: "POST", body: name ? { names: [name] } : {}, admin: true });
    toast("规则已更新", "good");
    await refreshFiles(state.files.tab);
  }

  async function saveKazumiSettings() {
    const body = {
      bangumi_acceleration: document.getElementById("setting-bangumi").value,
      rules_mirror: document.getElementById("setting-rules").value,
      image_acceleration: document.getElementById("setting-image").value,
      bangumi_mirrors: state.mirrors,
      rule_repository: document.getElementById("setting-repo").value.trim(),
    };
    await api("/api/admin/kazumi/settings", { method: "PATCH", body, admin: true });
    toast("网络设置已保存", "good");
    await refreshFiles("network");
  }

  function episodeFromName(name, fallback) {
    const match = String(name || "").match(/(\d+(?:\.\d+)?)/);
    if (!match) return fallback;
    const value = Number(match[1]);
    return Number.isFinite(value) && value > 0 ? Math.floor(value) : fallback;
  }

  // ---------- files ----------
  async function renderFiles(force = false) {
    setActions(`<button class="btn" data-action="refresh">刷新</button>`);
    if (state.files.loaded) renderFilesContent();
    if (!state.files.loaded || force) {
      if (!state.files.loaded) view.innerHTML = loadingCards(3);
      loadFiles().then(async () => {
        if (state.route !== "files") return;
        if (state.files.tab === "catalog" && !state.files.catalogLoaded) await loadRuleCatalog().catch(() => {});
        if (state.route === "files") renderFilesContent();
      }).catch((err) => {
        if (state.route === "files" && !state.files.loaded) view.innerHTML = emptyState("!", err.message || String(err));
      });
    }
    if (!state.timer) {
      state.timer = setInterval(async () => {
        if (state.route !== "files") return;
        try {
          const payload = await api("/api/admin/kazumi/downloads", { admin: true });
          state.files.tasks = payload.tasks || [];
          if (state.files.tab === "tasks") {
            const body = document.getElementById("files-body");
            if (body) {
              body.innerHTML = tasksTemplate(state.files.tasks);
              bindTaskGroups();
            } else renderFilesContent();
          }
        } catch (_) {}
      }, 5000);
    }
  }

  async function loadFiles() {
    const [downloads, files, rules, settings] = await Promise.all([
      api("/api/admin/kazumi/downloads", { admin: true }),
      api("/api/admin/kazumi/files", { admin: true }),
      api("/api/admin/kazumi/rules", { admin: true }),
      api("/api/admin/kazumi/settings", { admin: true }),
    ]);
    state.files.tasks = downloads.tasks || [];
    state.files.files = files.files || [];
    state.files.rules = rules.rules || [];
    state.files.settings = settings || {};
    state.files.loaded = true;
    state.mirrors = [...((settings || {}).bangumi_mirrors || [])];
    state.activeMirror = (settings || {}).bangumi_mirror || state.mirrors[0] || "";
  }

  function renderFilesContent() {
    const { tab, tasks, files, rules, catalog, settings } = state.files;
    view.innerHTML = `
      <div class="panel-head">
        <div class="chips">
          <button class="chip ${tab === "tasks" ? "active" : ""}" data-action="files-tab" data-tab="tasks">下载任务 <span class="badge muted">${tasks.length}</span></button>
          <button class="chip ${tab === "files" ? "active" : ""}" data-action="files-tab" data-tab="files">媒体文件 <span class="badge muted">${files.length}</span></button>
          <button class="chip ${tab === "installed" ? "active" : ""}" data-action="files-tab" data-tab="installed">已安装规则 <span class="badge muted">${rules.length}</span></button>
          <button class="chip ${tab === "catalog" ? "active" : ""}" data-action="files-tab" data-tab="catalog">规则仓库</button>
          <button class="chip ${tab === "network" ? "active" : ""}" data-action="files-tab" data-tab="network">网络设置</button>
          <button class="chip ${tab === "rules" ? "active" : ""}" data-action="files-tab" data-tab="rules">规则文件</button>
        </div>
      </div>
      <div id="files-body">${tab === "tasks" ? tasksTemplate(tasks)
        : tab === "files" ? filesTemplate(files)
        : tab === "installed" ? installedRulesTemplate(rules)
        : tab === "catalog" ? catalogTemplate(catalog)
        : tab === "network" ? networkSettingsTemplate(settings)
        : rulesTemplate(rules)}</div>
    `;
    if (tab === "catalog") bindCatalogFilter();
    if (tab === "tasks") bindTaskGroups();
  }

  function statusBadge(status) {
    switch (Number(status)) {
      case 1: return '<span class="badge warn"><span class="spin" style="width:10px;height:10px"></span> 下载中</span>';
      case 2: return '<span class="badge good">已完成</span>';
      case 3: return '<span class="badge bad">失败</span>';
      case 4: return '<span class="badge muted">已取消</span>';
      default: return '<span class="badge muted">等待中</span>';
    }
  }

  function taskStatusPriority(status) {
    const value = Number(status);
    if (value === 1 || value === 0) return 0;
    if (value === 3) return 1;
    if (value === 2) return 2;
    return 3;
  }

  function groupedTasks(tasks) {
    const groups = new Map();
    for (const task of tasks) {
      const key = task.name || task.display_name || "未知番剧";
      const title = task.display_name || task.name || "未知番剧";
      let group = groups.get(key);
      if (!group) {
        group = { key, title, cover: "", tasks: [], running: 0, queued: 0, done: 0, failed: 0, canceled: 0, size: 0, modified: 0 };
        groups.set(key, group);
      }
      if (!group.cover && task.cover) group.cover = task.cover;
      group.tasks.push(task);
      group.size += Number(task.size) || 0;
      group.modified = Math.max(group.modified, Number(task.updated_time) || 0);
      const status = Number(task.status);
      if (status === 1) group.running++;
      else if (status === 2) group.done++;
      else if (status === 3) group.failed++;
      else if (status === 4) group.canceled++;
      else group.queued++;
    }
    const out = Array.from(groups.values());
    for (const group of out) {
      group.active = group.running + group.queued + group.failed > 0;
      group.tasks.sort((a, b) => taskStatusPriority(a.status) - taskStatusPriority(b.status) || (Number(b.episode) || 0) - (Number(a.episode) || 0));
      group.latest = group.tasks.reduce((best, task) => ((Number(task.episode) || 0) >= (Number(best.episode) || 0) ? task : best), group.tasks[0]);
    }
    out.sort((a, b) => Number(b.active) - Number(a.active) || b.modified - a.modified || a.title.localeCompare(b.title));
    return out;
  }

  function bindTaskGroups() {
    $$("#files-body details.task-group").forEach((element) => {
      element.addEventListener("toggle", () => {
        state.files.taskGroupOpen[element.dataset.group] = element.open;
      });
    });
  }

  function tasksTemplate(tasks) {
    if (!tasks.length) return emptyState("⇣", "还没有 Kazumi 下载任务");
    const groups = groupedTasks(tasks);
    const counts = tasks.reduce((acc, task) => {
      const status = Number(task.status);
      if (status === 1) acc.running++;
      else if (status === 2) acc.done++;
      else if (status === 3) acc.failed++;
      else if (status === 4) acc.canceled++;
      else acc.queued++;
      return acc;
    }, { running: 0, queued: 0, done: 0, failed: 0, canceled: 0 });
    const failedCount = counts.failed;
    const actions = failedCount ? `
      <div class="toolbar" style="margin-bottom:12px">
        <button class="btn small" data-action="retry-failed-tasks">一键重试失败 (${failedCount})</button>
        <button class="btn small danger" data-action="delete-failed-tasks">一键删除失败 (${failedCount})</button>
      </div>` : "";
    const stats = `<div class="task-stats">
      ${counts.running ? `<span class="badge warn"><span class="spin" style="width:10px;height:10px"></span> 下载中 ${counts.running}</span>` : ""}
      ${counts.queued ? `<span class="badge muted">等待中 ${counts.queued}</span>` : ""}
      ${counts.done ? `<span class="badge good">已完成 ${counts.done}</span>` : ""}
      ${counts.failed ? `<span class="badge bad">失败 ${counts.failed}</span>` : ""}
      ${counts.canceled ? `<span class="badge muted">已取消 ${counts.canceled}</span>` : ""}
    </div>`;
    return `<div class="panel">${stats}${actions}<div class="task-groups">${groups.map((group) => {
      const cover = coverURL(group.cover);
      const open = typeof state.files.taskGroupOpen[group.key] === "boolean" ? state.files.taskGroupOpen[group.key] : group.active;
      return `
      <details class="task-group" data-group="${esc(group.key)}" ${open ? "open" : ""}>
        <summary class="task-group-head">
          ${cover ? `<img class="file-cover" loading="lazy" src="${esc(cover)}" alt="" referrerpolicy="no-referrer" onerror="this.style.display='none'" />` : `<div class="file-cover file-cover-fallback">${esc(String(group.title).slice(0, 1) || "⇣")}</div>`}
          <div class="grow">
            <div class="title">${esc(group.title)} <span class="badge muted">${group.tasks.length} 集</span></div>
            <div class="sub">${[
              group.running ? `下载中 ${group.running}` : "",
              group.queued ? `等待 ${group.queued}` : "",
              group.done ? `完成 ${group.done}` : "",
              group.failed ? `失败 ${group.failed}` : "",
              formatSize(group.size),
              formatTime(group.modified),
            ].filter(Boolean).join(" · ")}</div>
          </div>
          <span class="media-chevron" aria-hidden="true">▾</span>
        </summary>
        <div class="list task-episodes">${group.tasks.map((task) => {
      const running = Number(task.status) === 1 || Number(task.status) === 0;
      const speed = Number(task.speed) || 0;
      const size = Number(task.size) || 0;
      const episode = Number(task.episode) || 0;
      const progress = running ? `
        <div style="margin-top:8px">
          <div class="chips" style="gap:6px;margin-bottom:6px">
            <span class="badge warn">${Number(task.status) === 1 ? "下载中" : "等待中"}</span>
            <span class="badge muted">已下载 ${formatSize(size)}</span>
            ${speed > 0 ? `<span class="badge good">${formatSize(speed)}/s</span>` : `<span class="badge muted">正在连接/解析…</span>`}
          </div>
          <div class="progress"><i class="indeterminate"></i></div>
        </div>` : "";
      return `
          <div class="row task-row" style="align-items:flex-start">
            <span class="badge ${Number(task.status) === 3 ? "bad" : episode ? "muted" : "muted"}">${episode ? `EP ${episode}` : "EP ?"}</span>
            <div class="grow">
              <div class="title">${esc(task.title || task.name)}</div>
              <div class="sub">${esc(task.rule || "-")}${!running ? ` · 已下载 ${formatSize(size)}` : ""}${speed > 0 && !running ? ` · ${formatSize(speed)}/s` : ""} · ${esc(formatTime(task.updated_time))}</div>
              ${progress}
              ${task.error ? `<div class="sub" style="color:var(--bad);margin-top:6px" title="${esc(task.error)}">${esc(task.error)}</div>` : ""}
            </div>
            <div class="actions">
              ${statusBadge(task.status)}
              ${task.path && Number(task.status) === 2 ? `<button class="btn small" data-action="play-file" data-path="${esc(task.path)}" data-name="${esc(group.title)}${episode ? ` 第 ${episode} 集` : ""}">播放</button>` : ""}
              ${Number(task.status) <= 1
          ? `<button class="btn small danger" data-action="cancel-task" data-id="${task.id}">取消</button>`
          : `<button class="btn small" data-action="retry-task" data-id="${task.id}">重试</button><button class="btn small danger" data-action="delete-task" data-id="${task.id}">删除</button>`}
            </div>
          </div>`;
    }).join("")}</div>
      </details>`;
    }).join("")}</div></div>`;
  }

  function groupedMediaFiles(files) {
    const groups = new Map();
    for (const file of files) {
      const folder = String(file.path || "").split("/")[0] || "";
      const key = file.bangumi_name || file.display_name || folder || file.name || "未命名";
      const title = file.display_name || file.bangumi_name || folder || file.name || "未命名";
      let group = groups.get(key);
      if (!group) {
        group = { key, title, cover: "", files: [] };
        groups.set(key, group);
      }
      if (!group.cover && file.cover) group.cover = file.cover;
      group.files.push(file);
    }
    const out = Array.from(groups.values());
    for (const group of out) {
      group.files.sort((a, b) => (Number(a.episode) || 0) - (Number(b.episode) || 0) || String(a.name).localeCompare(String(b.name)));
      group.totalSize = group.files.reduce((sum, file) => sum + (Number(file.size) || 0), 0);
      group.modified = group.files.reduce((max, file) => Math.max(max, Number(file.modified) || 0), 0);
      group.latest = group.files.reduce((best, file) => ((Number(file.episode) || 0) >= (Number(best.episode) || 0) ? file : best), group.files[0]);
    }
    out.sort((a, b) => b.modified - a.modified || a.title.localeCompare(b.title));
    return out;
  }

  function filesTemplate(files) {
    if (!files.length) return emptyState("▤", "Kazumi 下载目录还是空的");
    const groups = groupedMediaFiles(files);
    return `<div class="panel"><div class="panel-head"><h2>媒体文件</h2><span class="badge muted">${groups.length} 部番剧</span><span class="badge muted">${files.length} 集</span><div class="spacer"></div><span class="muted small">同名番剧已合并，点击展开选集</span></div><div class="media-groups">${groups.map((group) => {
      const cover = coverURL(group.cover);
      const latestEpisode = Number(group.latest.episode) || 0;
      const fallback = esc(String(group.title).slice(0, 1) || "▤");
      return `
      <details class="media-group" data-group="${esc(group.key)}">
        <summary class="media-group-head">
          ${cover ? `<img class="file-cover" loading="lazy" src="${esc(cover)}" alt="" referrerpolicy="no-referrer" onerror="this.style.display='none'" />` : `<div class="file-cover file-cover-fallback">${fallback}</div>`}
          <div class="grow">
            <div class="title">${esc(group.title)} <span class="badge muted">${group.files.length} 集</span></div>
            <div class="sub">${latestEpisode ? `最新 EP ${latestEpisode} · ` : ""}${formatSize(group.totalSize)} · ${esc(formatTime(group.modified))} · 点击展开选集</div>
          </div>
          <span class="media-chevron" aria-hidden="true">▾</span>
        </summary>
        <div class="list media-episodes">${group.files.map((file) => {
          const episode = Number(file.episode) || 0;
          const episodeTitle = file.title || file.name;
          return `
          <div class="row file-row">
            <span class="badge ${episode ? "warn" : "muted"}">${episode ? `EP ${episode}` : "EP ?"}</span>
            <div class="grow">
              <div class="title">${esc(episodeTitle)}</div>
              <div class="sub">${formatSize(file.size)} · ${esc(formatTime(file.modified))} · ${esc(file.path)}</div>
            </div>
            <div class="actions">
              <button class="btn small" data-action="play-file" data-path="${esc(file.path)}" data-name="${esc(group.title)}${episode ? ` 第 ${episode} 集` : ""}">播放</button>
              <a class="btn small" href="/kazumi-files/${encodeURI(file.path)}" target="_blank" rel="noopener">打开</a>
              <button class="btn small danger" data-action="delete-file" data-path="${esc(file.path)}">删除</button>
            </div>
          </div>`;
        }).join("")}</div>
      </details>`;
    }).join("")}</div></div>`;
  }

  function rulesTemplate(rules) {
    if (!rules.length) return emptyState("K", "还没有安装 Kazumi 规则");
    return `<div class="panel"><div class="list">${rules.map((rule) => `
      <div class="row">
        <div class="grow"><div class="title">${esc(rule.name)} <span class="badge muted">${esc(rule.version || "-")}</span></div><div class="sub">${esc(rule.api || "")} · ${esc(formatTime(rule.updated_at))}</div></div>
        <div class="actions"><button class="btn small" data-action="kazumi-rule-file" data-name="${esc(rule.name)}">查看 / 编辑</button></div>
      </div>`).join("")}</div></div>`;
  }

  async function cancelTask(id) {
    await api(`/api/admin/kazumi/downloads/${id}`, { method: "DELETE", admin: true });
    toast("任务已取消", "good");
    await loadFiles();
    renderFilesContent();
  }

  async function retryTask(id) {
    await api(`/api/admin/kazumi/downloads/${id}/retry`, { method: "POST", admin: true });
    toast("已重新加入下载队列", "good");
    await loadFiles();
    renderFilesContent();
  }

  async function deleteTask(id) {
    if (!confirm("删除该任务及已下载文件？")) return;
    await api(`/api/admin/kazumi/downloads/${id}?delete=1`, { method: "DELETE", admin: true });
    toast("任务已删除", "good");
    await loadFiles();
    renderFilesContent();
  }

  async function retryFailedTasks() {
    const failed = (state.files.tasks || []).filter((task) => Number(task.status) === 3);
    if (!failed.length) return;
    showLoading(`正在重试 ${failed.length} 个失败任务…`);
    let ok = 0;
    try {
      for (const task of failed) {
        try {
          await api(`/api/admin/kazumi/downloads/${task.id}/retry`, { method: "POST", admin: true });
          ok += 1;
        } catch (_) {}
      }
    } finally {
      hideLoading();
    }
    toast(`已重试 ${ok}/${failed.length} 个失败任务`, ok ? "good" : "warn");
    await loadFiles();
    renderFilesContent();
  }

  async function deleteFailedTasks() {
    const failed = (state.files.tasks || []).filter((task) => Number(task.status) === 3);
    if (!failed.length) return;
    if (!confirm(`删除 ${failed.length} 个失败任务及其缓存文件？`)) return;
    showLoading(`正在删除 ${failed.length} 个失败任务…`);
    let ok = 0;
    try {
      for (const task of failed) {
        try {
          await api(`/api/admin/kazumi/downloads/${task.id}?delete=1`, { method: "DELETE", admin: true });
          ok += 1;
        } catch (_) {}
      }
    } finally {
      hideLoading();
    }
    toast(`已删除 ${ok}/${failed.length} 个失败任务`, ok ? "good" : "warn");
    await loadFiles();
    renderFilesContent();
  }

  async function refreshKazumiBrowseCache() {
    showLoading("正在启动 Kazumi 缓存更新…");
    try {
      const payload = await api("/api/admin/kazumi/browse/refresh", { method: "POST", admin: true });
      toast(payload.message || "已开始更新 Kazumi 缓存", "good");
      state.recommend.cache = {};
      state.recommend.categoriesLoaded = false;
      recommendCategoriesGeneration++;
      detailMemory.clear();
      // Keep showing the old cached content while the backend warms the new cache.
      const reload = () => {
        if (state.route === "timeline" || state.route === "recommend") renderRoute(false);
      };
      setTimeout(reload, 2500);
      setTimeout(() => {
        state.recommend.categoriesLoaded = false;
        reload();
      }, 9000);
      setTimeout(() => {
        state.recommend.categoriesLoaded = false;
        reload();
      }, 16000);
    } finally {
      hideLoading();
    }
  }

  async function clearKazumiBrowseCacheAction() {
    if (!confirm("清除 Kazumi 时间表/推荐的本地缓存？（不影响追番、订阅和下载）")) return;
    await api("/api/admin/kazumi/browse/cache", { method: "DELETE", admin: true });
    state.timeline.days = [];
    state.timeline.day = "";
    state.recommend.subjects = [];
    state.recommend.cache = {};
    state.recommend.categoriesLoaded = false;
    recommendCategoriesGeneration++;
    detailMemory.clear();
    toast("Kazumi 缓存已清除", "good");
    await renderRoute(true);
  }

  async function deleteFile(path) {
    if (!confirm(`删除文件 ${path}？`)) return;
    await api(`/api/admin/kazumi/files?path=${encodeURIComponent(path)}`, { method: "DELETE", admin: true });
    toast("文件已删除", "good");
    await loadFiles();
    renderFilesContent();
  }

  let editingRuleName = "";
  async function openRuleFile(name) {
    const payload = await api(`/api/admin/kazumi/rules/${encodeURIComponent(name)}/file`, { admin: true });
    editingRuleName = name;
    modalBody.innerHTML = `
      <div class="panel-head"><h2>${esc(name)}.json</h2><div class="spacer"></div><span class="muted small">可直接编辑并保存</span></div>
      <textarea id="rule-editor" spellcheck="false">${esc(payload.content || "")}</textarea>
      <div class="dialog-actions">
        <button class="btn ghost" type="button" id="rule-cancel">关闭</button>
        <button class="btn primary" data-action="save-rule-file">保存</button>
      </div>
    `;
    openDialog(modal);
    document.getElementById("rule-cancel").onclick = () => closeDialog(modal);
  }

  async function saveRuleFile() {
    const content = document.getElementById("rule-editor")?.value || "";
    try {
      JSON.parse(content);
    } catch (_) {
      throw new Error("JSON 格式不正确");
    }
    await api(`/api/admin/kazumi/rules/${encodeURIComponent(editingRuleName)}/file`, { method: "PUT", body: { content }, admin: true });
    toast("规则文件已保存", "good");
    closeDialog(modal);
  }

  // ---------- settings ----------
  async function renderSettings() {
    setActions("");
    view.innerHTML = `<div class="two-col">${loadingCards(2)}</div>`;
    Promise.allSettled([
      api("/api/admin/bangumi/sync", { admin: true }),
      api("/api/admin/webdav", { admin: true }),
      api("/api/admin/kazumi/settings", { admin: true }),
      api("/api/index/index"),
    ]).then(([syncRes, webdavRes, kazumiRes, indexRes]) => {
      if (state.route !== "settings") return;
      const sync = syncRes.status === "fulfilled" ? syncRes.value : {};
      const webdavStatus = webdavRes.status === "fulfilled" ? webdavRes.value : {};
      const kazumiSettings = kazumiRes.status === "fulfilled" ? kazumiRes.value : { bangumi_mirrors: [], bangumi_mirror: "" };
      const indexPayload = indexRes.status === "fulfilled" ? indexRes.value : {};
      state.version = indexPayload.version || state.version;
      state.mirrors = [...(kazumiSettings.bangumi_mirrors || state.mirrors || [])];
      state.activeMirror = kazumiSettings.bangumi_mirror || state.mirrors[0] || "";
      view.innerHTML = renderSettingsHTML(sync, webdavStatus, kazumiSettings);
    }).catch((err) => {
      if (state.route === "settings") view.innerHTML = emptyState("!", err.message || String(err));
    });
  }

  function renderSettingsHTML(sync = {}, webdavStatus = {}, kazumiSettings = {}) {
    return `
      <div class="two-col">
        <div>
          <section class="panel">
            <div class="panel-head"><h2>管理令牌</h2></div>
            <p class="muted small">用于调用管理 API。令牌只保存在浏览器 localStorage。</p>
            <div class="toolbar" style="margin-top:12px">
              <span class="badge ${state.token ? "good" : "bad"}">${state.token ? "已设置" : "未设置"}</span>
              <span class="muted small">${state.token ? "管理 API 可用" : "管理操作不可用"}</span>
              <div class="spacer" style="flex:1"></div>
              <button class="btn primary" data-action="open-token">修改令牌</button>
            </div>
          </section>

          <section class="panel" id="settings-sync-panel">
            <div class="panel-head"><h2>同步与镜像源</h2><span class="badge muted">通过按钮切换</span></div>
            <div class="chips" style="margin-bottom:16px">
              <button class="chip ${state.settingsTab === "bangumi" ? "active" : ""}" data-action="settings-tab" data-tab="bangumi">Bangumi 同步</button>
              <button class="chip ${state.settingsTab === "webdav" ? "active" : ""}" data-action="settings-tab" data-tab="webdav">WebDAV 同步</button>
              <button class="chip ${state.settingsTab === "mirror" ? "active" : ""}" data-action="settings-tab" data-tab="mirror">镜像源</button>
            </div>
            <div data-settings-pane="bangumi" class="${state.settingsTab === "bangumi" ? "" : "hidden"}">
              <div class="panel-head"><h3>Bangumi 追番同步</h3>${sync.token_configured ? '<span class="badge good">已连接</span>' : '<span class="badge muted">未连接</span>'}</div>
              <div class="list">
                <div class="row"><div class="grow"><div class="title">账号</div><div class="sub">${esc(sync.username || "未登录")}</div></div></div>
                <div class="row"><div class="grow"><div class="title">自动同步</div><div class="sub">开启后每次 update 前同步追番</div></div><label class="chip"><input id="bangumi-enabled" type="checkbox" ${sync.sync_enabled ? "checked" : ""} /> 启用</label></div>
                <div class="row"><div class="grow"><div class="title">同步分类</div><div class="sub">wish / watched / watching / on_hold / dropped</div></div><input id="bangumi-types" type="text" style="max-width:260px" value="${esc((sync.sync_types || []).join(","))}" /><button class="btn small" data-action="bangumi-sync-run">立即同步</button></div>
                <div class="row"><div class="grow"><div class="title">上次同步</div><div class="sub">${esc(formatTime(sync.last_sync))}</div></div><button class="btn small danger" data-action="bangumi-logout">退出</button></div>
              </div>
              <div class="divider"></div>
              <div class="toolbar">
                <div class="searchbox grow"><input id="bangumi-token" type="password" placeholder="Bangumi Access Token" /></div>
                <button class="btn primary" data-action="save-bangumi">保存</button>
              </div>
              <p class="muted small" style="margin-bottom:0">在 https://next.bgm.tv/demo/access-token 创建个人令牌。</p>
            </div>
            <div data-settings-pane="webdav" class="${state.settingsTab === "webdav" ? "" : "hidden"}">
              <div class="panel-head"><h3>WebDAV 同步</h3>${webdavStatus.enabled ? '<span class="badge good">已启用</span>' : '<span class="badge muted">未启用</span>'}</div>
              <div class="grid form-grid">
                <label class="field" style="grid-column:1/-1"><span>WebDAV 地址</span><input id="webdav-url" type="text" value="${esc(webdavStatus.url || "")}" placeholder="https://dav.example.com/remote.php/dav/files/user" /></label>
                <label class="field"><span>用户名</span><input id="webdav-user" type="text" value="${esc(webdavStatus.username || "")}" /></label>
                <label class="field"><span>密码 / 应用授权码</span><input id="webdav-pass" type="password" placeholder="${webdavStatus.password_set ? "已设置，留空不修改" : "密码"}" /></label>
                <label class="field" style="grid-column:1/-1"><span>同步方向</span><select id="webdav-direction">
                  <option value="download" ${(webdavStatus.sync_direction || "download") === "download" ? "selected" : ""}>云端 → 本地（默认）</option>
                  <option value="upload" ${webdavStatus.sync_direction === "upload" ? "selected" : ""}>本地 → 云端</option>
                </select></label>
              </div>
              <div class="chips" style="margin-top:12px">
                <label class="chip"><input id="webdav-enabled" type="checkbox" ${webdavStatus.enabled ? "checked" : ""} /> 启用同步</label>
                <label class="chip"><input id="webdav-collect" type="checkbox" ${webdavStatus.sync_collectibles !== false ? "checked" : ""} /> 同步收藏状态</label>
                <span class="muted small" style="align-self:center">上次同步：${esc(formatTime(webdavStatus.last_sync))}</span>
              </div>
              <div class="dialog-actions" style="justify-content:flex-start;margin-top:14px">
                <button class="btn primary" data-action="save-webdav">保存</button>
                <button class="btn" data-action="webdav-sync">立即同步</button>
              </div>
              <p class="muted small" style="margin-bottom:0">使用 Kazumi 相同的 <span class="mono">/kazumiSync</span> 目录保存 uani 收藏快照，可在多个 uani 实例间通用。</p>
            </div>
            <div data-settings-pane="mirror" class="${state.settingsTab === "mirror" ? "" : "hidden"}">
              <div class="panel-head"><h3>Bangumi 镜像</h3><span class="badge muted">可添加多个</span></div>
              <p class="muted small">搜索、吐槽和详情会优先使用当前镜像，并并发尝试列表中的地址。</p>
              <div class="list">${bangumiMirrorRows(kazumiSettings.bangumi_mirrors || [], kazumiSettings.bangumi_mirror || (kazumiSettings.bangumi_mirrors || [])[0] || "")}</div>
              <div class="toolbar" style="margin-top:12px">
                <div class="searchbox grow"><input id="bangumi-mirror-new" type="text" placeholder="https://next.bangumi.lat" /></div>
                <button class="btn" data-action="add-bangumi-mirror">添加</button>
                <button class="btn" data-action="use-bangumi-origin">原地址</button>
              </div>
              <p class="muted small" id="mirror-result" style="margin-bottom:0"></p>
            </div>
          </section>
        </div>

        <div>
          <section class="panel">
            <div class="panel-head"><h2>运行信息</h2></div>
            <div class="list">
              <div class="row"><div class="grow"><div class="title">版本</div><div class="sub">uani ${esc(state.version)}</div></div></div>
              <div class="row"><div class="grow"><div class="title">前端</div><div class="sub">内置 Web UI</div></div></div>
              <div class="row"><div class="grow"><div class="title">日历订阅</div><div class="sub"><a class="mono" href="/resource/calendar.ics" target="_blank">/resource/calendar.ics</a></div></div></div>
              <div class="row"><div class="grow"><div class="title">MCP</div><div class="sub"><span class="mono">/mcp</span> · <span class="mono">/mcp/sse</span></div></div></div>
              <div class="row"><div class="grow"><div class="title">缓存</div><div class="sub">导出 / 导入番剧详情、吐槽与 Kazumi 时间表/推荐缓存；清理只影响缓存，保留追番、订阅与下载信息</div></div><div class="actions"><button class="btn small" data-action="export-cache">导出缓存</button><button class="btn small" data-action="import-cache">导入缓存</button><button class="btn small danger" data-action="clear-detail-cache">清理缓存</button></div></div>
            </div>
          </section>
          <section class="panel">
            <div class="panel-head"><h2>Kazumi Files</h2></div>
            <p class="muted small">Kazumi 下载的剧集保存在 <span class="mono">save_path/kazumi</span>，可在 Files 页面播放、打开或删除。</p>
          </section>
        </div>
      </div>
    `;
  }

  async function saveBangumiSettings() {
    const token = document.getElementById("bangumi-token").value.trim();
    const enabled = document.getElementById("bangumi-enabled").checked;
    if (!token) throw new Error("请输入 Bangumi Access Token");
    const types = (document.getElementById("bangumi-types")?.value || "")
      .split(",").map((value) => value.trim()).filter(Boolean);
    await api("/api/admin/bangumi/token", { method: "POST", body: { access_token: token }, admin: true });
    await api("/api/admin/bangumi/sync", { method: "PATCH", body: { sync_enabled: enabled, ...(types.length ? { sync_types: types } : {}) }, admin: true });
    toast("Bangumi 账号已保存", "good");
    await renderSettings();
  }

  function setSettingsTab(tab) {
    if (!["bangumi", "webdav", "mirror"].includes(tab)) tab = "bangumi";
    state.settingsTab = tab;
    $$("[data-action=\"settings-tab\"]").forEach((button) => button.classList.toggle("active", button.dataset.tab === tab));
    $$("[data-settings-pane]").forEach((pane) => pane.classList.toggle("hidden", pane.dataset.settingsPane !== tab));
  }

  function bangumiMirrorRows(mirrors, active) {
    if (!(mirrors || []).length) return '<div class="muted small">当前使用默认镜像</div>';
    return mirrors.map((url) => {
      const isActive = url === active;
      return `<div class="row ${isActive ? "mirror-active" : ""}"><div class="grow"><div class="title mono">${esc(url)}</div><div class="sub">${isActive ? "当前使用" : "切换后优先使用此镜像"}</div></div><div class="actions">${isActive ? '<span class="badge good">当前使用</span>' : `<button class="btn small" data-action="use-bangumi-mirror" data-url="${esc(url)}">切换</button>`}<button class="btn small" data-action="test-bangumi-mirror" data-url="${esc(url)}">测试</button><button class="btn small danger" data-action="remove-bangumi-mirror" data-url="${esc(url)}">删除</button></div></div>`;
    }).join("");
  }

  async function setActiveBangumiMirror(url) {
    if (!url) throw new Error("镜像地址不能为空");
    const payload = await api("/api/admin/kazumi/settings", { method: "PATCH", body: { bangumi_mirror: url }, admin: true });
    state.activeMirror = payload.bangumi_mirror || url;
    toast("已切换 Bangumi 镜像", "good");
    if (state.route === "files") await refreshFiles("network");
    else if (state.route === "settings") await renderSettings();
  }

  async function persistBangumiMirrors() {
    const payload = await api("/api/admin/kazumi/settings", { method: "PATCH", body: { bangumi_mirrors: state.mirrors }, admin: true });
    state.mirrors = [...(payload.bangumi_mirrors || [])];
    state.activeMirror = payload.bangumi_mirror || state.mirrors[0] || "";
    if (state.route === "files") {
      await refreshFiles("network");
    } else if (state.route === "settings") {
      await renderSettings();
    }
  }

  async function addBangumiMirror(value) {
    const input = document.getElementById("bangumi-mirror-new");
    const url = String(value || (input && input.value) || "").trim();
    if (!url) throw new Error("请输入镜像地址");
    if (!/^https?:\/\//i.test(url)) throw new Error("镜像地址必须以 http:// 或 https:// 开头");
    if (!state.mirrors.includes(url)) state.mirrors.push(url);
    await persistBangumiMirrors();
    toast("镜像已添加", "good");
  }

  async function removeBangumiMirror(url) {
    state.mirrors = state.mirrors.filter((item) => item !== url);
    await persistBangumiMirrors();
    toast("镜像已删除", "good");
  }

  async function testBangumiMirror(url) {
    const input = document.getElementById("bangumi-mirror-new");
    const value = String(url || (input && input.value) || "").trim();
    const result = document.getElementById("mirror-result");
    if (result) result.textContent = "正在测试…";
    try {
      const payload = await api("/api/admin/kazumi/test-mirror", { method: "POST", body: { url: value }, admin: true });
      if (result) {
        if (payload.ok) {
          result.textContent = payload.warning
            ? `可用 · ${payload.latency_ms} ms（${payload.warning}）`
            : `可用 · ${payload.latency_ms} ms`;
        } else {
          result.textContent = `不可用：${payload.message}`;
        }
      }
    } catch (err) {
      if (result) result.textContent = `测试失败：${err.message}`;
    }
  }

  async function saveWebDAV() {
    const password = document.getElementById("webdav-pass").value;
    const body = {
      url: document.getElementById("webdav-url").value.trim(),
      username: document.getElementById("webdav-user").value,
      enabled: document.getElementById("webdav-enabled").checked,
      sync_collectibles: document.getElementById("webdav-collect").checked,
      sync_direction: document.getElementById("webdav-direction").value,
    };
    if (password) body.password = password;
    await api("/api/admin/webdav", { method: "PATCH", body, admin: true });
    toast("WebDAV 设置已保存", "good");
    await renderSettings();
  }

  async function syncWebDAV() {
    showLoading("正在同步 WebDAV…");
    try {
      const direction = document.getElementById("webdav-direction").value;
      const payload = await api("/api/admin/webdav/sync", { method: "POST", body: { direction }, admin: true });
      toast(payload.message || "WebDAV 同步完成", "good");
      await renderSettings();
    } finally {
      hideLoading();
    }
  }

  async function runBangumiSync() {
    toast("正在同步 Bangumi 追番…");
    const payload = await api("/api/admin/bangumi/sync", { method: "POST", body: {}, admin: true });
    const summary = payload.summary || {};
    toast(`同步完成：新增 ${summary.added || 0}，已存在 ${summary.existing || 0}`, "good");
  }

  async function bangumiLogout() {
    if (!confirm("退出 Bangumi 账号并关闭自动同步？")) return;
    await api("/api/admin/bangumi/token", { method: "DELETE", admin: true });
    toast("已退出 Bangumi", "good");
    await renderSettings();
  }

  // ---------- token ----------
  function updateTokenChip() {
    const dot = document.getElementById("token-dot");
    const label = document.getElementById("token-label");
    if (state.token) { dot.classList.add("ok"); label.textContent = "管理令牌已设置"; }
    else { dot.classList.remove("ok"); label.textContent = "未设置管理令牌"; }
  }

  function openTokenDialog() {
    tokenInput.value = state.token;
    openDialog(tokenDialog);
  }

  document.getElementById("modal-close").addEventListener("click", () => closeDialog(modal));
  document.getElementById("notice-ok").addEventListener("click", () => closeDialog(noticeDialog));
  document.getElementById("image-close").addEventListener("click", () => closeDialog(imageDialog));
  imageDialog.addEventListener("click", (event) => {
    if (event.target === imageDialog) closeDialog(imageDialog);
  });
  modal.addEventListener("close", () => { stopVideoPlayback(); modalBody.classList.remove("player-mode"); });
  document.getElementById("token-chip").addEventListener("click", openTokenDialog);
  document.getElementById("token-cancel").addEventListener("click", () => closeDialog(tokenDialog));
  document.getElementById("token-form").addEventListener("submit", (event) => {
    event.preventDefault();
    state.token = tokenInput.value.trim();
    if (state.token) localStorage.setItem("uani_token", state.token);
    else localStorage.removeItem("uani_token");
    updateTokenChip();
    closeDialog(tokenDialog);
    toast("管理令牌已保存", "good");
    renderRoute(true);
  });

  document.getElementById("menu-toggle").addEventListener("click", () => setSidebarOpen(!sidebar.classList.contains("open")));
  const sidebarScrim = document.getElementById("sidebar-scrim");
  if (sidebarScrim) sidebarScrim.addEventListener("click", () => setSidebarOpen(false));
  window.addEventListener("resize", () => {
    if (window.innerWidth > 900) setSidebarOpen(false);
  });
  document.addEventListener("click", (event) => {
    if (window.innerWidth <= 900 && sidebar.classList.contains("open") && !sidebar.contains(event.target) && event.target.id !== "menu-toggle") {
      setSidebarOpen(false);
    }
  });

  window.addEventListener("hashchange", () => renderRoute());
  window.addEventListener("DOMContentLoaded", () => {
    updateTokenChip();
    if (!location.hash) location.hash = "#/subscriptions";
    renderRoute();
  });
})();
