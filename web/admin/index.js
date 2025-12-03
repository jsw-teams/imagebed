(function () {
  var API_BASE = "/api/admin";
  var DASHBOARD_URL = "/admin/dashboard";

  var form = document.getElementById("login-form");
  var usernameInput = document.getElementById("username");
  var passwordInput = document.getElementById("password");
  var btnLogin = document.getElementById("btn-login");
  var btnClear = document.getElementById("btn-clear");
  var errorBox = document.getElementById("error-box");
  var errorText = document.getElementById("error-text");
  var badgeState = document.getElementById("badge-state");
  var statusDot = document.getElementById("status-dot");
  var statusText = document.getElementById("status-text");

  var tsWrap = document.getElementById("ts-wrap");
  var tsConfig = { enabled: false, siteKey: "" };
  var tsWidgetId = null;
  var tsToken = "";
  var tsPendingLogin = false;

  function setError(msg) {
    if (!msg) {
      errorBox.classList.remove("visible");
      errorText.textContent = "";
      usernameInput.classList.remove("input-error");
      passwordInput.classList.remove("input-error");
      return;
    }
    errorBox.classList.add("visible");
    errorText.textContent = msg;
    usernameInput.classList.add("input-error");
    passwordInput.classList.add("input-error");
  }

  function setLoading(isLoading) {
    if (isLoading) {
      btnLogin.disabled = true;
      btnLogin.innerHTML =
        '<div class="btn-primary-spinner"></div><span>正在登录…</span>';
    } else {
      btnLogin.disabled = false;
      btnLogin.innerHTML = "<span>登录后台</span>";
    }
  }

  function updateSessionUI(loggedIn, username) {
    if (loggedIn) {
      badgeState.textContent = "已登录";
      badgeState.style.background = "#ecfdf5";
      badgeState.style.color = "#166534";
      badgeState.style.borderColor = "#bbf7d0";

      statusDot.style.background = "#22c55e";
      statusDot.style.boxShadow = "0 0 0 4px rgba(34,197,94,0.22)";
      statusText.textContent = username ? "已登录：" + username : "已登录";
    } else {
      badgeState.textContent = "未登录";
      badgeState.style.background = "#f9fafb";
      badgeState.style.color = "#4b5563";
      badgeState.style.borderColor = "#e5e7eb";

      statusDot.style.background = "#f97316";
      statusDot.style.boxShadow = "0 0 0 4px rgba(249,115,22,0.25)";
      statusText.textContent = "尚未登录";
    }
  }

  // ---------------- Turnstile 前端逻辑 ----------------

  function showTsWrap() {
    if (!tsWrap) return;
    tsWrap.style.display = "block";
  }

  function loadTurnstileScriptOnce(callback) {
    if (window.turnstile) {
      callback();
      return;
    }
    if (loadTurnstileScriptOnce.loading) {
      loadTurnstileScriptOnce.queue.push(callback);
      return;
    }
    loadTurnstileScriptOnce.loading = true;
    loadTurnstileScriptOnce.queue = [callback];

    var s = document.createElement("script");
    s.src =
      "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
    s.async = true;
    s.defer = true;
    s.onload = function () {
      loadTurnstileScriptOnce.loading = false;
      var q = loadTurnstileScriptOnce.queue || [];
      loadTurnstileScriptOnce.queue = [];
      q.forEach(function (fn) {
        try {
          fn();
        } catch (e) {}
      });
    };
    document.head.appendChild(s);
  }

  function renderTurnstileIfNeeded() {
    if (!tsConfig.enabled || !tsConfig.siteKey) return;
    if (tsWidgetId !== null) return;

    showTsWrap();
    loadTurnstileScriptOnce(function () {
      if (!window.turnstile) return;
      tsWidgetId = window.turnstile.render("#ts-container", {
        sitekey: tsConfig.siteKey,
        callback: function (token) {
          tsToken = token || "";
          if (tsPendingLogin && tsToken) {
            tsPendingLogin = false;
            doLogin(); // 自动执行登录
          }
        },
        "expired-callback": function () {
          tsToken = "";
          setError("验证码已过期，请重新验证后再登录。");
        },
        "error-callback": function () {
          tsToken = "";
          tsPendingLogin = false;
          setError("人机验证出错，请刷新页面或稍后再试。");
        }
      });
    });
  }

  // 从后端读取 Turnstile 是否启用；没有该接口时视为未启用
  function fetchTurnstileConfig() {
    fetch("/api/turnstile", { credentials: "omit" })
      .then(function (resp) {
        if (!resp.ok) return null;
        return resp.json();
      })
      .then(function (data) {
        if (data && data.enabled && data.site_key) {
          tsConfig.enabled = true;
          tsConfig.siteKey = data.site_key;
        } else {
          tsConfig.enabled = false;
          tsConfig.siteKey = "";
        }
      })
      .catch(function () {
        tsConfig.enabled = false;
        tsConfig.siteKey = "";
      });
  }

  // ---------------- 登录逻辑 ----------------

  async function doLogin() {
    setError("");

    var username = usernameInput.value.trim();
    var password = passwordInput.value.trim();

    if (!username || !password) {
      setError("请完整填写用户名和密码。");
      return;
    }

    setLoading(true);
    try {
      var headers = {
        "Content-Type": "application/json"
      };
      if (tsConfig.enabled && tsToken) {
        headers["X-Turnstile-Token"] = tsToken;
      }

      var resp = await fetch(API_BASE + "/login", {
        method: "POST",
        credentials: "include",
        headers: headers,
        body: JSON.stringify({
          username: username,
          password: password,
          turnstile_token: tsToken
        })
      });

      var data = await resp.json().catch(function () {
        return {};
      });

      if (!resp.ok || !data.ok) {
        var msg =
          data.error === "invalid_credentials"
            ? "用户名或密码不正确。"
            : data.detail || data.error || "登录失败，请稍后重试。";
        setError(msg);
        updateSessionUI(false);
        // 登录失败后重置验证码，必须重新验证
        if (tsConfig.enabled && window.turnstile && tsWidgetId !== null) {
          window.turnstile.reset(tsWidgetId);
          tsToken = "";
        }
        return;
      }

      updateSessionUI(true, username);
      setError("");

      // 登录成功，跳转 dashboard
      if (DASHBOARD_URL) {
        window.location.href = DASHBOARD_URL;
      }
    } catch (err) {
      console.error(err);
      setError("无法连接服务器，请检查网络或稍后再试。");
    } finally {
      setLoading(false);
    }
  }

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    setError("");

    // 未启用 Turnstile：直接登录
    if (!tsConfig.enabled) {
      doLogin();
      return;
    }

    // 已启用且已有 token：直接登录
    if (tsToken) {
      doLogin();
      return;
    }

    // 需要先做人机验证
    tsPendingLogin = true;
    renderTurnstileIfNeeded();
    setError("请先完成人机验证，通过后会自动提交登录。");
  });

  btnClear.addEventListener("click", function () {
    usernameInput.value = "";
    passwordInput.value = "";
    setError("");
    usernameInput.focus();
  });

  passwordInput.addEventListener("keydown", function (e) {
    if (e.key === "Enter") {
      form.dispatchEvent(new Event("submit", { cancelable: true }));
    }
  });

  // ---------------- 会话检测 ----------------

  async function checkSession() {
    try {
      var resp = await fetch(API_BASE + "/session", {
        credentials: "include"
      });
      if (!resp.ok) {
        updateSessionUI(false);
        return;
      }
      var data = await resp.json().catch(function () {
        return {};
      });
      var logged = !!data.logged_in;
      var username = data.username || "";
      updateSessionUI(logged, username);

      if (logged && DASHBOARD_URL) {
        window.location.href = DASHBOARD_URL;
      }
    } catch (e) {
      statusDot.style.background = "#f97316";
      statusText.textContent = "会话检测失败";
    }
  }

  // 页面初始化：先检测会话，再读取 Turnstile 配置
  checkSession();
  fetchTurnstileConfig();
})();