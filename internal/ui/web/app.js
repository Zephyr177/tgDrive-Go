/* TG网盘 —— 自研单页前端（原生 JS，无构建步骤、无外部依赖）。 */
(function () {
  'use strict';

  /* ------------------------------------------------------------------ *
   * 常量与全局状态
   * ------------------------------------------------------------------ */

  var TOKEN_KEY = 'tgdrive_token';
  var USER_KEY = 'tgdrive_user';
  var THEME_KEY = 'tgdrive_theme';
  var PAGE_SIZES = [10, 20, 50];
  var ROLE_LABELS = { admin: '管理员', user: '普通用户', visitor: '访客' };
  var SETTING_LABELS = {
    allow_registration: '允许用户注册',
    bot_keep_alive: '机器人保活'
  };
  var MONITOR_LABELS = {
    uptime: '运行时长',
    uptimeSeconds: '运行时长',
    goVersion: 'Go 版本',
    version: '版本',
    os: '操作系统',
    arch: '架构',
    cpuCount: 'CPU 核心数',
    goroutines: '协程数',
    memoryAlloc: '内存占用',
    memoryTotal: '内存总量',
    memorySys: '系统内存',
    heapAlloc: '堆内存',
    heapInuse: '堆使用',
    gcCount: 'GC 次数',
    database: '数据库',
    dbPath: '数据库路径',
    dbSize: '数据库大小',
    fileCount: '文件数量',
    userCount: '用户数量',
    uploadDir: '上传目录',
    diskFree: '磁盘剩余',
    diskTotal: '磁盘总量',
    startTime: '启动时间',
    timestamp: '当前时间',
    status: '状态'
  };

  var state = {
    token: null,
    user: null,
    route: '',
    cleanup: null,
    uploadAllowed: false,
    ws: null,
    wsState: 'off',
    wsAttempts: 0,
    wsTimers: [],
    wsStatusNode: null,
    progress: new Map(),
    queue: [],
    results: [],
    uploading: false,
    files: { page: 1, size: 10, keyword: '', total: 0, records: [] },
    configs: [],
    monitorTimer: null
  };

  /* ------------------------------------------------------------------ *
   * 基础工具
   * ------------------------------------------------------------------ */

  function h(tag, attrs) {
    var el = document.createElement(tag);
    var kids = Array.prototype.slice.call(arguments, 2);
    if (attrs) {
      Object.keys(attrs).forEach(function (key) {
        var val = attrs[key];
        if (val === null || val === undefined || val === false) return;
        if (key === 'class') el.className = val;
        else if (key === 'text') el.textContent = val;
        else if (key === 'value') el.value = val;
        else if (key === 'dataset') Object.assign(el.dataset, val);
        else if (key === 'checked' || key === 'disabled' || key === 'selected' || key === 'multiple' || key === 'readOnly') el[key] = !!val;
        else if (key.indexOf('on') === 0 && typeof val === 'function') el.addEventListener(key.slice(2), val);
        else el.setAttribute(key, val);
      });
    }
    append(el, kids);
    return el;
  }

  function append(parent, kids) {
    kids.forEach(function (kid) {
      if (kid === null || kid === undefined || kid === false || kid === true) return;
      if (Array.isArray(kid)) { append(parent, kid); return; }
      if (kid instanceof Node) { parent.appendChild(kid); return; }
      parent.appendChild(document.createTextNode(String(kid)));
    });
  }

  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }

  function toast(msg, kind, ms) {
    var box = document.getElementById('toasts');
    if (!box) return;
    var item = h('div', { class: 'toast' + (kind ? ' ' + kind : ''), text: msg });
    box.appendChild(item);
    setTimeout(function () {
      item.style.opacity = '0';
      setTimeout(function () { if (item.parentNode) item.parentNode.removeChild(item); }, 200);
    }, ms || 2600);
  }

  function apiError(msg) {
    var err = new Error(msg);
    err.apiMessage = msg;
    return err;
  }

  function errText(err) {
    if (!err) return '操作失败';
    return err.apiMessage || err.message || '操作失败';
  }

  function pad2(n) { return n < 10 ? '0' + n : String(n); }

  function formatDate(unixSeconds) {
    if (!unixSeconds) return '-';
    var d = new Date(Number(unixSeconds) * 1000);
    if (isNaN(d.getTime())) return '-';
    return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
      ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
  }

  function formatBytes(bytes) {
    var n = Number(bytes);
    if (!isFinite(n) || n <= 0) return '-';
    var units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n = n / 1024; i++; }
    return (i === 0 ? n : n.toFixed(n >= 10 ? 1 : 2)) + ' ' + units[i];
  }

  function formatDuration(seconds) {
    var s = Math.floor(Number(seconds));
    if (!isFinite(s) || s < 0) return String(seconds);
    var d = Math.floor(s / 86400); s -= d * 86400;
    var hh = Math.floor(s / 3600); s -= hh * 3600;
    var mm = Math.floor(s / 60); s -= mm * 60;
    var parts = [];
    if (d) parts.push(d + ' 天');
    if (hh || d) parts.push(hh + ' 小时');
    if (mm || hh || d) parts.push(mm + ' 分');
    parts.push(s + ' 秒');
    return parts.join(' ');
  }

  function absoluteUrl(url) {
    if (!url) return '';
    try { return new URL(url, window.location.href).href; } catch (e) { return url; }
  }

  function fileSizeText(rec) {
    if (rec.size) return rec.size;
    if (rec.fullSize) return formatBytes(rec.fullSize);
    return '-';
  }

  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    return new Promise(function (resolve, reject) {
      var ta = h('textarea', { value: text, readOnly: true });
      ta.style.position = 'fixed';
      ta.style.left = '-9999px';
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); resolve(); } catch (e) { reject(e); }
      finally { document.body.removeChild(ta); }
    });
  }

  function copyThen(text, label) {
    copyText(text).then(function () {
      toast((label || '内容') + '已复制', 'ok');
    }).catch(function () {
      toast('复制失败，请手动复制', 'error');
    });
  }

  function markdownLink(rec) {
    return '![' + rec.fileName + '](' + absoluteUrl(rec.downloadUrl) + ')';
  }

  function downloadBlob(path, filename) {
    return fetch(path, { headers: authHeaders() }).then(function (res) {
      if (res.status === 401) { handleUnauthorized(); throw apiError('登录已过期，请重新登录'); }
      if (!res.ok) throw apiError('下载失败 (' + res.status + ')');
      return res.blob();
    }).then(function (blob) {
      var url = URL.createObjectURL(blob);
      var a = h('a', { href: url, download: filename });
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(function () { URL.revokeObjectURL(url); }, 4000);
    });
  }

  function roleLabel(role) { return ROLE_LABELS[role] || role || '未知'; }

  /* ------------------------------------------------------------------ *
   * API 层（统一信封 {code,msg,data}，Bearer 令牌）
   * ------------------------------------------------------------------ */

  function authHeaders() {
    return state.token ? { Authorization: 'Bearer ' + state.token } : {};
  }

  function api(path, opts) {
    opts = opts || {};
    var headers = Object.assign({}, authHeaders(), opts.headers || {});
    var body = opts.body;
    if (body !== undefined && body !== null && !(body instanceof FormData)) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(body);
    }
    return fetch(path, { method: opts.method || 'GET', headers: headers, body: body })
      .catch(function () { throw apiError('网络错误，请检查网络连接'); })
      .then(function (res) {
        if (res.status === 401) {
          handleUnauthorized();
          throw apiError('登录已过期，请重新登录');
        }
        var ctype = res.headers.get('content-type') || '';
        if (ctype.indexOf('application/json') === -1) {
          if (!res.ok) throw apiError('请求失败 (' + res.status + ')');
          return null;
        }
        return res.json().then(function (payload) {
          if (payload && payload.code === 1) return payload.data === undefined ? null : payload.data;
          if (payload && payload.code === 401) {
            handleUnauthorized();
            throw apiError(payload.msg || '登录已过期，请重新登录');
          }
          throw apiError((payload && payload.msg) || '操作失败');
        }, function () { throw apiError('响应解析失败'); });
      });
  }

  function handleUnauthorized() {
    if (!state.token) return;
    setSession(null);
    stopRealtime();
    toast('登录已过期，请重新登录', 'error');
    navigate('/login');
  }

  /* ------------------------------------------------------------------ *
   * 会话与主题
   * ------------------------------------------------------------------ */

  function setSession(data) {
    if (!data || !data.token) {
      state.token = null;
      state.user = null;
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(USER_KEY);
      return;
    }
    state.token = data.token;
    state.user = {
      UserId: data.UserId,
      role: data.role,
      username: data.username,
      email: data.email,
      expireAt: data.expireAt
    };
    localStorage.setItem(TOKEN_KEY, state.token);
    localStorage.setItem(USER_KEY, JSON.stringify(state.user));
  }

  function restoreSession() {
    var token = localStorage.getItem(TOKEN_KEY);
    var raw = localStorage.getItem(USER_KEY);
    if (!token) return false;
    var user = null;
    try { user = raw ? JSON.parse(raw) : null; } catch (e) { user = null; }
    state.token = token;
    state.user = user || {};
    return true;
  }

  function isAdmin() { return !!(state.user && state.user.role === 'admin'); }

  function logout(expired) {
    if (state.token) {
      fetch('/api/auth/logout', { method: 'POST', headers: authHeaders() }).catch(function () {});
    }
    setSession(null);
    stopRealtime();
    if (!expired) toast('已退出登录', 'ok');
    navigate('/login');
  }

  function themeMode() { return localStorage.getItem(THEME_KEY) || 'auto'; }

  function applyTheme() {
    var mode = themeMode();
    var dark = mode === 'dark' ||
      (mode === 'auto' && window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
    document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');
  }

  if (window.matchMedia) {
    var mq = window.matchMedia('(prefers-color-scheme: dark)');
    var onScheme = function () { if (themeMode() === 'auto') applyTheme(); };
    if (mq.addEventListener) mq.addEventListener('change', onScheme);
    else if (mq.addListener) mq.addListener(onScheme);
  }

  /* ------------------------------------------------------------------ *
   * 实时上传进度 WebSocket
   * ------------------------------------------------------------------ */

  function wsURL() {
    var proto = window.location.protocol === 'https:' ? 'wss://' : 'ws://';
    return proto + window.location.host + '/ws/upload-progress';
  }

  function setWsStatus(status) {
    state.wsState = status;
    if (!state.wsStatusNode) return;
    var labels = { on: '实时进度：已连接', off: '实时进度：已断开', connecting: '实时进度：连接中…' };
    state.wsStatusNode.textContent = labels[status] || '';
    state.wsStatusNode.className = 'ws-state ' + (status === 'on' ? 'on' : 'off');
  }

  function clearWsTimers() {
    state.wsTimers.forEach(function (t) { clearTimeout(t); });
    state.wsTimers = [];
  }

  function startRealtime() {
    if (!state.token) return;
    stopRealtime(true);
    openSocket();
  }

  function openSocket() {
    var ws;
    try { ws = new WebSocket(wsURL()); } catch (e) { scheduleReconnect(); return; }
    state.ws = ws;
    setWsStatus('connecting');

    ws.onopen = function () {
      state.wsAttempts = 0;
      setWsStatus('on');
      state.wsTimers.push(setInterval(function () {
        sendWs({ type: 'ping' });
      }, 30000));
      sendWs({ type: 'ping' });
      state.wsTimers.push(setTimeout(function () {
        if (state.ws && state.ws.readyState === WebSocket.OPEN) {
          try { state.ws.close(); } catch (e) { /* ignore */ }
        }
      }, 90000));
    };

    ws.onmessage = function (ev) {
      var msg;
      try { msg = JSON.parse(ev.data); } catch (e) { return; }
      handleWsMessage(msg);
    };

    ws.onerror = function () { setWsStatus('off'); };

    ws.onclose = function () {
      state.ws = null;
      clearWsTimers();
      setWsStatus('off');
      scheduleReconnect();
    };
  }

  function sendWs(obj) {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
      try { state.ws.send(JSON.stringify(obj)); } catch (e) { /* ignore */ }
    }
  }

  function scheduleReconnect() {
    if (!state.token || document.visibilityState === 'hidden') return;
    if (state.wsAttempts >= 10) return;
    var delay = Math.min(30000, 1000 * Math.pow(2, state.wsAttempts));
    state.wsAttempts++;
    state.wsTimers.push(setTimeout(function () { openSocket(); }, delay));
  }

  function handleWsMessage(msg) {
    if (!msg || !msg.type) return;
    if (msg.type === 'pong') return;
    if (msg.type === 'upload_progress') {
      state.progress.set(msg.fileName, {
        percentage: Number(msg.percentage) || 0,
        currentChunk: msg.currentChunk,
        totalChunks: msg.totalChunks,
        status: 'uploading'
      });
    } else if (msg.type === 'upload_complete') {
      var cur = state.progress.get(msg.fileName) || { percentage: 100 };
      cur.percentage = 100;
      cur.status = 'done';
      state.progress.set(msg.fileName, cur);
    } else if (msg.type === 'upload_error') {
      var item = state.progress.get(msg.fileName) || { percentage: 0 };
      item.status = 'error';
      item.error = msg.error;
      state.progress.set(msg.fileName, item);
    } else {
      return;
    }
    renderRealtimeProgress();
  }

  function stopRealtime(keepStatusNode) {
    clearWsTimers();
    if (state.ws) {
      try { state.ws.close(); } catch (e) { /* ignore */ }
      state.ws = null;
    }
    state.wsAttempts = 0;
    if (!keepStatusNode) {
      state.progress.clear();
      renderRealtimeProgress();
    }
  }

  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible' && state.token) {
      if (!state.ws) { state.wsAttempts = 0; openSocket(); }
    }
  });

  /* ------------------------------------------------------------------ *
   * 通用 UI 片段
   * ------------------------------------------------------------------ */

  function alertBox(kind, text) {
    return h('div', { class: 'alert ' + kind, text: text });
  }

  function button(text, onClick, cls) {
    return h('button', { class: 'btn' + (cls ? ' ' + cls : ''), type: 'button', text: text, onclick: onClick });
  }

  function field(label, input) {
    return h('label', { class: 'field' }, h('span', { text: label }), input);
  }

  function input(attrs) { return h('input', Object.assign({ type: 'text' }, attrs || {})); }

  function openDialog(title, bodyEl, onOk, okText) {
    var errBox = h('div', { class: 'alert error hidden' });
    var okBtn = h('button', { class: 'btn primary', type: 'button', text: okText || '确定' });
    var cancelBtn = h('button', { class: 'btn', type: 'button', text: '取消' });
    var mask = h('div', { class: 'modal-mask' },
      h('div', { class: 'card modal' },
        h('h3', { text: title }),
        errBox,
        bodyEl,
        h('div', { class: 'row', style: 'justify-content:flex-end' }, cancelBtn, okBtn)));

    function close() { if (mask.parentNode) mask.parentNode.removeChild(mask); }
    cancelBtn.addEventListener('click', close);
    mask.addEventListener('click', function (e) { if (e.target === mask) close(); });
    okBtn.addEventListener('click', function () {
      var result;
      try { result = onOk(); } catch (e) {
        errBox.className = 'alert error';
        errBox.textContent = errText(e);
        return;
      }
      if (result && typeof result.then === 'function') {
        okBtn.disabled = true;
        result.then(function () { close(); }, function (e) {
          okBtn.disabled = false;
          errBox.className = 'alert error';
          errBox.textContent = errText(e);
        });
      } else {
        close();
      }
    });
    document.body.appendChild(mask);
    return close;
  }

  /* ------------------------------------------------------------------ *
   * 路由
   * ------------------------------------------------------------------ */

  var ADMIN_ROUTES = ['/admin/configs', '/admin/users', '/admin/settings', '/admin/webdav',
    '/admin/keepalive', '/admin/backup', '/admin/monitor'];

  function navigate(route) {
    if (('#' + route) === window.location.hash) { onRoute(); return; }
    window.location.hash = '#' + route;
  }

  function currentRoute() {
    var hash = window.location.hash || '';
    var route = hash.replace(/^#/, '');
    if (!route || route === '/') route = state.token ? '/files' : '/login';
    return route;
  }

  function onRoute() {
    var route = currentRoute();
    if (!state.token && route !== '/login' && route !== '/register') {
      route = '/login';
      window.location.hash = '#/login';
      return;
    }
    if (state.token && (route === '/login' || route === '/register')) {
      route = '/files';
      window.location.hash = '#/files';
      return;
    }
    if (ADMIN_ROUTES.indexOf(route) >= 0 && !isAdmin()) {
      toast('无权限访问该页面', 'error');
      route = '/files';
      window.location.hash = '#/files';
      return;
    }
    state.route = route;
    mount(route);
  }

  window.addEventListener('hashchange', onRoute);

  function mount(route) {
    if (typeof state.cleanup === 'function') {
      try { state.cleanup(); } catch (e) { /* ignore */ }
      state.cleanup = null;
    }
    state.wsStatusNode = null;
    var app = document.getElementById('app');
    clear(app);

    var pageEl;
    if (route === '/login') { pageEl = views.login(); }
    else if (route === '/register') { pageEl = views.register(); }
    else {
      var main = h('main', { class: 'page' });
      app.appendChild(topbar());
      app.appendChild(main);
      var view = views[route] || views.notFound;
      view(main);
      return;
    }
    app.appendChild(pageEl);
  }

  /* ------------------------------------------------------------------ *
   * 顶栏 / 导航
   * ------------------------------------------------------------------ */

  function navLink(route, label) {
    return h('a', {
      href: '#' + route,
      class: state.route === route ? 'active' : '',
      text: label
    });
  }

  function topbar() {
    var nav = h('nav', { class: 'nav' },
      navLink('/files', '我的文件'),
      navLink('/upload', '上传文件'),
      navLink('/password', '修改密码'));
    if (isAdmin()) {
      nav.appendChild(navLink('/admin/configs', '配置管理'));
      nav.appendChild(navLink('/admin/users', '用户管理'));
      nav.appendChild(navLink('/admin/settings', '系统设置'));
      nav.appendChild(navLink('/admin/webdav', 'WebDAV'));
      nav.appendChild(navLink('/admin/keepalive', '机器人保活'));
      nav.appendChild(navLink('/admin/backup', '数据库备份'));
      nav.appendChild(navLink('/admin/monitor', '系统监控'));
    }

    var themeSelect = h('select', {
      title: '主题',
      value: themeMode(),
      onchange: function (e) { localStorage.setItem(THEME_KEY, e.target.value); applyTheme(); }
    },
      h('option', { value: 'auto', text: '跟随系统' }),
      h('option', { value: 'light', text: '亮色模式' }),
      h('option', { value: 'dark', text: '暗色模式' }));
    themeSelect.value = themeMode();

    var userArea = h('div', { class: 'userarea' },
      h('span', { class: 'username', text: (state.user && state.user.username) || '用户' }),
      h('span', { class: 'tag' + (isAdmin() ? ' admin' : ''), text: roleLabel(state.user && state.user.role) }),
      themeSelect,
      button('退出登录', function () { logout(false); }, 'small'));

    return h('header', { class: 'topbar' },
      h('div', { class: 'topbar-inner' },
        h('a', { class: 'brand', href: '#/files' }, 'TG', h('span', { class: 'dot', text: '网盘' })),
        nav,
        userArea));
  }

  function pageHead(title, sub, actions) {
    return h('div', { class: 'page-head' },
      h('div', null, h('h1', { text: title }), sub ? h('div', { class: 'sub', text: sub }) : null),
      actions ? h('div', { class: 'row' }, actions) : null);
  }

  /* ------------------------------------------------------------------ *
   * 视图：登录 / 注册
   * ------------------------------------------------------------------ */

  var views = {};

  views.login = function () {
    var errBox = h('div', { class: 'alert error hidden' });
    var username = input({ placeholder: '请输入用户名', autocomplete: 'username' });
    var password = input({ type: 'password', placeholder: '请输入密码', autocomplete: 'current-password' });
    var submit = h('button', { class: 'btn primary block', type: 'submit', text: '登 录' });
    var registerLink = h('a', { href: '#/register', text: '立即注册', class: 'hidden' });

    var form = h('form', { class: 'card auth-card' },
      h('h1', { text: 'TG网盘' }),
      h('div', { class: 'sub', text: '欢迎回来，请登录后继续' }),
      errBox,
      field('用户名', username),
      field('密码', password),
      submit,
      h('div', { class: 'auth-links' },
        h('span', null, '没有账户？', registerLink),
        h('span', { class: 'muted', text: '忘记密码请联系管理员' })));

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      errBox.className = 'alert error hidden';
      if (!username.value.trim()) {
        errBox.className = 'alert error';
        errBox.textContent = '请输入用户名';
        return;
      }
      if (!password.value) {
        errBox.className = 'alert error';
        errBox.textContent = '请输入密码';
        return;
      }
      submit.disabled = true;
      submit.textContent = '登录中…';
      api('/api/auth/login', { method: 'POST', body: { username: username.value.trim(), password: password.value } })
        .then(function (data) {
          setSession(data);
          startRealtime();
          toast('登录成功', 'ok');
          navigate('/files');
        })
        .catch(function (err) {
          errBox.className = 'alert error';
          errBox.textContent = errText(err);
        })
        .then(function () {
          submit.disabled = false;
          submit.textContent = '登 录';
        });
    });

    api('/api/setting/registration-status').then(function (data) {
      if (data && data.isRegistrationAllowed) registerLink.className = '';
    }).catch(function () {
      registerLink.className = 'hidden';
    });

    return h('div', { class: 'auth-wrap' }, form);
  };

  views.register = function () {
    var errBox = h('div', { class: 'alert error hidden' });
    var username = input({ placeholder: '用户名（3-20 个字符）', autocomplete: 'username' });
    var password = input({ type: 'password', placeholder: '密码（至少 5 位）', autocomplete: 'new-password' });
    var confirm = input({ type: 'password', placeholder: '请再次输入密码', autocomplete: 'new-password' });
    var email = input({ type: 'email', placeholder: '邮箱（可选）' });
    var submit = h('button', { class: 'btn primary block', type: 'submit', text: '注册' });

    var form = h('form', { class: 'card auth-card' },
      h('h1', { text: '创建账户' }),
      h('div', { class: 'sub', text: '注册以开始使用 TG网盘' }),
      errBox,
      field('用户名', username),
      field('密码', password),
      field('确认密码', confirm),
      field('邮箱（可选）', email),
      submit,
      h('div', { class: 'auth-links center' },
        h('span', null, '已有账户？', h('a', { href: '#/login', text: '立即登录' }))));

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      errBox.className = 'alert error hidden';
      var name = username.value.trim();
      var fail = null;
      if (name.length < 3 || name.length > 20) fail = '用户名长度需为 3-20 个字符';
      else if (password.value.length < 5) fail = '密码长度至少为 5 个字符';
      else if (password.value !== confirm.value) fail = '两次输入密码不一致';
      else if (email.value && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value)) fail = '请输入正确的邮箱地址';
      if (fail) {
        errBox.className = 'alert error';
        errBox.textContent = fail;
        return;
      }
      submit.disabled = true;
      submit.textContent = '注册中…';
      api('/api/auth/register', {
        method: 'POST',
        body: { username: name, password: password.value, confirmPassword: confirm.value, email: email.value.trim() }
      }).then(function (data) {
        if (data && data.token) {
          setSession(data);
          startRealtime();
          toast('注册成功，已自动登录', 'ok');
          navigate('/files');
          return;
        }
        toast('注册成功，请登录', 'ok');
        navigate('/login');
      }).catch(function (err) {
        errBox.className = 'alert error';
        errBox.textContent = errText(err);
      }).then(function () {
        submit.disabled = false;
        submit.textContent = '注册';
      });
    });

    api('/api/setting/registration-status').then(function (data) {
      if (!data || !data.isRegistrationAllowed) {
        errBox.className = 'alert warn';
        errBox.textContent = '当前未开放注册，请联系管理员';
        submit.disabled = true;
      }
    }).catch(function () {
      errBox.className = 'alert warn';
      errBox.textContent = '获取注册状态失败';
    });

    return h('div', { class: 'auth-wrap' }, form);
  };

  /* ------------------------------------------------------------------ *
   * 视图：文件列表
   * ------------------------------------------------------------------ */

  views['/files'] = function (root) {
    var keyword = input({ type: 'search', placeholder: '搜索文件名', style: 'min-width:200px' });
    var sizeSelect = h('select', { title: '每页数量' },
      PAGE_SIZES.map(function (n) {
        return h('option', { value: String(n), text: n + ' 条/页', selected: n === state.files.size });
      }));
    var tbody = h('tbody');
    var pager = h('div', { class: 'pager' });
    var emptyBox = h('div', { class: 'empty hidden', text: '暂无文件' });

    var table = h('table', { class: 'data' },
      h('thead', null, h('tr', null,
        h('th', { text: '文件名' }),
        h('th', { text: '状态' }),
        h('th', { text: '大小' }),
        h('th', { text: '上传时间' }),
        h('th', { text: '上传者' }),
        h('th', { text: '操作' }))),
      tbody);

    function load() {
      var q = 'page=' + state.files.page + '&size=' + state.files.size +
        (state.files.keyword ? '&keyword=' + encodeURIComponent(state.files.keyword) : '');
      return api('/api/file-list?' + q).then(function (data) {
        var total = data && data.total ? Number(data.total) : 0;
        var records = (data && data.records) || [];
        state.files.total = total;
        state.files.records = records;
        renderRows(records);
        renderPager(total);
      }).catch(function (err) {
        toast(errText(err), 'error');
      });
    }

    function renderRows(records) {
      clear(tbody);
      emptyBox.className = 'empty' + (records.length ? ' hidden' : '');
      records.forEach(function (rec) {
        tbody.appendChild(renderFileRow(rec, load));
      });
    }

    function renderPager(total) {
      clear(pager);
      var pages = Math.max(1, Math.ceil(total / state.files.size));
      pager.appendChild(h('span', { class: 'muted small', text: '共 ' + total + ' 条，第 ' + state.files.page + '/' + pages + ' 页' }));
      var prev = button('上一页', function () {
        if (state.files.page > 1) { state.files.page--; load(); }
      }, 'small');
      prev.disabled = state.files.page <= 1;
      var next = button('下一页', function () {
        if (state.files.page < pages) { state.files.page++; load(); }
      }, 'small');
      next.disabled = state.files.page >= pages;
      pager.appendChild(prev);
      pager.appendChild(next);
    }

    keyword.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        e.preventDefault();
        state.files.keyword = keyword.value.trim();
        state.files.page = 1;
        load();
      }
    });

    sizeSelect.addEventListener('change', function () {
      state.files.size = Number(sizeSelect.value) || 10;
      state.files.page = 1;
      load();
    });

    root.appendChild(pageHead('文件列表', '查看、下载与分享已上传的文件',
      [button('搜索', function () {
        state.files.keyword = keyword.value.trim();
        state.files.page = 1;
        load();
      }), button('刷新', load)]));
    root.appendChild(h('div', { class: 'card' },
      h('div', { class: 'card-head' },
        h('div', { class: 'row' }, keyword, sizeSelect),
        h('div', { class: 'row' }, navLink('/upload', '上传文件'))),
      h('div', { class: 'table-wrap' }, table, emptyBox),
      pager));

    load();
  };

  function renderFileRow(rec, reload) {
    var actions = h('td', { class: 'actions' });

    if (!rec.dir) {
      actions.appendChild(button('下载', function () {
        window.open(absoluteUrl(rec.downloadUrl), '_blank');
      }, 'small'));

      actions.appendChild(button('复制 Markdown', function () {
        copyThen(markdownLink(rec), 'Markdown 链接');
      }, 'small'));

      actions.appendChild(button(rec.public ? '设为私有' : '设为公开', function (e) {
        var btn = e.currentTarget;
        btn.disabled = true;
        api('/api/file/' + encodeURIComponent(rec.fileId) + '/public', {
          method: 'PUT',
          body: { isPublic: !rec.public }
        }).then(function () {
          toast('状态已更新', 'ok');
          reload();
        }).catch(function (err) {
          btn.disabled = false;
          toast(errText(err), 'error');
        });
      }, 'small'));
    }

    actions.appendChild(button('删除', function () {
      if (!window.confirm('确定要删除文件「' + rec.fileName + '」吗？此操作不可撤销')) return;
      api('/api/file/' + encodeURIComponent(rec.fileId), { method: 'DELETE' }).then(function () {
        toast('删除成功', 'ok');
        reload();
      }).catch(function (err) {
        toast(errText(err), 'error');
      });
    }, 'small danger'));

    var statusTag = rec.dir
      ? h('span', { class: 'tag dir', text: '目录' })
      : h('span', { class: 'tag ' + (rec.public ? 'public' : 'private'), text: rec.public ? '公开' : '私有' });

    return h('tr', null,
      h('td', { class: 'break' }, rec.fileName || '-'),
      h('td', null, statusTag),
      h('td', { class: 'nowrap', text: rec.dir ? '-' : fileSizeText(rec) }),
      h('td', { class: 'nowrap', text: formatDate(rec.uploadTime) }),
      h('td', { class: 'nowrap', text: rec.uploader || '-' }),
      actions);
  }

  /* ------------------------------------------------------------------ *
   * 视图：上传
   * ------------------------------------------------------------------ */

  views['/upload'] = function (root) {
    var permBox = h('div', { class: 'alert info', text: '正在检查上传权限…' });
    var fileInput = h('input', { type: 'file', multiple: true, class: 'hidden' });
    var dropzone = h('div', { class: 'dropzone' },
      h('div', null, '将文件拖到此处，或点击选择文件'),
      h('div', { class: 'small', text: '支持多文件上传' }));
    var queueBox = h('div');
    var resultsBox = h('div');
    var realtimeBox = h('div', { class: 'realtime-box' });
    var startBtn = button('开始上传', null, 'primary');
    var clearBtn = button('清空已完成', null, 'small');
    var wsNode = h('span', { class: 'ws-state off' });
    state.wsStatusNode = wsNode;
    setWsStatus(state.wsState);

    startBtn.disabled = true;
    startBtn.addEventListener('click', function () { startUpload(); });
    clearBtn.addEventListener('click', function () {
      state.queue = state.queue.filter(function (it) { return it.status === 'pending' || it.status === 'uploading'; });
      state.results = [];
      renderQueue();
      renderResults();
    });

    dropzone.addEventListener('click', function () { fileInput.click(); });
    dropzone.addEventListener('dragover', function (e) { e.preventDefault(); dropzone.classList.add('over'); });
    dropzone.addEventListener('dragleave', function () { dropzone.classList.remove('over'); });
    dropzone.addEventListener('drop', function (e) {
      e.preventDefault();
      dropzone.classList.remove('over');
      if (e.dataTransfer && e.dataTransfer.files) addFiles(e.dataTransfer.files);
    });
    fileInput.addEventListener('change', function () {
      addFiles(fileInput.files);
      fileInput.value = '';
    });

    function addFiles(list) {
      if (state.uploading) {
        toast('当前正在上传，请稍后再添加文件', 'error');
        return;
      }
      Array.prototype.forEach.call(list, function (file) {
        state.queue.push({ file: file, xhr: 0, status: 'pending', message: '' });
      });
      startBtn.disabled = !state.uploadAllowed || state.queue.length === 0;
      renderQueue();
    }

    function renderQueue() {
      clear(queueBox);
      if (!state.queue.length) {
        queueBox.appendChild(h('div', { class: 'empty', text: '暂无待上传文件' }));
        return;
      }
      state.queue.forEach(function (item) {
        var pct = item.status === 'done' ? 100 : item.xhr;
        var bar = h('div', { class: 'bar' + (item.status === 'done' ? ' done' : item.status === 'error' ? ' err' : '') },
          h('i', { style: 'width:' + pct + '%' }));
        var stateText = {
          pending: '等待上传',
          uploading: '上传中 ' + pct + '%',
          done: '已完成',
          error: '失败：' + (item.message || '未知错误')
        }[item.status];
        queueBox.appendChild(h('div', { class: 'progress-item' },
          h('div', { class: 'progress-head' },
            h('span', { class: 'nm', title: item.file.name, text: item.file.name }),
            h('span', { class: 'muted', text: stateText })),
          bar));
      });
    }

    function renderResults() {
      clear(resultsBox);
      if (!state.results.length) return;
      var tbody = h('tbody');
      state.results.forEach(function (res) {
        var full = absoluteUrl(res.downloadLink);
        var md = '![' + res.fileName + '](' + full + ')';
        tbody.appendChild(h('tr', null,
          h('td', { class: 'break', text: res.fileName }),
          h('td', { class: 'actions' },
            button('下载', function () { window.open(full, '_blank'); }, 'small'),
            ' ',
            button('复制 Markdown', function () { copyThen(md, 'Markdown 链接'); }, 'small'),
            ' ',
            button('复制链接', function () { copyThen(full, '下载链接'); }, 'small'))));
      });
      resultsBox.appendChild(h('h3', { text: '本次上传结果' }));
      resultsBox.appendChild(h('div', { class: 'table-wrap' },
        h('table', { class: 'data' },
          h('thead', null, h('tr', null, h('th', { text: '文件名' }), h('th', { text: '操作' }))),
          tbody)));
    }

    function startUpload() {
      if (state.uploading) return;
      var pending = state.queue.filter(function (it) { return it.status === 'pending'; });
      if (!pending.length) { toast('没有待上传的文件', 'error'); return; }
      state.uploading = true;
      startBtn.disabled = true;
      var chain = Promise.resolve();
      pending.forEach(function (item) {
        chain = chain.then(function () {
          item.status = 'uploading';
          renderQueue();
          return uploadOne(item);
        });
      });
      chain.then(function () {
        state.uploading = false;
        startBtn.disabled = state.queue.length === 0;
        renderQueue();
        toast('上传任务已结束', 'ok');
      });
    }

    function uploadOne(item) {
      return new Promise(function (resolve) {
        var xhr = new XMLHttpRequest();
        var fd = new FormData();
        fd.append('file', item.file);
        xhr.open('POST', '/api/upload');
        if (state.token) xhr.setRequestHeader('Authorization', 'Bearer ' + state.token);
        xhr.upload.onprogress = function (e) {
          if (e.lengthComputable) {
            item.xhr = Math.round(e.loaded / e.total * 100);
            renderQueue();
          }
        };
        xhr.onload = function () {
          if (xhr.status === 401) {
            handleUnauthorized();
            item.status = 'error';
            item.message = '登录已过期';
            resolve();
            return;
          }
          var payload = null;
          try { payload = JSON.parse(xhr.responseText); } catch (e) { payload = null; }
          if (payload && payload.code === 1 && payload.data) {
            item.status = 'done';
            item.xhr = 100;
            state.results.push({ fileName: payload.data.fileName || item.file.name, downloadLink: payload.data.downloadLink || '' });
            renderResults();
          } else {
            item.status = 'error';
            item.message = (payload && payload.msg) || '上传失败';
            toast(item.file.name + '：' + item.message, 'error');
          }
          renderQueue();
          resolve();
        };
        xhr.onerror = function () {
          item.status = 'error';
          item.message = '网络错误';
          toast(item.file.name + '：网络错误', 'error');
          renderQueue();
          resolve();
        };
        xhr.send(fd);
      });
    }

    api('/api/upload/permission-check').then(function () {
      state.uploadAllowed = true;
      permBox.className = 'alert ok';
      permBox.textContent = '已获得上传权限';
      startBtn.disabled = state.queue.length === 0;
    }).catch(function (err) {
      state.uploadAllowed = false;
      permBox.className = 'alert error';
      permBox.textContent = '无上传权限：' + errText(err);
      startBtn.disabled = true;
    });

    renderQueue();
    renderResults();

    root.appendChild(pageHead('文件上传', '拖拽或选择文件上传到 Telegram 存储',
      [h('span', null, wsNode), button('刷新文件列表', function () { navigate('/files'); }, 'small')]));
    root.appendChild(h('div', { class: 'card' },
      permBox,
      dropzone,
      fileInput,
      h('div', { class: 'row', style: 'margin-top:1rem' }, startBtn, clearBtn)));
    root.appendChild(h('div', { class: 'card' },
      h('h3', { text: '上传进度（本地）' }),
      queueBox));
    root.appendChild(h('div', { class: 'card' },
      h('h3', { text: '实时进度（服务器）' }),
      h('div', { class: 'small muted', text: '通过 WebSocket 接收服务端分片进度；断线时会自动重连。' }),
      realtimeBox));
    root.appendChild(h('div', { class: 'card' }, resultsBox));

    renderRealtimeProgress();
  };

  function renderRealtimeProgress() {
    var box = document.querySelector('.realtime-box');
    if (!box) return;
    clear(box);
    if (!state.progress.size) {
      box.appendChild(h('div', { class: 'empty', text: '暂无实时进度' }));
      return;
    }
    state.progress.forEach(function (item, name) {
      var pct = Math.max(0, Math.min(100, Math.round(item.percentage)));
      var label = item.status === 'done' ? '已完成' :
        item.status === 'error' ? ('失败：' + (item.error || '未知错误')) :
          ('上传中 ' + pct + '%' + (item.totalChunks ? '（分片 ' + (item.currentChunk || 0) + '/' + item.totalChunks + '）' : ''));
      box.appendChild(h('div', { class: 'progress-item' },
        h('div', { class: 'progress-head' },
          h('span', { class: 'nm', title: name, text: name }),
          h('span', { class: 'muted', text: label })),
        h('div', { class: 'bar' + (item.status === 'done' ? ' done' : item.status === 'error' ? ' err' : '') },
          h('i', { style: 'width:' + (item.status === 'done' ? 100 : pct) + '%' }))));
    });
  }

  /* ------------------------------------------------------------------ *
   * 视图：修改密码
   * ------------------------------------------------------------------ */

  views['/password'] = function (root) {
    var errBox = h('div', { class: 'alert error hidden' });
    var okBox = h('div', { class: 'alert ok hidden' });
    var oldPwd = input({ type: 'password', placeholder: '请输入当前密码', autocomplete: 'current-password' });
    var newPwd = input({ type: 'password', placeholder: '请输入新密码（至少 5 位）', autocomplete: 'new-password' });
    var confirmPwd = input({ type: 'password', placeholder: '请再次输入新密码', autocomplete: 'new-password' });
    var submit = h('button', { class: 'btn primary', type: 'submit', text: '确认修改' });

    var form = h('form', { class: 'card' },
      errBox, okBox,
      field('旧密码', oldPwd),
      field('新密码', newPwd),
      field('确认新密码', confirmPwd),
      h('div', { class: 'row' }, submit));

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      errBox.className = 'alert error hidden';
      okBox.className = 'alert ok hidden';
      if (!oldPwd.value || !newPwd.value) {
        errBox.className = 'alert error';
        errBox.textContent = '请输入完整信息';
        return;
      }
      if (newPwd.value.length < 5) {
        errBox.className = 'alert error';
        errBox.textContent = '新密码长度至少为 5 位';
        return;
      }
      if (newPwd.value !== confirmPwd.value) {
        errBox.className = 'alert error';
        errBox.textContent = '两次输入的密码不一致';
        return;
      }
      submit.disabled = true;
      submit.textContent = '提交中…';
      api('/api/auth/change-password', {
        method: 'POST',
        body: { oldPassword: oldPwd.value, newPassword: newPwd.value }
      }).then(function () {
        okBox.className = 'alert ok';
        okBox.textContent = '密码修改成功，为了安全起见请重新登录';
        toast('密码修改成功，请重新登录', 'ok');
        setTimeout(function () { logout(true); }, 1200);
      }).catch(function (err) {
        errBox.className = 'alert error';
        errBox.textContent = errText(err);
      }).then(function () {
        submit.disabled = false;
        submit.textContent = '确认修改';
      });
    });

    root.appendChild(pageHead('修改密码', '普通用户修改自己的密码'));
    root.appendChild(form);
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：Bot 配置
   * ------------------------------------------------------------------ */

  views['/admin/configs'] = function (root) {
    var name = input({ placeholder: '请输入配置名称' });
    var token = input({ placeholder: '请输入 Bot Token' });
    var target = input({ placeholder: '请输入接收文件的频道/用户' });
    var url = input({ placeholder: '用于生成下载链接的域名（选填）' });
    var pass = input({ placeholder: '访问密码（选填）' });
    var submit = h('button', { class: 'btn primary', type: 'submit', text: '保存并提交' });
    var errBox = h('div', { class: 'alert error hidden' });
    var listBox = h('tbody');

    var form = h('form', { class: 'card' },
      h('h2', { text: 'Bot 配置' }),
      errBox,
      field('名称', name),
      field('Token', token),
      field('目标频道', target),
      field('URL（选填）', url),
      field('访问密码（选填）', pass),
      h('div', { class: 'row' }, submit,
        button('清空表单', function () {
          [name, token, target, url, pass].forEach(function (i) { i.value = ''; });
        })));

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      errBox.className = 'alert error hidden';
      if (!name.value.trim() || !token.value.trim() || !target.value.trim()) {
        errBox.className = 'alert error';
        errBox.textContent = '名称、Token 与目标频道为必填项';
        return;
      }
      submit.disabled = true;
      api('/api/config', {
        method: 'POST',
        body: {
          name: name.value.trim(),
          token: token.value.trim(),
          target: target.value.trim(),
          url: url.value.trim(),
          pass: pass.value
        }
      }).then(function () {
        toast('配置提交成功', 'ok');
        load();
      }).catch(function (err) {
        errBox.className = 'alert error';
        errBox.textContent = errText(err);
      }).then(function () { submit.disabled = false; });
    });

    function load() {
      return api('/api/config/configs').then(function (list) {
        state.configs = list || [];
        renderList();
      }).catch(function (err) {
        toast('获取配置列表失败：' + errText(err), 'error');
      });
    }

    function renderList() {
      clear(listBox);
      if (!state.configs.length) {
        listBox.appendChild(h('tr', null, h('td', { colspan: '6', class: 'empty', text: '暂无配置' })));
        return;
      }
      state.configs.forEach(function (cfg) {
        listBox.appendChild(h('tr', null,
          h('td', { text: cfg.name }),
          h('td', { class: 'mono break', text: cfg.token ? cfg.token.slice(0, 8) + '…' : '-' }),
          h('td', { class: 'break', text: cfg.target || '-' }),
          h('td', { class: 'break', text: cfg.url || '-' }),
          h('td', { text: cfg.pass ? '已设置' : '未设置' }),
          h('td', { class: 'actions' },
            button('加载', function () {
              api('/api/config/' + encodeURIComponent(cfg.name)).then(function (data) {
                name.value = cfg.name;
                token.value = cfg.token || '';
                target.value = cfg.target || '';
                url.value = cfg.url || '';
                pass.value = cfg.pass || '';
                toast('配置加载成功：' + (typeof data === 'string' && data ? data : cfg.name), 'ok');
              }).catch(function (err) { toast(errText(err), 'error'); });
            }, 'small'),
            ' ',
            button('删除', function () {
              if (!window.confirm('确定要删除配置「' + cfg.name + '」吗？此操作不可撤销')) return;
              api('/api/config/' + encodeURIComponent(cfg.name), { method: 'DELETE' }).then(function () {
                toast('配置删除成功', 'ok');
                load();
              }).catch(function (err) { toast(errText(err), 'error'); });
            }, 'small danger'))));
      });
    }

    root.appendChild(pageHead('配置管理', '维护 Telegram Bot 配置'));
    root.appendChild(form);
    root.appendChild(h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('h2', { text: '现有配置' }), button('刷新', load, 'small')),
      h('div', { class: 'table-wrap' },
        h('table', { class: 'data' },
          h('thead', null, h('tr', null,
            h('th', { text: '名称' }), h('th', { text: 'Token' }), h('th', { text: '目标' }),
            h('th', { text: 'URL' }), h('th', { text: '访问密码' }), h('th', { text: '操作' }))),
          listBox))));

    load();
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：用户管理
   * ------------------------------------------------------------------ */

  views['/admin/users'] = function (root) {
    var searchBox = input({ type: 'search', placeholder: '搜索用户名或邮箱', style: 'min-width:200px' });
    var tbody = h('tbody');
    var list = [];

    function render() {
      clear(tbody);
      var kw = searchBox.value.trim().toLowerCase();
      var rows = list.filter(function (u) {
        if (!kw) return true;
        return (u.username || '').toLowerCase().indexOf(kw) >= 0 ||
          (u.email || '').toLowerCase().indexOf(kw) >= 0;
      });
      if (!rows.length) {
        tbody.appendChild(h('tr', null, h('td', { colspan: '5', class: 'empty', text: '暂无用户' })));
        return;
      }
      rows.forEach(function (u) {
        var roleSelect = h('select', { title: '用户角色' },
          ['admin', 'user', 'visitor'].map(function (r) {
            return h('option', { value: r, text: roleLabel(r), selected: u.role === r });
          }));
        roleSelect.addEventListener('change', function () {
          var next = roleSelect.value;
          if (next === u.role) return;
          api('/api/auth/admin/users/' + encodeURIComponent(u.id) + '/role?role=' + encodeURIComponent(next), { method: 'PUT' })
            .then(function () {
              toast('用户角色设置成功', 'ok');
              load();
            }).catch(function (err) {
              toast(errText(err), 'error');
              roleSelect.value = u.role;
            });
        });

        tbody.appendChild(h('tr', null,
          h('td', { text: u.username || '-' }),
          h('td', { class: 'break', text: u.email || '-' }),
          h('td', null, roleSelect),
          h('td', { class: 'nowrap', text: u.lastLoginTime || '从未登录' }),
          h('td', { class: 'actions' },
            button('改密', function () {
              var pwd = input({ type: 'password', placeholder: '请输入新密码（至少 5 位）' });
              openDialog('修改用户密码：' + u.username, h('div', null, field('新密码', pwd)), function () {
                if ((pwd.value || '').length < 5) throw apiError('新密码长度至少为 5 位');
                return api('/api/auth/admin/change-password', {
                  method: 'POST',
                  body: { username: u.username, newPassword: pwd.value }
                }).then(function () { toast('密码修改成功', 'ok'); });
              });
            }, 'small'),
            ' ',
            button('删除', function () {
              if (u.role === 'admin') { toast('不能删除管理员账户', 'error'); return; }
              if (!window.confirm('确定要删除用户「' + u.username + '」吗？此操作不可撤销')) return;
              api('/api/auth/admin/users/' + encodeURIComponent(u.id), { method: 'DELETE' }).then(function () {
                toast('用户删除成功', 'ok');
                load();
              }).catch(function (err) { toast(errText(err), 'error'); });
            }, 'small danger'))));
      });
    }

    function load() {
      return api('/api/auth/admin/users').then(function (users) {
        list = users || [];
        render();
      }).catch(function (err) {
        toast('刷新用户列表失败：' + errText(err), 'error');
      });
    }

    searchBox.addEventListener('input', render);

    root.appendChild(pageHead('用户管理', '查看用户、调整角色、重置密码',
      [button('刷新数据', load, 'small')]));
    root.appendChild(h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('div', { class: 'row' }, searchBox)),
      h('div', { class: 'table-wrap' },
        h('table', { class: 'data' },
          h('thead', null, h('tr', null,
            h('th', { text: '用户名' }), h('th', { text: '邮箱' }), h('th', { text: '角色' }),
            h('th', { text: '最后上线' }), h('th', { text: '操作' }))),
          tbody))));

    load();
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：系统设置
   * ------------------------------------------------------------------ */

  views['/admin/settings'] = function (root) {
    var box = h('div');

    function saveSetting(key, value) {
      return api('/api/setting', { method: 'POST', body: { key: key, value: value } }).then(function () {
        toast('设置更新成功', 'ok');
      });
    }

    function render(settings) {
      clear(box);
      if (!settings.length) {
        box.appendChild(h('div', { class: 'empty', text: '暂无设置项' }));
        return;
      }
      settings.forEach(function (st) {
        var valueEl;
        if (st.key === 'allow_registration') {
          valueEl = h('select', null,
            h('option', { value: 'true', text: '允许注册', selected: String(st.value) === 'true' }),
            h('option', { value: 'false', text: '禁止注册', selected: String(st.value) !== 'true' }));
        } else {
          valueEl = input({ value: st.value == null ? '' : String(st.value) });
        }
        var saveBtn = button('保存', function () {
          saveBtn.disabled = true;
          saveSetting(st.key, valueEl.value).catch(function (err) {
            toast(errText(err), 'error');
          }).then(function () { saveBtn.disabled = false; });
        }, 'small primary');

        box.appendChild(h('div', { class: 'progress-item' },
          h('div', { class: 'row' },
            h('div', { class: 'grow' },
              h('div', { text: SETTING_LABELS[st.key] || st.key }),
              h('div', { class: 'small muted', text: st.description || ('键名：' + st.key) })),
            h('div', { style: 'min-width:180px' }, valueEl),
            saveBtn)));
      });
    }

    function load() {
      return api('/api/setting/settings').then(function (settings) {
        render(settings || []);
      }).catch(function (err) {
        toast('加载设置失败：' + errText(err), 'error');
      });
    }

    root.appendChild(pageHead('系统设置', '全局设置项，保存后立即生效',
      [button('刷新', load, 'small')]));
    root.appendChild(h('div', { class: 'card' }, box));
    load();
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：WebDAV 配置
   * ------------------------------------------------------------------ */

  views['/admin/webdav'] = function (root) {
    var enabled = h('input', { type: 'checkbox' });
    var requireAuth = h('input', { type: 'checkbox' });
    var allowMkdir = h('input', { type: 'checkbox' });
    var allowDelete = h('input', { type: 'checkbox' });
    var allowMove = h('input', { type: 'checkbox' });
    var allowCopy = h('input', { type: 'checkbox' });
    var roles = h('select', null,
      ['admin', 'user', 'visitor', 'all'].map(function (r) {
        return h('option', { value: r, text: r === 'admin' ? '仅管理员' : r === 'all' ? '所有角色' : roleLabel(r) });
      }));
    var description = input({ placeholder: '配置描述' });
    var current = null;

    function toggles(label, box) {
      return h('label', { class: 'field inline' }, box, h('span', { text: label }));
    }

    var form = h('form', { class: 'card' },
      h('h2', { text: 'WebDAV 服务配置' }),
      h('div', { class: 'small muted', text: '启用后可通过 WebDAV 协议访问网盘文件（地址：/webdav）' }),
      toggles('启用 WebDAV', enabled),
      toggles('需要认证', requireAuth),
      field('允许的角色', roles),
      toggles('允许创建目录', allowMkdir),
      toggles('允许删除文件', allowDelete),
      toggles('允许移动文件', allowMove),
      toggles('允许复制文件', allowCopy),
      field('描述', description),
      h('div', { class: 'row' },
        h('button', { class: 'btn primary', type: 'submit', text: '保存配置' }),
        button('恢复默认', function () {
          if (!window.confirm('确定要恢复默认 WebDAV 配置吗？')) return;
          api('/api/webdav-config/reset', { method: 'POST' }).then(function () {
            toast('已恢复默认配置', 'ok');
            load();
          }).catch(function (err) { toast(errText(err), 'error'); });
        })));

    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var base = current || {};
      api('/api/webdav-config', {
        method: 'PUT',
        body: {
          id: base.id || 1,
          enabled: enabled.checked,
          requireAuth: requireAuth.checked,
          allowedRoles: roles.value,
          allowMkdir: allowMkdir.checked,
          allowDelete: allowDelete.checked,
          allowMove: allowMove.checked,
          allowCopy: allowCopy.checked,
          description: description.value,
          createTime: base.createTime || 0,
          updateTime: base.updateTime || 0
        }
      }).then(function () {
        toast('保存WebDAV配置成功', 'ok');
        load();
      }).catch(function (err) {
        toast(errText(err), 'error');
      });
    });

    function fill(cfg) {
      current = cfg;
      enabled.checked = !!cfg.enabled;
      requireAuth.checked = !!cfg.requireAuth;
      allowMkdir.checked = !!cfg.allowMkdir;
      allowDelete.checked = !!cfg.allowDelete;
      allowMove.checked = !!cfg.allowMove;
      allowCopy.checked = !!cfg.allowCopy;
      roles.value = cfg.allowedRoles || 'admin';
      description.value = cfg.description || '';
    }

    function load() {
      return api('/api/webdav-config').then(function (cfg) {
        if (cfg) fill(cfg);
      }).catch(function (err) {
        toast('加载WebDAV配置失败：' + errText(err), 'error');
      });
    }

    root.appendChild(pageHead('WebDAV 配置', '通过标准 WebDAV 协议访问网盘文件'));
    root.appendChild(form);
    load();
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：机器人保活
   * ------------------------------------------------------------------ */

  views['/admin/keepalive'] = function (root) {
    var statusBox = h('div', { class: 'alert info', text: '正在获取任务状态…' });

    function loadStatus() {
      return api('/api/bot/keep-alive/status').then(function (data) {
        var text = typeof data === 'string' ? data : (data && (data.status || data.message)) || '未知状态';
        var running = /运行|running|started|active/i.test(text);
        statusBox.className = 'alert ' + (running ? 'ok' : 'warn');
        statusBox.textContent = '任务状态：' + text;
      }).catch(function (err) {
        statusBox.className = 'alert error';
        statusBox.textContent = '获取任务状态失败：' + errText(err);
      });
    }

    function action(path, okMsg, confirmMsg) {
      if (confirmMsg && !window.confirm(confirmMsg)) return;
      api(path, { method: 'POST' }).then(function () {
        toast(okMsg, 'ok');
        loadStatus();
      }).catch(function (err) {
        toast(errText(err), 'error');
      });
    }

    root.appendChild(pageHead('机器人保活', '保持 Telegram 机器人活跃，避免长时间不活跃被限制',
      [button('刷新状态', loadStatus, 'small')]));
    root.appendChild(h('div', { class: 'card' },
      statusBox,
      h('p', { class: 'muted small', text: '定时任务将在每天凌晨 2 点自动发送保活消息。' }),
      h('div', { class: 'row' },
        button('发送保活消息', function () { action('/api/bot/keep-alive/send', '保活消息发送成功'); }, 'primary'),
        button('启动定时任务', function () {
          action('/api/bot/keep-alive/start', '定时保活任务启动成功',
            '确定要启动定时保活任务吗？任务将在每天凌晨2点自动发送保活消息');
        }),
        button('停止定时任务', function () {
          action('/api/bot/keep-alive/stop', '定时保活任务停止成功',
            '确定要停止定时保活任务吗？停止后机器人可能会因长时间不活跃而被限制');
        }, 'danger'))));
    loadStatus();
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：数据库备份
   * ------------------------------------------------------------------ */

  views['/admin/backup'] = function (root) {
    var fileInput = h('input', { type: 'file', accept: '.db' });
    var restoreBtn = button('恢复数据库', null, 'danger');
    restoreBtn.disabled = true;
    fileInput.addEventListener('change', function () {
      var f = fileInput.files && fileInput.files[0];
      if (f && !/\.db$/i.test(f.name)) {
        toast('只能上传 .db 格式的数据库文件', 'error');
        fileInput.value = '';
      }
      restoreBtn.disabled = !(f && /\.db$/i.test(f.name));
    });
    restoreBtn.addEventListener('click', function () {
      var f = fileInput.files && fileInput.files[0];
      if (!f) return;
      if (!window.confirm('您确定要使用文件「' + f.name + '」恢复数据库吗？此操作不可逆')) return;
      var fd = new FormData();
      fd.append('multipartFile', f);
      restoreBtn.disabled = true;
      restoreBtn.textContent = '恢复中…';
      fetch('/api/backup/upload', { method: 'POST', headers: authHeaders(), body: fd })
        .then(function (res) {
          if (res.status === 401) { handleUnauthorized(); throw apiError('登录已过期'); }
          return res.json();
        })
        .then(function (payload) {
          if (payload && payload.code === 1) toast('数据库恢复成功', 'ok');
          else throw apiError((payload && payload.msg) || '数据库恢复失败');
        })
        .catch(function (err) { toast(errText(err), 'error'); })
        .then(function () {
          restoreBtn.disabled = false;
          restoreBtn.textContent = '恢复数据库';
          fileInput.value = '';
        });
    });

    root.appendChild(pageHead('数据库备份', '下载或恢复 SQLite 数据库'));
    root.appendChild(h('div', { class: 'grid two' },
      h('div', { class: 'card' },
        h('h3', { text: '备份数据库' }),
        h('p', { class: 'muted small', text: '将当前数据库下载为 .db 文件，请妥善保管备份文件。' }),
        button('下载备份', function () {
          downloadBlob('/api/backup/download', 'tgDrive.db')
            .then(function () { toast('数据库备份下载成功', 'ok'); })
            .catch(function (err) { toast(errText(err), 'error'); });
        }, 'primary')),
      h('div', { class: 'card' },
        h('h3', { text: '恢复数据库' }),
        h('p', { class: 'muted small', text: '从 .db 备份文件恢复数据库。此操作将覆盖当前所有数据，请谨慎操作。' }),
        field('选择备份文件', fileInput),
        restoreBtn)));
  };

  /* ------------------------------------------------------------------ *
   * 管理视图：系统监控
   * ------------------------------------------------------------------ */

  views['/admin/monitor'] = function (root) {
    var box = h('div');
    var updated = h('span', { class: 'small muted' });

    function labelFor(key) { return MONITOR_LABELS[key] || key; }

    function formatValue(key, value) {
      if (value === null || value === undefined) return '-';
      if (typeof value === 'boolean') return value ? '是' : '否';
      if (typeof value === 'number') {
        if (/(millis|Milliseconds|Ms)$/.test(key)) return formatDuration(value / 1000);
        if (/(seconds|Seconds|uptime|Uptime)/.test(key)) return formatDuration(value);
        return String(value);
      }
      if (typeof value === 'object') return JSON.stringify(value);
      return String(value);
    }

    function render(data) {
      clear(box);
      updated.textContent = '最后更新：' + new Date().toLocaleTimeString();
      if (!data || typeof data !== 'object') {
        box.appendChild(h('div', { class: 'empty', text: '暂无监控数据' }));
        return;
      }
      var grid = h('div', { class: 'stats' });
      Object.keys(data).forEach(function (key) {
        var value = data[key];
        if (value && typeof value === 'object') {
          grid.appendChild(h('div', { class: 'stat' },
            h('div', { class: 'k', text: labelFor(key) }),
            h('div', { class: 'v small mono break', text: formatValue(key, value) })));
          return;
        }
        grid.appendChild(h('div', { class: 'stat' },
          h('div', { class: 'k', text: labelFor(key) }),
          h('div', { class: 'v', text: formatValue(key, value) })));
      });
      box.appendChild(grid);
    }

    function load() {
      return api('/api/system/monitor').then(render).catch(function (err) {
        clear(box);
        box.appendChild(alertBox('error', '加载监控数据失败：' + errText(err)));
      });
    }

    root.appendChild(pageHead('系统监控', '服务运行状态与资源占用', [updated, button('刷新', load, 'small')]));
    root.appendChild(h('div', { class: 'card' }, box));

    load().then(function () {
      state.monitorTimer = setInterval(function () {
        if (document.visibilityState === 'visible') load();
      }, 10000);
    });
    state.cleanup = function () {
      if (state.monitorTimer) { clearInterval(state.monitorTimer); state.monitorTimer = null; }
    };
  };

  /* ------------------------------------------------------------------ *
   * 视图：未找到
   * ------------------------------------------------------------------ */

  views.notFound = function (root) {
    root.appendChild(pageHead('页面不存在', '请检查地址或返回文件列表', [navLink('/files', '返回文件列表')]));
  };

  /* ------------------------------------------------------------------ *
   * 启动
   * ------------------------------------------------------------------ */

  function boot() {
    applyTheme();
    restoreSession();
    if (state.token) startRealtime();
    onRoute();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
