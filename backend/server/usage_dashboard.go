package server

const usageDashboardHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Elysia API Usage Dashboard</title>
  <style>

    :root {

      color-scheme: light;
      --bg: #fff5f7;
      --panel: #ffffff;
      --panel-2: #ffeef2;
      --text: #350014;
      --muted: #9d5d78;
      --border: #ffd1dc;
      --primary: #db2777;
      --primary-text: #ffffff;
      --danger: #e11d48;
      --ok: #059669;
      --shadow: 0 10px 30px rgba(219, 39, 119, .08);
    }
    [data-theme="dark"] {
      color-scheme: dark;
      --bg: #10070b;
      --panel: #180d13;
      --panel-2: #25141d;
      --text: #fce7f3;
      --muted: #c0a9b6;
      --border: #4a2135;
      --primary: #f472b6;
      --primary-text: #10070b;
      --danger: #fda4af;
      --ok: #34d399;
      --shadow: none;
    }
    * { box-sizing: border-box; }
    body { margin: 0; background: var(--bg); color: var(--text); font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    header { padding: 22px 24px; border-bottom: 1px solid var(--border); background: var(--panel); position: sticky; top: 0; z-index: 2; }
    h1 { margin: 0; font-size: 24px; }
    h2 { margin: 0 0 14px; font-size: 18px; }
    h3 { margin: 14px 0 8px; font-size: 14px; }
    main { padding: 24px; display: grid; gap: 18px; }
    .topline { display: flex; justify-content: space-between; gap: 16px; align-items: center; }
    .login { min-height: calc(100vh - 90px); display: grid; place-items: center; padding: 24px; }
    .login-card { width: min(480px, 100%); background: var(--panel); border: 1px solid var(--border); border-radius: 16px; padding: 24px; box-shadow: var(--shadow); }
    .login-card p { color: var(--muted); line-height: 1.7; }
    .controls { display: flex; justify-content: space-between; gap: 12px; margin-top: 18px; align-items: end; flex-wrap: wrap; }
    .global-timebar { display: flex; flex-wrap: wrap; gap: 8px; align-items: end; justify-content: flex-end; margin-top: 0; margin-left: auto; padding: 0; border: 0; border-radius: 0; background: transparent; }
    .global-timebar label { min-width: 170px; }
    .global-timebar input, .global-timebar button { padding: 8px 10px; }
    .range-dropdown { position: relative; }
    .range-dropdown > button { background: var(--panel); color: var(--text); border-color: var(--border); font-weight: 700; min-height: 38px; min-width: 120px; font-size: 14px; padding: 8px 12px; }
    .range-dropdown-menu { display: none; position: absolute; z-index: 4; top: 100%; left: 0; width: 120px; overflow: auto; padding: 6px; border: 1px solid var(--border); border-radius: 12px; background: var(--panel); box-shadow: var(--shadow); gap: 4px; }
    .range-dropdown.hover .range-dropdown-menu, .range-dropdown:hover .range-dropdown-menu { display: grid; }
    .range-dropdown-menu button { background: transparent; border: 0; color: var(--text); font-size: 14px; padding: 8px 12px; text-align: left; font-weight: 700; border-radius: 8px; width: 100%; }
    .range-dropdown-menu button:hover { background: var(--panel-2); }
    label { display: grid; gap: 6px; color: var(--muted); font-size: 12px; }
    input, select, button { border: 1px solid var(--border); background: var(--panel); color: var(--text); border-radius: 10px; padding: 10px 12px; font: inherit; }
    button { cursor: pointer; background: var(--primary); color: var(--primary-text); border-color: var(--primary); font-weight: 700; }
    button.secondary { background: var(--panel-2); color: var(--text); border-color: var(--border); }
    .theme-switch { position: relative; width: 78px; height: 38px; padding: 3px; display: inline-flex; align-items: center; background: linear-gradient(180deg, #f472b6 0%, #fce7f3 100%); color: var(--text); border: 1px solid var(--border); border-radius: 999px; overflow: hidden; transition: background-color .24s ease, border-color .24s ease, box-shadow .22s ease; }
    .theme-switch:hover { border-color: var(--primary); box-shadow: 0 0 0 3px color-mix(in srgb, var(--primary) 18%, transparent); }
    .theme-switch:focus-visible { outline: 2px solid var(--primary); outline-offset: 2px; }
    .theme-switch-scene { position: absolute; inset: 0; opacity: 0; transition: opacity .24s ease; }
    .theme-switch-scene.light { opacity: 1; background: linear-gradient(180deg, #f472b6 0%, #fce7f3 100%); }
    .theme-switch-scene.light::before { content: ''; position: absolute; left: 33px; right: 6px; bottom: 5px; height: 13px; border-radius: 999px; background: rgba(255,255,255,.78); box-shadow: -13px 4px 0 -2px rgba(255,255,255,.72), 8px -4px 0 -3px rgba(255,255,255,.62); }
    .theme-switch-scene.dark { background: #180d13; }
    .theme-switch.is-dark .theme-switch-scene.light { opacity: 0; }
    .theme-switch.is-dark .theme-switch-scene.dark { opacity: 1; }
    .theme-switch-thumb { position: absolute; z-index: 2; top: 4px; left: 4px; width: 30px; height: 30px; border-radius: 999px; background: rgba(255,255,255,.78); border: 1px solid rgba(255,255,255,.9); box-shadow: 0 2px 7px rgba(15, 23, 42, .25); transition: transform .26s cubic-bezier(.2,.8,.2,1), background-color .22s ease; }
    .theme-switch.is-dark .theme-switch-thumb { transform: translateX(40px); background: rgba(24,13,19,.55); border-color: rgba(192,169,182,.7); }
    .sun { position: absolute; z-index: 1; left: 10px; top: 8px; width: 16px; height: 16px; border-radius: 999px; background: radial-gradient(circle at 35% 30%, #ffffff 0%, #fffbeb 18%, #fef08a 44%, #eab308 100%); box-shadow: 0 0 5px #fff, 0 0 12px #fde047, 0 0 20px #eab308, 0 0 0 4px rgba(254,240,138,.46); filter: saturate(1.25); }
    .cloud, .cloud::before, .cloud::after { position: absolute; display: block; background: rgba(255,255,255,.95); border-radius: 999px; content: ''; }
    .cloud { left: 39px; top: 18px; width: 26px; height: 8px; box-shadow: -7px 2px 0 -2px rgba(255,255,255,.78); }
    .cloud::before { left: 3px; top: -6px; width: 11px; height: 11px; }
    .cloud::after { left: 12px; top: -8px; width: 15px; height: 15px; }
    .moon { position: absolute; right: 11px; top: 8px; width: 17px; height: 17px; border-radius: 999px; background: #fde68a; box-shadow: -5px 0 0 #180d13 inset; }
    .star { position: absolute; width: 3px; height: 3px; border-radius: 999px; background: #e0f2fe; }
    .star.s1 { left: 14px; top: 9px; } .star.s2 { left: 27px; top: 22px; } .star.s3 { left: 45px; top: 12px; }
    .tabs { display: flex; flex-wrap: wrap; gap: 8px; flex: 1; }
    .tab { background: var(--panel); color: var(--text); border: 1px solid var(--border); }
    .tab.active { background: var(--primary); color: var(--primary-text); border-color: var(--primary); }
    .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 14px; }
    @media (min-width: 1500px) { .cards { grid-template-columns: repeat(8, minmax(0, 1fr)); } }
    .card, section { background: var(--panel); border: 1px solid var(--border); border-radius: 16px; box-shadow: var(--shadow); }
    .card { padding: 14px; }
    .card .label { color: var(--muted); font-size: 12px; }
    .card .value { font-size: 22px; font-weight: 800; margin-top: 8px; }
    .overview-card { position: relative; aspect-ratio: 1 / 1; min-width: 0; overflow: hidden; padding: 18px; transition: transform .25s ease, border-color .25s ease; }
    .overview-card:hover { transform: translateY(-2px); border-color: var(--primary); }
    .overview-bg { position: absolute; right: 10px; bottom: -20px; font-size: clamp(52px, 8vw, 96px); line-height: 1; font-weight: 900; color: var(--primary); opacity: .10; pointer-events: none; transition: transform .32s ease, font-size .32s ease, opacity .32s ease; }
    .overview-card.last-call-card .overview-bg { left: 12px; right: 12px; bottom: -8px; font-size: clamp(54px, 7vw, 84px); letter-spacing: -.08em; text-align: center; white-space: normal; overflow-wrap: anywhere; }
    .overview-card:hover .overview-bg { transform: translateY(-10px) scale(.7); opacity: 0; }
    .overview-main { position: relative; z-index: 1; height: 100%; display: flex; flex-direction: column; justify-content: space-between; transition: transform .3s ease, opacity .25s ease; }
    .overview-card:hover .overview-main { transform: translateY(-42%) scale(.58); opacity: 0; }
    .overview-card.last-call-card .overview-main { justify-content: space-between; }
    .overview-card.last-call-card .overview-value { font-size: clamp(20px, 2.8vw, 31px); letter-spacing: -.02em; line-height: 1.15; margin-top: auto; margin-bottom: auto; }
    .overview-label { color: var(--muted); font-size: 14px; font-weight: 800; margin-bottom: auto; margin-top: 14px; }
    .overview-value { font-size: clamp(24px, 3.2vw, 46px); font-weight: 900; margin-top: auto; margin-bottom: auto; letter-spacing: -.04em; word-break: break-all; overflow: hidden; }
    .overview-note { color: var(--muted); margin-top: 0; font-size: 0; }
    .overview-compact { position: absolute; z-index: 3; top: 14px; left: 16px; right: 16px; display: flex; align-items: baseline; gap: 6px; opacity: 0; transform: translateY(10px); transition: opacity .24s ease .08s, transform .28s ease .05s; white-space: nowrap; }
    .overview-card:hover .overview-compact { opacity: 1; transform: translateY(0); }
    .overview-compact span { color: var(--muted); font-size: 15px; font-weight: 800; }
    .overview-compact b { color: var(--text); font-size: 15px; font-weight: 900; overflow: hidden; text-overflow: ellipsis; }
    .overview-reveal { position: absolute; z-index: 2; left: 0; right: 0; bottom: 0; height: calc(100% - 42px); padding: 16px 16px 26px; background: color-mix(in srgb, var(--panel-2) 94%, var(--primary) 6%); border-top: 1px solid var(--border); transform: translateY(100%); transition: transform .32s cubic-bezier(.2,.8,.2,1); }
    .overview-card:hover .overview-reveal { transform: translateY(0); }
    .overview-reveal-row { display: flex; justify-content: space-between; gap: 12px; border-bottom: 1px dashed var(--border); padding: 6px 0; font-size: 12px; }
    .overview-reveal-row span { color: var(--muted); }
    .overview-reveal-row b { text-align: right; }
    .overview-page-window { overflow: hidden; height: calc(100% - 16px); }
    .overview-pages { display: flex; width: 200%; height: 100%; transition: transform .28s cubic-bezier(.2,.8,.2,1); }
    .overview-card.page-2 .overview-pages { transform: translateX(-50%); }
    .overview-page { flex: 0 0 50%; width: 50%; box-sizing: border-box; padding-right: 10px; }
    .overview-dots { position: absolute; left: 0; right: 0; bottom: 8px; display: flex; justify-content: center; gap: 8px; }
    .overview-dot { width: 7px; height: 7px; border-radius: 999px; background: var(--muted); opacity: .45; border: 0; padding: 0; }
    .overview-dot:hover, .overview-card:not(.page-2) .overview-dot.page-1, .overview-card.page-2 .overview-dot.page-2 { background: var(--primary); opacity: 1; }
    .card.token-card { cursor: help; }
    .card.help-card { cursor: help; }
    .card.token-card:hover { border-color: var(--primary); }
    section { padding: 18px; overflow: auto; }
    table { width: 100%; border-collapse: collapse; font-size: 13px; }
    th, td { text-align: left; padding: 10px 8px; border-bottom: 1px solid var(--border); white-space: nowrap; }
    th { color: var(--primary); font-weight: 800; }
    .table-sort { padding: 0; border: 0; background: transparent; color: var(--primary); font: inherit; font-weight: 800; }
    tr:hover td { background: var(--panel-2); }
    .muted { color: var(--muted); }
    .error { color: var(--danger); }
    .ok { color: var(--ok); }
    .hidden { display: none !important; }
    .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 18px; }
    .overview-stack { display: grid; gap: 20px; }
    .chart { position: relative; width: 100%; min-height: 280px; border: 1px solid var(--border); border-radius: 14px; background: var(--panel-2); padding: 12px; overflow: hidden; }
    .chart-tooltip { position: absolute; pointer-events: none; display: none; background: var(--panel); color: var(--text); border: 1px solid var(--border); border-radius: 10px; padding: 8px 10px; box-shadow: var(--shadow); font-size: 12px; z-index: 1; min-width: 150px; }
    svg { width: 100%; height: 260px; display: block; }
    .legend { display: flex; flex-wrap: wrap; gap: 12px; color: var(--muted); font-size: 12px; margin-top: 10px; }
    .legend-item { display: inline-flex; align-items: center; gap: 6px; padding: 4px 8px; border: 1px solid var(--border); background: var(--panel); color: var(--text); border-radius: 999px; font-size: 12px; font-weight: 700; }
    .legend-item.disabled { opacity: .45; text-decoration: line-through; }
    .legend-item.disabled .dot { background: transparent !important; border: 1px solid currentColor; }
    .time-toolbar { display: flex; flex-wrap: wrap; gap: 8px; align-items: end; justify-content: flex-end; }
    .time-toolbar label { min-width: 180px; }
    .time-toolbar input { padding: 8px 10px; }
    .time-toolbar button { padding: 8px 10px; }
    .log-page-size { display: grid; margin-bottom: 0; min-width: 0; width: 264px; align-self: end; gap: 2px; font-size: 11px; line-height: 1.1; }
    .log-page-size select { padding: 8px 10px; width: 100%; height: 38px; min-height: 38px; }
    .filter-pane { display: grid; grid-template-columns: repeat(5, minmax(192px, 256px)) auto minmax(180px, 264px); justify-content: start; width: 100%; gap: 8px; align-items: end; margin-bottom: 12px; padding: 12px; border: 1px solid var(--border); border-radius: 14px; background: var(--panel-2); }
    .filter-pane input, .filter-pane select, .filter-pane button { padding: 8px 10px; min-height: 38px; }
    .filter-actions { display: flex; flex-wrap: nowrap; gap: 8px; }
    .filter-actions button { white-space: nowrap; }
    .multi-select { position: relative; min-width: 0; }
    .multi-select > button { width: 100%; background: var(--panel); color: var(--text); border-color: var(--border); text-align: left; font-weight: 700; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    .multi-select-menu { display: none; position: absolute; z-index: 4; top: 100%; left: 0; width: min(360px, 88vw); max-height: 250px; overflow: auto; padding: 6px; border: 1px solid var(--border); border-radius: 12px; background: var(--panel); box-shadow: var(--shadow); }
    .multi-select.open .multi-select-menu, .multi-select.hover .multi-select-menu { display: grid; gap: 4px; }
    .multi-select-menu input[type="search"] { width: 100%; height: 24px; min-height: 24px; padding: 2px 6px; font-size: 12px; }
    .multi-select-menu input[type="search"]::placeholder { font-size: 11px; }
    .multi-select-menu .filter-actions button { padding: 2px 4px; font-size: 11px; border-radius: 6px; }
    .multi-option { display: flex; gap: 4px; align-items: center; padding: 2px 4px; border-radius: 6px; color: var(--text); font-size: 12px; }
    .multi-option:hover { background: var(--panel-2); }
    .multi-option input { padding: 0; }
    .log-summary { margin: 8px 0 12px; }
    .log-pagination { align-items: center; margin-top: 12px; }
    .log-pagination button { padding: 4px 8px; min-height: 28px; font-size: 12px; border-radius: 8px; }
    .log-pagination input { width: 64px; padding: 4px 7px; min-height: 28px; font-size: 12px; border-radius: 8px; }
    .dot { display: inline-block; width: 10px; height: 10px; border-radius: 99px; margin-right: 2px; }
    dialog { width: min(1100px, 92vw); max-height: 88vh; border: 1px solid var(--border); border-radius: 16px; background: var(--panel); color: var(--text); }
    dialog::backdrop { background: rgba(15, 23, 42, .45); }
    .json-tree { background: var(--panel-2); border: 1px solid var(--border); border-radius: 12px; padding: 12px; max-height: 360px; overflow: auto; font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: 12px; }
    .json-tree details { margin-left: 14px; }
    .json-tree summary { cursor: pointer; color: var(--primary); font-weight: 800; }
    .json-row { margin-left: 16px; line-height: 1.7; }
    .json-key { color: var(--primary); font-weight: 800; }
    .json-string { color: var(--ok); }
    .json-number { color: #a855f7; }
    .json-bool { color: #fb923c; }
    .json-null { color: var(--muted); font-style: italic; }
    .protocol-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(240px, 1fr)); gap: 10px; margin: 8px 0 12px; }
    .protocol-card { background: var(--panel-2); border: 1px solid var(--border); border-radius: 12px; padding: 10px; }
    .protocol-card strong { display: block; color: var(--primary); margin-bottom: 6px; }
    .protocol-list { display: grid; gap: 4px; font-size: 12px; }
    .protocol-list div { display: flex; justify-content: space-between; gap: 12px; border-bottom: 1px dashed var(--border); padding-bottom: 3px; }
    .protocol-list span:first-child { color: var(--muted); }
    .raw-json { margin-top: 10px; }
    .detail-section { margin-top: 18px; }
    .detail-title { display: flex; align-items: center; gap: 8px; margin: 18px 0 10px; }
    .detail-title h3 { margin: 0; }
    .detail-summary { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; margin-bottom: 12px; align-items: stretch; }
    .detail-metric { background: var(--panel-2); border: 1px solid var(--border); border-radius: 12px; padding: 12px; }
    .detail-metric span { display: block; color: var(--muted); font-size: 12px; margin-bottom: 6px; }
    .detail-metric b { font-size: 16px; word-break: break-word; }
    .status-pill { display: inline-flex; align-items: center; gap: 6px; border-radius: 999px; padding: 4px 9px; font-size: 12px; font-weight: 800; border: 1px solid var(--border); background: var(--panel-2); }
    .status-pill.ok { color: var(--ok); }
    .status-pill.error { color: var(--danger); }
    .body-card { border: 1px solid var(--border); border-radius: 14px; padding: 12px; background: var(--panel); margin-bottom: 12px; }
    .body-card > h4 { margin: 0 0 10px; color: var(--primary); }
    pre { white-space: pre-wrap; word-break: break-word; background: var(--panel-2); border-radius: 12px; padding: 12px; border: 1px solid var(--border); max-height: 280px; overflow: auto; }
    @media (max-width: 1200px) { .controls, .cards { grid-template-columns: repeat(2, minmax(0, 1fr)); } .grid { grid-template-columns: 1fr; } .log-controls-row { grid-template-columns: 1fr; } .filter-pane { grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); } .filter-actions { flex-wrap: wrap; } .log-page-size { width: 132px; } .detail-summary { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
    @media (max-width: 760px) { .detail-summary { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
    @media (max-width: 680px) { .cards { grid-template-columns: 1fr; } .global-timebar label { min-width: 100%; } }
    @media (max-width: 520px) { .detail-summary { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <div id="login" class="login">
    <div class="login-card">
      <div class="topline">
        <h1>Elysia API Usage</h1>
        <button id="themeLogin" class="theme-switch" type="button" aria-label="切换主题" aria-pressed="false"><span class="theme-switch-scene light"><span class="sun"></span><span class="cloud"></span></span><span class="theme-switch-scene dark"><span class="moon"></span><span class="star s1"></span><span class="star s2"></span><span class="star s3"></span></span><span class="theme-switch-thumb"></span></button>
      </div>
      <p>请输入 Usage 面板访问令牌后进入统计面板。这个令牌只用于查看统计，不是上游模型 API key；业务调用方 access token 会作为统计维度展示。</p>
      <label>面板访问令牌 <input id="panelToken" type="password" autocomplete="off" placeholder="panel access token" /></label>
      <div style="display:flex; gap:10px; margin-top:14px; align-items:center">
        <button id="enter" type="button">进入面板</button>
        <span id="loginStatus" class="muted"></span>
      </div>
    </div>
  </div>

  <div id="app" class="hidden">
    <header>
      <div class="topline">
        <h1>Elysia API 用量统计面板</h1>
        <div style="display:flex; gap:8px; align-items:center">
          <button id="refresh" type="button">刷新</button>
          <button id="theme" class="theme-switch" type="button" aria-label="切换主题" aria-pressed="false"><span class="theme-switch-scene light"><span class="sun"></span><span class="cloud"></span></span><span class="theme-switch-scene dark"><span class="moon"></span><span class="star s1"></span><span class="star s2"></span><span class="star s3"></span></span><span class="theme-switch-thumb"></span></button>
          <button id="logout" class="secondary" type="button">退出</button>
        </div>
      </div>
      <div class="controls">
        <div class="tabs" id="tabs"></div>
        <div class="global-timebar" id="globalTimebar"></div>
      </div>
    </header>
    <main>
      <div id="status" class="muted"></div>
      <div id="content"></div>
    </main>
  </div>

  <dialog id="detail"><form method="dialog"><button class="secondary" style="float:right">关闭</button></form><h2>请求详情</h2><div id="detailBody"></div></dialog>
<script>
const $ = id => document.getElementById(id)
const state = { token: localStorage.getItem('elysiaPanelToken') || '', stats: null, overviewStats: null, logs: null, view: 'overview', customFrom: '', customTo: '', sort: { table: '', index: -1, dir: 'desc' }, logFilters: { keyNames: [], groupNames: [], modelNames: [], stream: '', status: '' }, logPage: 1, logPageSize: '50', activeRangeText: '最近 24h', overviewVisibleMetrics: { requests: true, totalTokens: true, inputTokens: true, outputTokens: true }, timeVisibleMetrics: { requests: true, totalTokens: true, inputTokens: true, outputTokens: true, cacheHitTokens: true } }
const views = [
  ['overview', '总览'], ['time', '按时间'], ['caller', '按调用方'], ['group', '按模型组'], ['model', '按具体模型'], ['logs', '请求日志']
]
const colors = ['#ec4899', '#10b981', '#f59e0b', '#8b5cf6']
function applyTheme(theme) { document.documentElement.dataset.theme = theme; localStorage.setItem('elysiaUsageTheme', theme); syncThemeSwitches(theme) }
function syncThemeSwitches(theme) {
  const isDark = theme === 'dark'
  for (const id of ['theme', 'themeLogin']) {
    const el = $(id)
    if (!el) continue
    el.classList.toggle('is-dark', isDark)
    el.setAttribute('aria-pressed', String(isDark))
    el.title = isDark ? '切换到浅色模式' : '切换到深色模式'
  }
}
function initTheme() { applyTheme(localStorage.getItem('elysiaUsageTheme') || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')) }
function toggleTheme() { applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark') }
$('theme').onclick = toggleTheme
$('themeLogin').onclick = toggleTheme
$('panelToken').value = state.token
$('enter').onclick = enter
$('logout').onclick = () => { localStorage.removeItem('elysiaPanelToken'); state.token = ''; $('app').classList.add('hidden'); $('login').classList.remove('hidden') }
$('refresh').onclick = refresh
initTheme()
renderTabs()
if (state.token) enter()
function authHeaders() { return state.token ? { Authorization: 'Bearer ' + state.token } : {} }
function defaultRange() {
  const to = new Date(); const from = new Date(to); from.setDate(from.getDate() - 1)
  return { from: from.toISOString(), to: to.toISOString() }
}
function range() {
  if (state.customFrom && state.customTo) {
    return { from: new Date(state.customFrom).toISOString(), to: new Date(state.customTo).toISOString() }
  }
  return defaultRange()
}
function qs(extra = {}) {
  const r = range(); const params = new URLSearchParams({ from: r.from, to: r.to, window: 'auto', ...extra })
  return params.toString()
}
function defaultQs(extra = {}) {
  const r = defaultRange(); const params = new URLSearchParams({ from: r.from, to: r.to, window: 'auto', ...extra })
  return params.toString()
}
function logQs(extra = {}) {
  const r = range(); const params = new URLSearchParams({ from: r.from, to: r.to })
  const filters = state.logFilters
  const pageSize = Number(state.logPageSize || 50) || 50
  const page = Math.max(1, Number(state.logPage || 1) || 1)
  for (const value of filters.keyNames || []) params.append('keyName', value)
  for (const value of filters.groupNames || []) params.append('groupName', value)
  for (const value of filters.modelNames || []) params.append('modelName', value)
  for (const key of ['stream', 'status']) {
    if (filters[key]) params.set(key, filters[key])
  }
  params.set('limit', String(pageSize))
  params.set('offset', String((page - 1) * pageSize))
  for (const [key, value] of Object.entries(extra)) params.set(key, value)
  return params.toString()
}
async function getJSON(path) {
  const res = await fetch(path, { headers: authHeaders() })
  if (!res.ok) throw new Error(res.status === 401 ? '面板访问令牌未配置或无效' : await res.text())
  return res.json()
}
async function enter() {
  const token = $('panelToken').value.trim()
  if (!token) { $('loginStatus').textContent = '请输入面板访问令牌'; $('loginStatus').className = 'error'; return }
  state.token = token; $('loginStatus').textContent = '验证中...'; $('loginStatus').className = 'muted'
  try {
    await loadData()
    localStorage.setItem('elysiaPanelToken', token)
    $('login').classList.add('hidden'); $('app').classList.remove('hidden')
    render()
  } catch (err) {
    $('loginStatus').textContent = String(err.message || err)
    $('loginStatus').className = 'error'
  }
}
async function refresh() {
  try { $('status').textContent = '加载中...'; await loadData(); render(); $('status').textContent = '已刷新：' + new Date().toLocaleString(); $('status').className = 'muted' }
  catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
}
async function loadData() {
  state.stats = await getJSON('/__usage/stats?' + qs())
  state.overviewStats = await getJSON('/__usage/stats?' + defaultQs())
  state.logs = await getJSON('/__usage/logs?' + logQs())
}
async function loadLogs() {
  state.logs = await getJSON('/__usage/logs?' + logQs())
}
function renderTabs() {
  $('tabs').innerHTML = views.map(v => '<button class="tab ' + (state.view === v[0] ? 'active' : '') + '" onclick="setView(\'' + v[0] + '\')">' + v[1] + '</button>').join('')
  renderGlobalTimebar()
}
function renderGlobalTimebar() {
  const el = $('globalTimebar')
  if (!el) return
  const options = [
    [1, '最近 24h'],
    [7, '最近 7 天'],
    [30, '最近 30 天'],
    [365, '最近 365 天']
  ]
  const items = options
    .filter(o => o[1] !== state.activeRangeText)
    .map(o => '<button type="button" onclick="selectQuickRange(' + o[0] + ', \'' + esc(o[1]) + '\')">' + esc(o[1]) + '</button>')
    .join('')
  el.innerHTML = '<div class="range-dropdown" onmouseenter="hoverRangeDropdown(true)" onmouseleave="hoverRangeDropdown(false)"><button type="button" class="secondary">' + esc(state.activeRangeText) + '</button><div class="range-dropdown-menu">' + items + '</div></div><label>开始时间 <input id="globalFrom" type="datetime-local" value="' + esc(state.customFrom) + '"></label><label>结束时间 <input id="globalTo" type="datetime-local" value="' + esc(state.customTo) + '"></label><button type="button" onclick="applyCustomRange()">应用</button><button class="secondary" type="button" onclick="clearCustomRange()">清除</button>'
}
function hoverRangeDropdown(active) { const el = document.querySelector('.range-dropdown'); if (el) el.classList.toggle('hover', active) }
async function selectQuickRange(days, label) {
  state.activeRangeText = label
  if (days === 1) {
    state.customFrom = ''; state.customTo = ''; state.logPage = 1
    try { $('status').textContent = '加载全局数据中...'; await loadData(); render(); $('status').textContent = '数据已刷新：' + new Date().toLocaleString(); $('status').className = 'muted' }
    catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
  } else {
    quickRange(days)
  }
  renderGlobalTimebar()
}
function setView(view) { state.view = view; renderTabs(); render() }
function fmt(n) { return fmtCount(n) }
function fmtCount(n) { return Number(n || 0).toLocaleString() }
function compactNumber(n) {
  n = Number(n || 0)
  const abs = Math.abs(n)
  if (abs < 1000) return String(Math.round(n))
  if (abs < 1000000) return (n / 1000).toFixed(abs < 10000 ? 1 : 0).replace(/\.0$/, '') + 'k'
  if (abs < 1000000000) return (n / 1000000).toFixed(abs < 10000000 ? 1 : 0).replace(/\.0$/, '') + 'm'
  return (n / 1000000000).toFixed(abs < 10000000000 ? 1 : 0).replace(/\.0$/, '') + 'b'
}
function fmtToken(n, withUnit = false) { const text = compactNumber(n); return withUnit ? text + ' token' : text }
function fmtExactToken(n) { return fmtCount(n) + ' token' }
function fmtPlainRate(n) { const v = Number(n); return Number.isFinite(v) ? fmtCount(Math.round(v)) : '-' }
function fmtRPM(n) { const v = Number(n); return Number.isFinite(v) ? v.toFixed(v < 10 ? 2 : v < 100 ? 1 : 0).replace(/\.0+$/, '').replace(/(\.\d*[1-9])0$/, '$1') + '/min' : '-' }
function fmtTPM(n) { const v = Number(n); return Number.isFinite(v) ? fmtToken(v, false) + '/min' : '-' }
function fmtRateDetail(n, unit) { const v = Number(n); return Number.isFinite(v) ? v.toLocaleString([], { maximumFractionDigits: 2 }) + ' ' + unit : '-' }
function minutesBetween(from, to) { const start = validTime(from); const end = validTime(to); if (!start || !end) return 0; return Math.max(0, (end.getTime() - start.getTime()) / 60000) }
function ratePerMinute(count, from, to) { const minutes = minutesBetween(from, to); return minutes > 0 ? Number(count || 0) / minutes : NaN }
function outputShare(s) { const total = Number((s && s.totalTokens) || 0); return total > 0 ? Number((s && s.outputTokens) || 0) / total : 0 }
function builtinToolCalls(s) { return Number((s && s.webSearchCalls) || 0) + Number((s && s.fileSearchCalls) || 0) + Number((s && s.imageGenerationCalls) || 0) }
function niceScale(max, count = 4) {
  max = Math.max(1, Number(max || 0))
  const rough = max / count
  const pow = Math.pow(10, Math.floor(Math.log10(rough)))
  const scaled = rough / pow
  const step = (scaled <= 1 ? 1 : scaled <= 2 ? 2 : scaled <= 5 ? 5 : 10) * pow
  const niceMax = Math.ceil(max / step) * step
  const ticks = []
  for (let v = 0; v <= niceMax + step / 2; v += step) ticks.push(Math.round(v))
  return { max: niceMax, ticks }
}
function isTokenMetric(key) { return String(key || '').toLowerCase().includes('token') || key === 'cacheHitTokens' }
function fmtMetric(key, value, withUnit = false) { return isTokenMetric(key) ? fmtToken(value, withUnit) : fmtCount(value) }
function fmtMaybe(n) {
  if (n === null || n === undefined || n === '') return '-'
  if (typeof n === 'number') return Number.isFinite(n) ? fmtCount(n) : '-'
  return String(n)
}
function pct(n) { return ((Number(n || 0)) * 100).toFixed(1) + '%' }
function ms(n) { const v = Number(n); return Number.isFinite(v) && v > 0 ? Math.round(v) + ' ms' : '-' }
function sec(n) { const v = Number(n); return Number.isFinite(v) && v > 0 ? (v / 1000).toFixed(2).replace(/\.?0+$/, '') + ' s' : '-' }
function validTime(v) { if (!v) return null; const d = new Date(v); return isNaN(d) || d.getFullYear() <= 1 ? null : d }
function time(v) { const d = validTime(v); return d ? d.toLocaleString() : '-' }
function esc(s) { return String(s ?? '').replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c])) }
function card(label, value, title = '', className = '') { return '<div class="card ' + esc(className) + '"' + (title ? ' title="' + esc(title) + '"' : '') + '><div class="label">' + label + '</div><div class="value">' + value + '</div></div>' }
function tokenCard(label, value) { return card(label, fmtToken(value, true), label + '：' + fmtCount(value) + ' token', 'token-card') }
function overviewRangeLabel() { return state.customFrom && state.customTo ? '当前时间窗' : '最近 24h' }
function overviewCards(rangeSummary, allTimeSummary, rangeFrom, rangeTo) {
  const range = rangeSummary || {}
  const all = allTimeSummary || range
  const rangeTPM = ratePerMinute(range.totalTokens, rangeFrom, rangeTo)
  const rangeRPM = ratePerMinute(range.requests, rangeFrom, rangeTo)
  const allTPM = ratePerMinute(all.totalTokens, all.firstUsedAt, all.lastUsedAt)
  const allRPM = ratePerMinute(all.requests, all.firstUsedAt, all.lastUsedAt)
  return '<div class="cards">'
    + overviewHeroCard({ label: '请求次数', value: fmtCount(range.requests), bg: '24h', compactPrefix: '24h', reveal: [['全部请求', fmtCount(all.requests)], ['失败次数', fmtCount(all.failed)], ['成功率', pct(all.successRate)], ['流式请求', fmtCount(all.streamRequests)]] })
    + overviewHeroCard({ label: '消耗 token', compactLabel: 'Token', value: fmtToken(range.totalTokens, false), bg: '24h', compactPrefix: '24h', pages: [[['历史总 token', fmtExactToken(all.totalTokens)], ['输入 Token', fmtExactToken(all.inputTokens)], ['输出 Token', fmtExactToken(all.outputTokens)]], [['缓存 Token', fmtExactToken(all.cacheHitTokens)], ['缓存命中率', pct(all.cacheHitRate)]]] })
    + overviewHeroCard({ label: '平均延迟', value: sec(range.avgLatencyMs), bg: '24h', compactPrefix: '24h', reveal: [['历史平均延迟', ms(all.avgLatencyMs)], ['历史平均首字', ms(all.avgFirstByteMs)], ['历史平均耗时', ms(all.avgDurationMs)]] })
    + overviewHeroCard({ label: 'TPM', value: fmtPlainRate(rangeTPM), bg: '24h', compactPrefix: '24h', compactLabel: 'TPM', compactValue: fmtPlainRate(rangeTPM), reveal: [['24h RPM', fmtRateDetail(rangeRPM, '次')], ['总体 TPM', fmtRateDetail(allTPM, 'token')], ['总体 RPM', fmtRateDetail(allRPM, '次')]] })
    + overviewHeroCard({ label: '最后调用', value: minuteTime(range.lastUsedAt), bg: yearText(range.lastUsedAt), compactPrefix: '', compactLabel: '最后调用', compactValue: minuteTime(range.lastUsedAt), className: 'last-call-card', reveal: [['首次调用', fullSecondTime(all.firstUsedAt)], ['首次调用模型', all.firstModelName || '-'], ['最后调用', fullSecondTime(all.lastUsedAt)], ['最后调用模型', all.lastModelName || '-']] })
    + overviewHeroCard({ label: '服务质量', value: pct(range.successRate), bg: 'SLA', compactPrefix: '24h', compactLabel: '成功率', reveal: [['24h 失败数', fmtCount(range.failed)], ['完整成功率', pct(all.successRate)], ['完整失败数', fmtCount(all.failed)]] })
    + overviewHeroCard({ label: 'Token 结构', value: pct(outputShare(range)), bg: 'OUT', compactPrefix: '24h', compactLabel: '输出占比', reveal: [['24h 输入', fmtExactToken(range.inputTokens)], ['24h 输出', fmtExactToken(range.outputTokens)], ['24h 缓存', fmtExactToken(range.cacheHitTokens)], ['完整缓存命中', pct(all.cacheHitRate)]] })
    + overviewHeroCard({ label: '能力调用', value: fmtCount(builtinToolCalls(range)), bg: 'TOOL', compactPrefix: '24h', compactLabel: '工具', reveal: [['Web Search', fmtCount(range.webSearchCalls)], ['File Search', fmtCount(range.fileSearchCalls)], ['Image Generation', fmtCount(range.imageGenerationCalls)], ['Responses 请求', fmtCount(range.responsesRequests)]] })
    + '</div>'
}
function overviewHeroCard(cfg) {
  const reveal = cfg.pages ? overviewPagedReveal(cfg.pages) : cfg.reveal.map(r => '<div class="overview-reveal-row"><span>' + esc(r[0]) + '</span><b>' + esc(r[1]) + '</b></div>').join('')
  return '<div class="card overview-card ' + esc(cfg.className || '') + '"><div class="overview-bg">' + esc(cfg.bg) + '</div><div class="overview-compact">' + (cfg.compactPrefix ? '<span>' + esc(cfg.compactPrefix) + '</span>' : '') + '<span>' + esc(cfg.compactLabel || cfg.label) + '</span><b>' + esc(cfg.compactValue || cfg.value) + '</b></div><div class="overview-main"><div class="overview-label">' + esc(cfg.label) + '</div><div class="overview-value">' + esc(cfg.value) + '</div></div><div class="overview-reveal">' + reveal + '</div></div>'
}
function overviewPagedReveal(pages) {
  return '<div class="overview-page-window"><div class="overview-pages">' + pages.map(page => '<div class="overview-page">' + page.map(r => '<div class="overview-reveal-row"><span>' + esc(r[0]) + '</span><b>' + esc(r[1]) + '</b></div>').join('') + '</div>').join('') + '</div></div><div class="overview-dots"><button type="button" class="overview-dot page-1" onmouseenter="setOverviewPage(this, 1)" aria-label="第一页"></button><button type="button" class="overview-dot page-2" onmouseenter="setOverviewPage(this, 2)" aria-label="第二页"></button></div>'
}
function setOverviewPage(dot, page) { const card = dot.closest('.overview-card'); if (card) card.classList.toggle('page-2', page === 2) }
function shortDate(v) { const d = validTime(v); return d ? d.toLocaleDateString([], { month: '2-digit', day: '2-digit' }) : '-' }
function yearText(v) { const d = validTime(v); return d ? String(d.getFullYear()) : 'LAST' }
function minuteTime(v) { const d = validTime(v); return d ? d.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '-' }
function fullSecondTime(v) { const d = validTime(v); return d ? d.toLocaleString([], { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '-' }
function shortClock(v) { const d = validTime(v); return d ? d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '-' }
function cards(s) { return overviewCards(s, s, null, null) }

const cols = [['名称', r => r.key, r => r.key], ['请求', r => fmtCount(r.summary.requests), r => r.summary.requests], ['成功率', r => pct(r.summary.successRate), r => r.summary.successRate], ['总Token', r => fmtExactToken(r.summary.totalTokens), r => r.summary.totalTokens], ['输入', r => fmtExactToken(r.summary.inputTokens), r => r.summary.inputTokens], ['输出', r => fmtExactToken(r.summary.outputTokens), r => r.summary.outputTokens], ['缓存', r => fmtExactToken(r.summary.cacheHitTokens), r => r.summary.cacheHitTokens], ['平均首字', r => ms(r.summary.avgFirstByteMs), r => r.summary.avgFirstByteMs], ['P95首字', r => ms(r.summary.p95FirstByteMs), r => r.summary.p95FirstByteMs], ['平均耗时', r => ms(r.summary.avgDurationMs), r => r.summary.avgDurationMs], ['最后调用', r => time(r.summary.lastUsedAt), r => new Date(r.summary.lastUsedAt).getTime() || 0]]
function table(rows, columns = cols, tableId = '') {
  const sortedRows = sortRows(rows, columns, tableId)
  return '<table><tr>' + columns.map((c, i) => '<th>' + (c[2] ? '<button class="table-sort" type="button" onclick="sortTable(\'' + esc(tableId) + '\',' + i + ')">' + c[0] + sortMark(tableId, i) + '</button>' : c[0]) + '</th>').join('') + '</tr>' + sortedRows.map(r => '<tr>' + columns.map(c => '<td>' + (c[1](r) ?? '') + '</td>').join('') + '</tr>').join('') + '</table>'
}
function sortRows(rows, columns, tableId) {
  const copy = rows.slice()
  if (!tableId || state.sort.table !== tableId || state.sort.index < 0) return copy
  const col = columns[state.sort.index]
  if (!col || !col[2]) return copy
  const dir = state.sort.dir === 'asc' ? 1 : -1
  return copy.sort((a, b) => compareValues(col[2](a), col[2](b)) * dir)
}
function compareValues(a, b) {
  if (typeof a === 'string' || typeof b === 'string') return String(a ?? '').localeCompare(String(b ?? ''), 'zh-Hans-CN')
  return Number(a || 0) - Number(b || 0)
}
function sortTable(tableId, index) {
  if (state.sort.table === tableId && state.sort.index === index) state.sort.dir = state.sort.dir === 'asc' ? 'desc' : 'asc'
  else state.sort = { table: tableId, index, dir: 'desc' }
  render()
}
function sortMark(tableId, index) { return state.sort.table === tableId && state.sort.index === index ? (state.sort.dir === 'asc' ? ' ↑' : ' ↓') : '' }
function render() {
  if (!state.stats) return
  if (state.view === 'overview') renderOverview()
  if (state.view === 'time') renderTime()
  if (state.view === 'caller') renderAggregate('按调用方令牌', state.stats.byCaller || state.stats.byKey || [])
  if (state.view === 'group') renderAggregate('按模型组', state.stats.byModelGroup || [])
  if (state.view === 'model') renderAggregate('按具体模型', state.stats.byModel || [])
  if (state.view === 'logs') renderLogs()
}
function renderOverview() {
  const overviewStats = state.overviewStats || state.stats
  $('content').innerHTML = '<div class="overview-stack">' + overviewCards((overviewStats && overviewStats.summary) || {}, (overviewStats && (overviewStats.allTimeSummary || overviewStats.summary)) || {}, overviewStats && overviewStats.from, overviewStats && overviewStats.to) + '<section><h2>时间趋势</h2><div class="chart" id="chart"></div></section></div>'
  renderLineChart('chart', state.stats.chartSeries || state.stats.series || [], [['请求数', 'requests'], ['总Token', 'totalTokens'], ['输入Token', 'inputTokens'], ['输出Token', 'outputTokens']], 'overviewVisibleMetrics')
}
function renderTime() {
  const metrics = [['请求数', 'requests'], ['总Token', 'totalTokens'], ['输入Token', 'inputTokens'], ['输出Token', 'outputTokens'], ['缓存Token', 'cacheHitTokens']]
  const columns = [['时间', r => time(r.window || r.key), r => new Date(r.window || r.key).getTime() || 0], ['请求', r => fmtCount(r.summary.requests), r => r.summary.requests], ['成功率', r => pct(r.summary.successRate), r => r.summary.successRate], ['总Token', r => fmtCount(r.summary.totalTokens), r => r.summary.totalTokens], ['输入', r => fmtCount(r.summary.inputTokens), r => r.summary.inputTokens], ['输出', r => fmtCount(r.summary.outputTokens), r => r.summary.outputTokens], ['缓存', r => fmtCount(r.summary.cacheHitTokens), r => r.summary.cacheHitTokens], ['平均首字', r => ms(r.summary.avgFirstByteMs), r => r.summary.avgFirstByteMs], ['平均耗时', r => ms(r.summary.avgDurationMs), r => r.summary.avgDurationMs]]
  const customNote = state.customFrom && state.customTo ? '<div class="muted">当前使用全局时间窗：' + time(state.customFrom) + ' - ' + time(state.customTo) + '</div>' : ''
  $('content').innerHTML = '<section><h2>按时间统计</h2><div class="muted">当前粒度：' + windowLabel(state.stats.window) + '</div>' + customNote + '<div class="chart" id="chart"></div>' + table(state.stats.series || [], columns, 'time') + '</section>'
  renderLineChart('chart', state.stats.chartSeries || state.stats.series || [], metrics, 'timeVisibleMetrics')
}
function applyCustomRange() {
  const from = $('globalFrom').value
  const to = $('globalTo').value
  if (!from || !to) { $('status').textContent = '请选择完整的开始和结束时间'; $('status').className = 'error'; return }
  if (new Date(from).getTime() >= new Date(to).getTime()) { $('status').textContent = '开始时间必须早于结束时间'; $('status').className = 'error'; return }
  state.activeRangeText = '自定义'
  state.customFrom = from; state.customTo = to; state.logPage = 1; refresh()
}
function clearCustomRange() {
  state.activeRangeText = '最近 24h'
  state.customFrom = ''; state.customTo = ''; state.logPage = 1; refresh()
}
function quickRange(days) {
  const to = new Date(); const from = new Date(to); from.setDate(from.getDate() - Number(days || 1))
  state.activeRangeText = '最近 ' + days + ' 天'
  state.customFrom = toLocalInputValue(from); state.customTo = toLocalInputValue(to); state.logPage = 1; refresh()
}
function toLocalInputValue(date) {
  const pad = n => String(n).padStart(2, '0')
  return date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate()) + 'T' + pad(date.getHours()) + ':' + pad(date.getMinutes())
}
function toggleMetric(stateKey, key) {
  const visibleState = state[stateKey]
  const visible = Object.keys(visibleState).filter(k => visibleState[k])
  if (visibleState[key] && visible.length <= 1) return
  visibleState[key] = !visibleState[key]
  render()
}
function visibleMetrics(metrics, stateKey) { return stateKey ? metrics.filter(m => state[stateKey][m[1]]) : metrics }
function windowLabel(v) { return ({'5m':'5 分钟','15m':'15 分钟',hour:'小时',day:'天',minute:'分钟'})[v] || v || '-' }
function renderAggregate(title, rows) {
  $('content').innerHTML = '<section><h2>' + title + '</h2><div class="chart" id="chart"></div>' + table(rows, cols, 'aggregate-' + state.view) + '</section>'
  renderBarChart('chart', rows, 'totalTokens')
}
function renderLogs() {
  const rows = (state.logs && state.logs.items) || []
  const logCols = [['时间', r => time(r.startedAt), r => new Date(r.startedAt).getTime() || 0], ['调用方', r => esc(r.keyName || r.keyHash), r => r.keyName || r.keyHash || ''], ['模型组', r => esc(r.groupName), r => r.groupName || ''], ['模型', r => esc(r.modelName), r => r.modelName || ''], ['状态', r => r.statusCode, r => Number(r.statusCode || 0)], ['流式', r => r.stream ? '是' : '否', r => r.stream ? 1 : 0], ['Token', r => fmtMaybe(r.usage && r.usage.totalTokens), r => Number((r.usage && r.usage.totalTokens) || 0)], ['首字', r => ms(r.firstByteMs), r => Number(r.firstByteMs || 0)], ['耗时', r => ms(r.durationMs), r => Number(r.durationMs || 0)], ['详情', r => '<button class="secondary" onclick="detail(\'' + r.requestId + '\')">查看</button>']]
  const total = state.logs ? state.logs.total : 0
  const pageSize = Number(state.logPageSize || 50) || 50
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  if (state.logPage > totalPages) state.logPage = totalPages
  const summary = '<div class="muted log-summary">后端匹配 ' + fmtCount(total) + ' 条，当前第 ' + fmtCount(state.logPage) + '/' + fmtCount(totalPages) + ' 页，显示 ' + fmtCount(rows.length) + ' 条</div>'
  $('content').innerHTML = '<section><h2>请求日志</h2>' + logFilterPane() + summary + table(rows, logCols, 'logs') + renderLogPagination(total) + '</section>'
}
function logFilterPane() {
  const f = state.logFilters
  const pageTools = '<label class="log-page-size">单页展示日志数 <select id="logPageSize" onchange="changeLogPageSize()">' + selectOptions([['10', '10'], ['50', '50'], ['100', '100'], ['500', '500'], ['1000', '1000']], state.logPageSize) + '</select></label>'
  return '<div class="filter-pane">'
    + multiSelectDropdown('logKeyNames', '调用方', callerOptions(), f.keyNames || [], '搜索调用方')
    + multiSelectDropdown('logGroupNames', '模型组', groupOptions(), f.groupNames || [], '搜索模型组')
    + multiSelectDropdown('logModelNames', '模型', modelOptions(), f.modelNames || [], '搜索模型')
    + '<label>流式 <select id="logStream">' + selectOptions([['', '全部'], ['true', '是'], ['false', '否']], f.stream) + '</select></label>'
    + '<label>状态 <select id="logStatus">' + selectOptions([['', '全部'], ['success', '成功'], ['failed', '失败'], ['200', '200'], ['400', '400'], ['401', '401'], ['500', '500']], f.status) + '</select></label>'
    + '<div class="filter-actions"><button type="button" onclick="applyLogFilters()">应用筛选</button><button class="secondary" type="button" onclick="clearLogFilters()">清除筛选</button></div>'
    + pageTools
    + '</div>'
}
function callerOptions() { return uniqueOptions((state.stats && (state.stats.byCaller || state.stats.byKey)) || [], r => r.keyName || r.key, r => r.keyName || r.key) }
function groupOptions() { return uniqueOptions((state.stats && state.stats.byModelGroup) || [], r => r.groupName || r.key, r => r.groupName || r.key) }
function modelOptions() { return uniqueOptions((state.stats && state.stats.byModel) || [], r => r.modelName || r.key, r => (r.groupName && r.modelName) ? r.groupName + ' / ' + r.modelName : r.key) }
function uniqueOptions(rows, valueFn, labelFn) {
  const seen = new Set()
  return rows.map(r => ({ value: valueFn(r), label: labelFn(r) })).filter(o => o.value && !seen.has(o.value) && seen.add(o.value))
}
function multiSelectDropdown(id, label, options, selectedValues, placeholder) {
  const selected = new Set(selectedValues || [])
  const caption = selected.size ? label + ' · ' + selected.size : label + ' · 全部'
  const items = options.map(o => '<label class="multi-option" data-label="' + esc(String(o.label).toLowerCase()) + '"><input type="checkbox" name="' + esc(id) + '" value="' + esc(o.value) + '"' + (selected.has(o.value) ? ' checked' : '') + '> <span>' + esc(o.label) + '</span></label>').join('') || '<div class="muted">暂无选项</div>'
  return '<div class="multi-select" id="' + esc(id) + 'Wrap" onmouseenter="hoverMultiSelect(\'' + esc(id) + '\', true)" onmouseleave="hoverMultiSelect(\'' + esc(id) + '\', false)"><label>' + esc(label) + '<button type="button" onclick="toggleMultiSelect(\'' + esc(id) + '\')">' + esc(caption) + '</button></label><div class="multi-select-menu"><input type="search" placeholder="' + esc(placeholder) + '" oninput="filterMultiOptions(\'' + esc(id) + '\', this.value)"><div class="filter-actions"><button class="secondary" type="button" onclick="setMultiOptions(\'' + esc(id) + '\', true)">全选</button><button class="secondary" type="button" onclick="setMultiOptions(\'' + esc(id) + '\', false)">清空</button></div>' + items + '</div></div>'
}
function hoverMultiSelect(id, active) { const el = $(id + 'Wrap'); if (el && !el.classList.contains('open')) el.classList.toggle('hover', active) }
function toggleMultiSelect(id) { const el = $(id + 'Wrap'); if (!el) return; el.classList.toggle('open'); el.classList.remove('hover') }
function filterMultiOptions(id, query) {
  const root = $(id + 'Wrap'); if (!root) return
  const q = String(query || '').toLowerCase()
  root.querySelectorAll('.multi-option').forEach(el => { el.style.display = el.dataset.label.includes(q) ? 'flex' : 'none' })
}
function setMultiOptions(id, checked) { const root = $(id + 'Wrap'); if (root) root.querySelectorAll('input[type="checkbox"]').forEach(el => { el.checked = checked }) }
function selectedMultiValues(id) { const root = $(id + 'Wrap'); return root ? Array.from(root.querySelectorAll('input[type="checkbox"]:checked')).map(el => el.value) : [] }
function selectOptions(options, current) { return options.map(o => '<option value="' + esc(o[0]) + '"' + (String(current) === String(o[0]) ? ' selected' : '') + '>' + esc(o[1]) + '</option>').join('') }
function readLogFilters() {
  state.logFilters = {
    keyNames: selectedMultiValues('logKeyNames'),
    groupNames: selectedMultiValues('logGroupNames'),
    modelNames: selectedMultiValues('logModelNames'),
    stream: $('logStream').value,
    status: $('logStatus').value
  }
}
async function applyLogFilters() {
  readLogFilters()
  state.logPage = 1
  try { $('status').textContent = '筛选日志中...'; await loadLogs(); renderLogs(); $('status').textContent = '日志已筛选：' + new Date().toLocaleString(); $('status').className = 'muted' }
  catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
}
async function clearLogFilters() {
  state.logFilters = { keyNames: [], groupNames: [], modelNames: [], stream: '', status: '' }
  state.logPage = 1
  try { $('status').textContent = '清除日志筛选中...'; await loadLogs(); renderLogs(); $('status').textContent = '日志筛选已清除：' + new Date().toLocaleString(); $('status').className = 'muted' }
  catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
}
async function changeLogPageSize() {
  state.logPageSize = $('logPageSize').value || '50'
  state.logPage = 1
  try { $('status').textContent = '加载日志中...'; await loadLogs(); renderLogs(); $('status').textContent = '日志已刷新：' + new Date().toLocaleString(); $('status').className = 'muted' }
  catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
}
function renderLogPagination(total) {
  const pageSize = Number(state.logPageSize || 50) || 50
  const totalPages = Math.max(1, Math.ceil(Number(total || 0) / pageSize))
  const page = Math.max(1, Math.min(totalPages, Number(state.logPage || 1) || 1))
  return '<div class="time-toolbar log-pagination"><button class="secondary" type="button" onclick="goLogPage(' + (page - 1) + ')"' + (page <= 1 ? ' disabled' : '') + '>上一页</button><span class="muted">第 ' + fmtCount(page) + ' / ' + fmtCount(totalPages) + ' 页</span><button class="secondary" type="button" onclick="goLogPage(' + (page + 1) + ')"' + (page >= totalPages ? ' disabled' : '') + '>下一页</button><input id="logPageJump" type="number" min="1" max="' + totalPages + '" value="' + page + '"><button type="button" onclick="jumpLogPage()">跳转</button></div>'
}
async function goLogPage(page) {
  const total = state.logs ? state.logs.total : 0
  const pageSize = Number(state.logPageSize || 50) || 50
  const totalPages = Math.max(1, Math.ceil(Number(total || 0) / pageSize))
  state.logPage = Math.max(1, Math.min(totalPages, Number(page || 1) || 1))
  try { $('status').textContent = '加载日志中...'; await loadLogs(); renderLogs(); $('status').textContent = '日志已刷新：' + new Date().toLocaleString(); $('status').className = 'muted' }
  catch (err) { $('status').textContent = String(err.message || err); $('status').className = 'error' }
}
function jumpLogPage() { goLogPage($('logPageJump').value) }
function metric(row, key) { return Number((row.summary && row.summary[key]) || 0) }
function renderLineChart(id, rows, metrics, stateKey = '') {
  const el = $(id); if (!rows.length) { el.innerHTML = '<p class="muted">暂无数据</p>'; return }
  const activeMetrics = visibleMetrics(metrics, stateKey)
  const w = 900, h = 260, left = 68, right = 68, top = 18, bottom = 48
  const plotLeft = left, plotRight = w - right, plotWidth = plotRight - plotLeft, plotBottom = h - bottom, plotHeight = plotBottom - top
  const tokenMetrics = activeMetrics.filter(m => m[1] !== 'requests')
  const requestMetrics = activeMetrics.filter(m => m[1] === 'requests')
  const rawTokenMax = Math.max(1, ...tokenMetrics.flatMap(m => rows.map(r => metric(r, m[1]))))
  const rawRequestMax = Math.max(1, ...requestMetrics.flatMap(m => rows.map(r => metric(r, m[1]))))
  const tokenScale = niceScale(rawTokenMax)
  const requestScale = niceScale(rawRequestMax)
  const tokenMax = tokenScale.max
  const requestMax = requestScale.max
  const x = i => plotLeft + (rows.length === 1 ? plotWidth / 2 : i * plotWidth / (rows.length - 1))
  const yToken = v => plotBottom - (v / tokenMax) * plotHeight
  const yRequest = v => plotBottom - (v / requestMax) * plotHeight
  const yFor = (key, v) => key === 'requests' ? yRequest(v) : yToken(v)
  const tokenAxis = tokenScale.ticks.map(v => { const yy = yToken(v); return '<line x1="' + plotLeft + '" y1="' + yy + '" x2="' + plotRight + '" y2="' + yy + '" stroke="var(--border)" opacity=".55"/><text x="' + (plotLeft-8) + '" y="' + (yy+4) + '" text-anchor="end" fill="var(--muted)" font-size="11">' + fmtToken(v) + '</text>' }).join('')
  const requestAxis = requestMetrics.length ? requestScale.ticks.map(v => { const yy = yRequest(v); return '<text x="' + (plotRight+8) + '" y="' + (yy+4) + '" text-anchor="start" fill="var(--muted)" font-size="11">' + fmtCount(v) + '</text>' }).join('') : ''
  const tickStep = Math.max(1, Math.ceil(rows.length / 6))
  const xAxis = rows.map((r,i) => i % tickStep === 0 || i === rows.length - 1 ? '<text x="' + x(i) + '" y="' + (h-18) + '" text-anchor="middle" fill="var(--muted)" font-size="10">' + esc(shortTime(r.window || r.key)) + '</text>' : '').join('')
  const paths = activeMetrics.map((m) => '<path fill="none" stroke="' + colors[metrics.indexOf(m) % colors.length] + '" stroke-width="2" d="' + smoothPath(rows.map((r, i) => ({ x: x(i), y: yFor(m[1], metric(r, m[1])) }))) + '"/>').join('')
  const hoverPoints = activeMetrics.map((m) => '<circle class="hover-point" data-key="' + esc(m[1]) + '" cx="0" cy="0" r="3.5" fill="' + colors[metrics.indexOf(m) % colors.length] + '" stroke="var(--panel)" stroke-width="1.5" opacity="0"/>').join('')
  const legend = metrics.map((m, i) => {
    const disabled = stateKey && !state[stateKey][m[1]]
    return '<button type="button" class="legend-item ' + (disabled ? 'disabled' : '') + '" data-state-key="' + esc(stateKey) + '" data-metric-key="' + esc(m[1]) + '"><i class="dot" style="background:' + colors[i % colors.length] + '"></i>' + m[0] + '</button>'
  }).join('')
  el.innerHTML = '<svg viewBox="0 0 ' + w + ' ' + h + '"><line x1="' + plotLeft + '" y1="' + plotBottom + '" x2="' + plotRight + '" y2="' + plotBottom + '" stroke="var(--border)"/><line x1="' + plotLeft + '" y1="' + top + '" x2="' + plotLeft + '" y2="' + plotBottom + '" stroke="var(--border)"/><line x1="' + plotRight + '" y1="' + top + '" x2="' + plotRight + '" y2="' + plotBottom + '" stroke="var(--border)"/>' + tokenAxis + requestAxis + xAxis + paths + hoverPoints + '<line id="cursor-' + id + '" x1="0" y1="' + top + '" x2="0" y2="' + plotBottom + '" stroke="var(--primary)" opacity="0"/><rect class="plot-hit" x="' + plotLeft + '" y="' + top + '" width="' + plotWidth + '" height="' + plotHeight + '" fill="transparent" pointer-events="all"/></svg><div class="chart-tooltip" id="tip-' + id + '"></div><div class="legend">' + legend + '</div>'
  el.querySelectorAll('.legend-item').forEach(button => { button.onclick = () => { const sk = button.dataset.stateKey; if (sk) toggleMetric(sk, button.dataset.metricKey) } })
  const svg = el.querySelector('svg'), tip = $('tip-' + id), cursor = $('cursor-' + id), hit = el.querySelector('.plot-hit'), hoverPointEls = Array.from(el.querySelectorAll('.hover-point'))
  const showPoint = ev => {
    const rect = svg.getBoundingClientRect(); const point = svgPoint(svg, ev)
    const ratio = rows.length === 1 ? 0 : Math.max(0, Math.min(1, (point.x - plotLeft) / plotWidth))
    const idx = rows.length === 1 ? 0 : Math.max(0, Math.min(rows.length - 1, Math.round(ratio * (rows.length - 1))))
    cursor.setAttribute('x1', x(idx)); cursor.setAttribute('x2', x(idx)); cursor.setAttribute('opacity', '.8')
    hoverPointEls.forEach(point => {
      const key = point.dataset.key
      point.setAttribute('cx', x(idx)); point.setAttribute('cy', yFor(key, metric(rows[idx], key))); point.setAttribute('opacity', '1')
    })
    const localX = Math.max(8, Math.min(rect.width - 190, ev.clientX - rect.left + 14))
    const localY = Math.max(8, Math.min(rect.height - 80, ev.clientY - rect.top - 12))
    tip.style.display = 'block'; tip.style.left = localX + 'px'; tip.style.top = localY + 'px'
    tip.innerHTML = '<strong>' + esc(time(rows[idx].window || rows[idx].key)) + '</strong><br>' + activeMetrics.map((m) => '<span style="color:' + colors[metrics.indexOf(m) % colors.length] + '">' + m[0] + ': ' + fmtMetric(m[1], metric(rows[idx], m[1]), true) + '</span>').join('<br>')
  }
  hit.onmousemove = showPoint
  hit.onmouseleave = () => { tip.style.display = 'none'; cursor.setAttribute('opacity', '0'); hoverPointEls.forEach(point => point.setAttribute('opacity', '0')) }
}
function svgPoint(svg, ev) {
  const point = svg.createSVGPoint()
  point.x = ev.clientX; point.y = ev.clientY
  return point.matrixTransform(svg.getScreenCTM().inverse())
}
function smoothPath(points) {
  if (!points.length) return ''
  if (points.length < 3) return 'M ' + points.map(p => p.x + ' ' + p.y).join(' L ')
  let d = 'M ' + points[0].x + ' ' + points[0].y
  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(0, i - 1)], p1 = points[i], p2 = points[i + 1], p3 = points[Math.min(points.length - 1, i + 2)]
    const c1x = p1.x + (p2.x - p0.x) / 6, c1y = p1.y + (p2.y - p0.y) / 6
    const c2x = p2.x - (p3.x - p1.x) / 6, c2y = p2.y - (p3.y - p1.y) / 6
    d += ' C ' + c1x + ' ' + c1y + ', ' + c2x + ' ' + c2y + ', ' + p2.x + ' ' + p2.y
  }
  return d
}
function shortTime(v) { const d = new Date(v); return isNaN(d) ? v : d.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) }
function renderBarChart(id, rows, key) {
  const el = $(id); if (!rows.length) { el.innerHTML = '<p class="muted">暂无数据</p>'; return }
  const top = rows.slice().sort((a,b) => metric(b,key)-metric(a,key)).slice(0, 12)
  const max = Math.max(1, ...top.map(r => metric(r,key)))
  el.innerHTML = top.map(r => '<div style="display:grid;grid-template-columns:minmax(120px,240px) 1fr 90px;gap:10px;align-items:center;margin:8px 0"><span title="' + esc(r.key) + '">' + esc(r.key) + '</span><div style="height:14px;background:var(--border);border-radius:999px;overflow:hidden"><div style="width:' + (metric(r,key)/max*100) + '%;height:100%;background:var(--primary)"></div></div><strong title="' + esc(fmtExactToken(metric(r,key))) + '">' + fmtToken(metric(r,key), true) + '</strong></div>').join('')
}
async function detail(id) {
  const data = await getJSON('/__usage/logs/' + id)
  const inputProtocol = normalizeProtocol(field(data, 'inputFormat', 'InputFormat'))
  const targetProtocol = normalizeProtocol(field(data, 'targetPlatform', 'TargetPlatform') || field(data, 'platform', 'Platform'))
  $('detailBody').innerHTML = renderDetailSummary(data) + renderDetailBodySection('收到的请求体', field(data, 'incomingBody', 'IncomingBody'), inputProtocol, 'request') + renderDetailBodySection('发送的请求体', field(data, 'outgoingBody', 'OutgoingBody'), targetProtocol, 'request') + renderDetailBodySection('API 服务商返回结构体', field(data, 'providerResponse', 'ProviderResponse'), targetProtocol, 'response')
  $('detail').showModal()
}
function field(obj, ...names) {
  for (const name of names) {
    if (obj && obj[name] !== undefined && obj[name] !== null) return obj[name]
  }
  return undefined
}
function renderDetailSummary(data) {
  const usage = field(data, 'usage', 'Usage') || {}
  const statusCode = field(data, 'statusCode', 'StatusCode')
  const inputFormat = field(data, 'inputFormat', 'InputFormat')
  const targetPlatform = field(data, 'targetPlatform', 'TargetPlatform') || field(data, 'platform', 'Platform')
  const groupName = field(data, 'groupName', 'GroupName') || '-'
  const modelName = field(data, 'modelName', 'ModelName') || '-'
  return '<div class="detail-section">'
    + '<div class="detail-title"><h3>请求概览</h3>' + statusPill(statusCode) + '</div>'
    + '<div class="detail-summary">'
    + detailMetric('请求 ID', field(data, 'requestId', 'RequestID'))
    + detailMetric('调用方', field(data, 'keyName', 'KeyName') || field(data, 'keyHash', 'KeyHash'))
    + detailMetric('模型组 / 模型', groupName + ' / ' + modelName)
    + detailMetric('协议转换', (inputFormat || '-') + ' → ' + (targetPlatform || '-'))
    + detailMetric('流式', field(data, 'stream', 'Stream') ? '是' : '否')
    + detailMetric('重试次数', field(data, 'retryCount', 'RetryCount'))
    + detailMetric('首字延迟', ms(field(data, 'firstByteMs', 'FirstByteMs')))
    + detailMetric('总耗时', ms(field(data, 'durationMs', 'DurationMs')))
    + detailMetric('总 Token', tokenDetail(field(usage, 'totalTokens', 'TotalTokens')), 'token-card')
    + detailMetric('输入 Token', tokenDetail(field(usage, 'inputTokens', 'InputTokens')), 'token-card')
    + detailMetric('输出 Token', tokenDetail(field(usage, 'outputTokens', 'OutputTokens')), 'token-card')
    + detailMetric('缓存 Token', tokenDetail(field(usage, 'cacheHitTokens', 'CacheHitTokens')), 'token-card')
    + '</div>'
    + (field(data, 'error', 'Error') ? '<div class="protocol-card"><strong>错误信息</strong><pre>' + esc(field(data, 'error', 'Error')) + '</pre></div>' : '')
    + '</div>'
}
function detailMetric(label, value, className = '') { return '<div class="detail-metric ' + esc(className) + '"><span>' + esc(label) + '</span><b>' + esc(fmtMaybe(value)) + '</b></div>' }
function tokenDetail(value) { return value === null || value === undefined ? '-' : fmtExactToken(value) }
function statusPill(code) { const n = Number(code); if (!Number.isFinite(n)) return '<span class="status-pill">未知 · -</span>'; const ok = n >= 200 && n < 400; return '<span class="status-pill ' + (ok ? 'ok' : 'error') + '">' + (ok ? '成功' : '失败') + ' · ' + esc(n) + '</span>' }
function renderDetailBodySection(title, body, protocol, kind) { return '<div class="detail-section"><div class="detail-title"><h3>' + esc(title) + '</h3><span class="status-pill">' + esc(protocol.toUpperCase()) + '</span></div><div class="body-card">' + renderProtocolBody(body, protocol, kind) + '</div></div>' }
function normalizeProtocol(value) {
  value = String(value || '').toLowerCase()
  if (value.includes('anthropic') || value.includes('claude')) return 'claude'
  if (value.includes('gemini')) return 'gemini'
  return 'openai'
}
function parseBodyContent(body) {
  const content = body && (body.content !== undefined ? body.content : body.Content)
  if (!content) return null
  return parseMaybeJSON(content)
}
function parseMaybeJSON(value) {
  if (typeof value !== 'string') return value
  const text = value.trim()
  if (!text) return ''
  try {
    const parsed = JSON.parse(text)
    if (typeof parsed === 'string' && /^[\[{]/.test(parsed.trim())) return parseMaybeJSON(parsed)
    return parsed
  } catch {
    return value
  }
}
function renderProtocolBody(body, protocol, kind) {
  const value = parseBodyContent(body)
  if (value === null) return '<p class="muted">无数据</p>'
  if (typeof value === 'string') return '<pre>' + esc(value) + '</pre>'
  let html = ''
  if (Array.isArray(value)) {
    html += '<div class="protocol-card"><strong>流式事件采样</strong><div class="protocol-list"><div><span>事件数量</span><b>' + value.length + '</b></div></div></div>'
    const usageEvent = value.map(v => extractUsageRoot(v, protocol)).find(Boolean)
    if (usageEvent) html += renderUsageFields(usageEvent, protocol)
  } else if (protocol === 'claude') html += renderClaudeView(value, kind)
  else if (protocol === 'gemini') html += renderGeminiView(value, kind)
  else html += renderOpenAIView(value, kind)
  return html + '<div class="protocol-card"><strong>完整 JSON 结构</strong>' + jsonPanel(value) + '</div>'
}
function renderOpenAIView(value, kind) {
  const usage = extractUsageRoot(value, 'openai')
  if (kind === 'request') return '<div class="protocol-grid">' + protocolCard('OpenAI 基础参数', [['model', value.model], ['stream', value.stream], ['max_tokens', value.max_tokens], ['max_completion_tokens', value.max_completion_tokens], ['temperature', value.temperature], ['top_p', value.top_p], ['reasoning_effort', value.reasoning_effort], ['tool_choice', summaryText(value.tool_choice, 80)]]) + '</div>' + renderListSection('Messages', value.messages || [], renderOpenAIMessage) + renderOpenAITools(value.tools || [])
  return '<div class="protocol-grid">' + protocolCard('OpenAI 响应', [['id', value.id], ['object', value.object], ['model', value.model], ['choices', len(value.choices)], ['finish_reason', value.choices && value.choices[0] && value.choices[0].finish_reason]]) + '</div>' + renderUsageFields(usage, 'openai')
}
function renderClaudeView(value, kind) {
  const usage = extractUsageRoot(value, 'claude')
  if (kind === 'request') return '<div class="protocol-grid">' + protocolCard('Claude 基础参数', [['model', value.model], ['max_tokens', value.max_tokens], ['stream', value.stream], ['temperature', value.temperature], ['top_p', value.top_p], ['stop_sequences', summaryText(value.stop_sequences, 80)]]) + protocolCard('System', [['content', summaryText(value.system, 220)]]) + '</div>' + renderListSection('Messages', value.messages || [], renderClaudeMessage) + renderClaudeTools(value.tools || [])
  return '<div class="protocol-grid">' + protocolCard('Claude 响应', [['id', value.id || (value.message && value.message.id)], ['type', value.type], ['model', value.model || (value.message && value.message.model)], ['role', value.role || (value.message && value.message.role)], ['content', len(value.content || (value.message && value.message.content))], ['stop_reason', value.stop_reason || (value.delta && value.delta.stop_reason)]]) + '</div>' + renderUsageFields(usage, 'claude')
}
function renderGeminiView(value, kind) {
  const usage = extractUsageRoot(value, 'gemini')
  if (kind === 'request') return '<div class="protocol-grid">' + protocolCard('Gemini 基础参数', [['contents', len(value.contents)], ['tools', len(value.tools)], ['systemInstruction', summaryText(value.systemInstruction, 160)]]) + protocolCard('Generation Config', [['maxOutputTokens', value.generationConfig && value.generationConfig.maxOutputTokens], ['temperature', value.generationConfig && value.generationConfig.temperature], ['topP', value.generationConfig && value.generationConfig.topP], ['topK', value.generationConfig && value.generationConfig.topK], ['stopSequences', summaryText(value.generationConfig && value.generationConfig.stopSequences, 80)], ['responseMimeType', value.generationConfig && value.generationConfig.responseMimeType]]) + '</div>' + renderListSection('Contents', value.contents || [], renderGeminiContent) + renderGeminiTools(value.tools || [])
  return '<div class="protocol-grid">' + protocolCard('Gemini 响应', [['candidates', len(value.candidates)], ['finishReason', value.candidates && value.candidates[0] && value.candidates[0].finishReason], ['promptFeedback', value.promptFeedback === undefined ? undefined : 'present']]) + '</div>' + renderUsageFields(usage, 'gemini')
}
function extractUsageRoot(value, protocol) {
  if (!value || typeof value !== 'object') return null
  if (protocol === 'gemini') return value.usageMetadata || null
  if (protocol === 'claude') return value.usage || (value.message && value.message.usage) || (value.message_delta && value.message_delta.usage) || null
  return value.usage || null
}
function renderUsageFields(usage, protocol) {
  if (!usage) return '<div class="protocol-card"><strong>Usage / Token 信息</strong><p class="muted">未返回 usage 字段</p></div>'
  let rows
  if (protocol === 'gemini') rows = [['promptTokenCount', usage.promptTokenCount], ['toolUsePromptTokenCount', usage.toolUsePromptTokenCount], ['candidatesTokenCount', usage.candidatesTokenCount], ['thoughtsTokenCount', usage.thoughtsTokenCount], ['totalTokenCount', usage.totalTokenCount], ['cachedContentTokenCount', usage.cachedContentTokenCount], ['promptTokensDetails', len(usage.promptTokensDetails)], ['toolUsePromptTokensDetails', len(usage.toolUsePromptTokensDetails)], ['candidatesTokensDetails', len(usage.candidatesTokensDetails)]]
  else if (protocol === 'claude') rows = [['input_tokens', usage.input_tokens], ['output_tokens', usage.output_tokens], ['cache_read_input_tokens', usage.cache_read_input_tokens], ['cache_creation_input_tokens', usage.cache_creation_input_tokens], ['cache_creation.5m', usage.cache_creation && usage.cache_creation.ephemeral_5m_input_tokens], ['cache_creation.1h', usage.cache_creation && usage.cache_creation.ephemeral_1h_input_tokens], ['server_tool_use.web_search_requests', usage.server_tool_use && usage.server_tool_use.web_search_requests]]
  else rows = [['prompt_tokens', usage.prompt_tokens], ['completion_tokens', usage.completion_tokens], ['total_tokens', usage.total_tokens], ['cached_tokens', usage.cached_tokens], ['prompt_tokens_details.cached_tokens', usage.prompt_tokens_details && usage.prompt_tokens_details.cached_tokens], ['prompt_tokens_details.cached_creation_tokens', usage.prompt_tokens_details && usage.prompt_tokens_details.cached_creation_tokens], ['completion_tokens_details.reasoning_tokens', usage.completion_tokens_details && usage.completion_tokens_details.reasoning_tokens], ['input_tokens', usage.input_tokens], ['output_tokens', usage.output_tokens]]
  return protocolCard('Usage / Token 信息', rows)
}
function summaryText(value, max = 160) {
  if (value === null || value === undefined || value === '') return '-'
  const text = typeof value === 'string' ? value : JSON.stringify(value)
  return text.length > max ? text.slice(0, max) + '…' : text
}
function renderListSection(title, items, renderer) {
  if (!Array.isArray(items) || !items.length) return '<div class="protocol-card"><strong>' + esc(title) + '</strong><p class="muted">无数据</p></div>'
  return '<div class="protocol-card"><strong>' + esc(title) + ' <span class="muted">[' + items.length + ']</span></strong><div class="protocol-list">' + items.map((item, i) => renderer(item, i)).join('') + '</div></div>'
}
function renderOpenAIMessage(message, i) {
  return '<div><span>#' + i + ' ' + esc(message.role || '-') + '</span><b>' + esc(summaryText(message.content, 220)) + '</b></div>' + (message.tool_calls ? '<div><span>tool_calls</span><b>' + esc(summaryText(message.tool_calls, 220)) + '</b></div>' : '')
}
function renderOpenAITools(tools) { return renderListSection('Tools', tools, (tool, i) => '<div><span>#' + i + ' ' + esc(tool.type || 'function') + '</span><b>' + esc(summaryText(tool.function && (tool.function.name + ': ' + (tool.function.description || '')), 180)) + '</b></div>') }
function renderClaudeMessage(message, i) { return '<div><span>#' + i + ' ' + esc(message.role || '-') + '</span><b>' + esc(summaryText(message.content, 240)) + '</b></div>' }
function renderClaudeTools(tools) { return renderListSection('Tools', tools, (tool, i) => '<div><span>#' + i + ' ' + esc(tool.name || '-未命名-') + '</span><b>' + esc(summaryText(tool.description || tool.input_schema, 180)) + '</b></div>') }
function renderGeminiContent(content, i) { return '<div><span>#' + i + ' ' + esc(content.role || '-') + '</span><b>' + esc(summaryText(content.parts, 240)) + '</b></div>' }
function renderGeminiTools(tools) { return renderListSection('Tools', tools, (tool, i) => '<div><span>#' + i + '</span><b>' + esc(summaryText(tool.functionDeclarations || tool, 220)) + '</b></div>') }
function protocolCard(title, rows) { return '<div class="protocol-card"><strong>' + esc(title) + '</strong><div class="protocol-list">' + rows.map(r => '<div><span>' + esc(r[0]) + '</span><b>' + esc(fmtMaybe(r[1])) + '</b></div>').join('') + '</div></div>' }
function len(value) { return Array.isArray(value) ? value.length : value === undefined || value === null ? undefined : 1 }
function renderRawJsonSection(value) { return '<details class="raw-json"><summary>查看原始 JSON</summary>' + jsonPanel(value) + '</details>' }
function bodyPanel(body) {
  const value = parseBodyContent(body)
  if (value === null) return '<p class="muted">无数据</p>'
  return typeof value === 'string' ? '<pre>' + esc(value) + '</pre>' : jsonPanel(value)
}
function jsonPanel(value) { return '<div class="json-tree">' + renderJsonTree(value, 'root') + '</div>' }
function renderJsonTree(value, label) {
  if (Array.isArray(value)) return '<details open><summary>' + esc(label) + ' <span class="muted">[' + value.length + ']</span></summary>' + value.map((v, i) => renderJsonTree(v, String(i))).join('') + '</details>'
  if (value && typeof value === 'object') return '<details open><summary>' + esc(label) + ' <span class="muted">{}</span></summary>' + Object.keys(value).map(k => renderJsonTree(value[k], k)).join('') + '</details>'
  return '<div class="json-row"><span class="json-key">' + esc(label) + '</span>: ' + renderJsonScalar(value) + '</div>'
}
function renderJsonScalar(value) {
  if (value === null || value === undefined) return '<span class="json-null">-</span>'
  if (typeof value === 'string') return '<span class="json-string">"' + esc(value) + '"</span>'
  if (typeof value === 'number') return '<span class="json-number">' + fmt(value) + '</span>'
  if (typeof value === 'boolean') return '<span class="json-bool">' + String(value) + '</span>'
  return esc(String(value))
}
</script>
</body>
</html>`
