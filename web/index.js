(function () {
  var fileInput = document.getElementById("file-input");
  var dropzone = document.getElementById("dropzone");
  var btnChoose = document.getElementById("btn-choose");
  var btnUpload = document.getElementById("btn-upload");
  var fileNameEl = document.getElementById("file-name");
  var statusText = document.getElementById("status-text");
  var resultBox = document.getElementById("result-box");
  var resultUrl = document.getElementById("result-url");
  var btnCopy = document.getElementById("btn-copy");

  function setStatus(msg) {
    statusText.textContent = msg || "";
  }

  function handleFiles(files) {
    if (!files || !files.length) return;
    var f = files[0];
    fileInput.files = files;
    fileNameEl.textContent =
      f.name + " · " + Math.round(f.size / 1024) + " KB";
    resultBox.style.display = "none";
    setStatus("");
  }

  btnChoose.addEventListener("click", function () {
    fileInput.click();
  });

  fileInput.addEventListener("change", function (e) {
    handleFiles(e.target.files);
  });

  dropzone.addEventListener("dragover", function (e) {
    e.preventDefault();
    dropzone.style.borderColor = "#4f46e5";
    dropzone.style.background = "#eef2ff";
  });

  dropzone.addEventListener("dragleave", function (e) {
    e.preventDefault();
    dropzone.style.borderColor = "rgba(148,163,184,0.9)";
    dropzone.style.background = "rgba(255,255,255,0.95)";
  });

  dropzone.addEventListener("drop", function (e) {
    e.preventDefault();
    dropzone.style.borderColor = "rgba(148,163,184,0.9)";
    dropzone.style.background = "rgba(255,255,255,0.95)";
    handleFiles(e.dataTransfer.files);
  });

  // ---------------- Turnstile 相关 ----------------

  var tsConfig = { enabled: false, siteKey: "" };
  var tsWidgetId = null;
  var tsToken = "";
  var tsPendingUpload = false;

  function createTurnstileContainer() {
    if (document.getElementById("turnstile-wrap")) return;

    var card = document.querySelector(".upload-card");
    if (!card) return;

    var wrap = document.createElement("div");
    wrap.id = "turnstile-wrap";
    wrap.style.marginTop = "12px";

    var label = document.createElement("div");
    label.textContent = "人机验证";
    label.style.fontSize = "12px";
    label.style.color = "#6b7280";
    label.style.marginBottom = "4px";

    var container = document.createElement("div");
    container.id = "turnstile-container";

    wrap.appendChild(label);
    wrap.appendChild(container);
    card.appendChild(wrap);
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

    createTurnstileContainer();
    loadTurnstileScriptOnce(function () {
      if (!window.turnstile) return;
      tsWidgetId = window.turnstile.render("#turnstile-container", {
        sitekey: tsConfig.siteKey,
        callback: function (token) {
          tsToken = token || "";
          if (tsPendingUpload && tsToken) {
            tsPendingUpload = false;
            doUpload();
          }
        },
        "expired-callback": function () {
          tsToken = "";
          setStatus("验证码已过期，请重新验证后再上传。");
        },
        "error-callback": function () {
          tsToken = "";
          tsPendingUpload = false;
          setStatus("人机验证出错，请刷新页面或稍后重试。");
        }
      });
    });
  }

  // 从后端读取 Turnstile 启用状态（如果没有该接口则自动降级为未启用）
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

  // ---------------- 实际上传逻辑 ----------------

  function doUpload() {
    var files = fileInput.files;
    if (!files || !files.length) {
      setStatus("请先选择一张图片。");
      return;
    }

    var form = new FormData();
    form.append("file", files[0]);

    var headers = {};
    if (tsConfig.enabled && tsToken) {
      // 尽量兼容不同后端实现：Header + 多个字段都带上
      headers["X-Turnstile-Token"] = tsToken;
      form.append("turnstile_token", tsToken);
      form.append("cf-turnstile-response", tsToken);
    }

    btnUpload.disabled = true;
    setStatus("正在上传，请稍候…");

    fetch("/api/upload", {
      method: "POST",
      body: form,
      headers: headers
    })
      .then(function (resp) {
        return resp
          .json()
          .catch(function () {
            return {};
          })
          .then(function (data) {
            return { resp: resp, data: data };
          });
      })
      .then(function (result) {
        var resp = result.resp;
        var data = result.data || {};

        if (!resp.ok) {
          var msg =
            data.error ||
            data.detail ||
            resp.statusText ||
            "上传失败，请稍后重试。";
          throw new Error(msg);
        }

        var id = data.id || data.image_id || data.imageID;
        if (!id) {
          throw new Error("后端没有返回图片 ID。");
        }

        var url =
          window.location.protocol +
          "//" +
          window.location.host +
          "/i/" +
          id;
        resultUrl.value = url;
        resultBox.style.display = "block";
        setStatus("上传成功！");

        // 每次上传成功后重置验证码，下次上传需重新验证
        if (tsConfig.enabled && window.turnstile && tsWidgetId !== null) {
          window.turnstile.reset(tsWidgetId);
          tsToken = "";
        }
      })
      .catch(function (err) {
        resultBox.style.display = "none";
        setStatus(
          "上传失败：" +
            (err && err.message ? err.message : String(err || "未知错误"))
        );
      })
      .finally(function () {
        btnUpload.disabled = false;
      });
  }

  btnUpload.addEventListener("click", function () {
    var files = fileInput.files;
    if (!files || !files.length) {
      setStatus("请先选择一张图片。");
      return;
    }

    // 未启用 Turnstile：直接上传
    if (!tsConfig.enabled) {
      doUpload();
      return;
    }

    // 已启用且已有 token：直接上传
    if (tsToken) {
      doUpload();
      return;
    }

    // 需要先做人机验证
    tsPendingUpload = true;
    renderTurnstileIfNeeded();
    setStatus("请先完成人机验证，通过后会自动开始上传。");
  });

  // 复制按钮
  btnCopy.addEventListener("click", function () {
    var text = resultUrl.value;
    if (!text) return;

    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard
        .writeText(text)
        .then(function () {
          setStatus("链接已复制到剪贴板。");
        })
        .catch(function () {
          resultUrl.select();
          document.execCommand("copy");
          setStatus("链接已复制到剪贴板。");
        });
    } else {
      resultUrl.select();
      document.execCommand("copy");
      setStatus("链接已复制到剪贴板。");
    }
  });

  // 页面加载后，静默查询 Turnstile 是否启用
  fetchTurnstileConfig();
})();