(function () {
  if (window.CQr) return;

  var active = null;

  function decodeData(data, width, height, attempts) {
    if (typeof jsQR !== "function") throw new Error("QR library not loaded");
    var res = jsQR(data, width, height, { inversionAttempts: attempts });
    return res && res.data ? res.data : null;
  }

  function decodeFile(file) {
    return new Promise(function (resolve, reject) {
      var url = URL.createObjectURL(file);
      var img = new Image();
      img.onload = function () {
        try {
          var canvas = document.createElement("canvas");
          canvas.width = img.naturalWidth;
          canvas.height = img.naturalHeight;
          var ctx = canvas.getContext("2d");
          ctx.drawImage(img, 0, 0);
          var d = ctx.getImageData(0, 0, canvas.width, canvas.height);
          var text = decodeData(d.data, d.width, d.height, "attemptBoth");
          if (text) resolve(text);
          else reject(new Error("No QR code found in image"));
        } catch (err) {
          reject(err);
        } finally {
          URL.revokeObjectURL(url);
        }
      };
      img.onerror = function () {
        URL.revokeObjectURL(url);
        reject(new Error("Could not load image"));
      };
      img.src = url;
    });
  }

  function stop() {
    if (!active) return;
    cancelAnimationFrame(active.raf);
    active.stream.getTracks().forEach(function (t) { t.stop(); });
    active.video.srcObject = null;
    active.video.hidden = true;
    active.root.classList.remove("qr-active");
    active = null;
  }

  function startCamera(root, video, onText) {
    stop();
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return Promise.reject(new Error("Camera not supported"));
    }
    return navigator.mediaDevices
      .getUserMedia({ video: { facingMode: { ideal: "environment" } }, audio: false })
      .then(function (stream) {
        var canvas = document.createElement("canvas");
        var ctx = canvas.getContext("2d", { willReadFrequently: true });
        active = { root: root, video: video, stream: stream, raf: 0 };
        video.srcObject = stream;
        video.hidden = false;
        root.classList.add("qr-active");
        var playing = video.play();
        if (playing && playing.catch) playing.catch(function () {});
        var current = active;
        function tick() {
          if (active !== current) return;
          if (video.readyState >= 2 && video.videoWidth > 0) {
            canvas.width = video.videoWidth;
            canvas.height = video.videoHeight;
            ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
            var d = ctx.getImageData(0, 0, canvas.width, canvas.height);
            var text = null;
            try {
              text = decodeData(d.data, d.width, d.height, "dontInvert");
            } catch (err) {
              stop();
              return;
            }
            if (text) {
              stop();
              onText(text);
              return;
            }
          }
          current.raf = requestAnimationFrame(tick);
        }
        current.raf = requestAnimationFrame(tick);
      });
  }

  function showError(root, message) {
    var box = root.querySelector("[data-qr-error-box]");
    if (!box) return;
    box.textContent = message || "";
    box.hidden = !message;
  }

  function deliver(root, text) {
    var input = document.getElementById(root.getAttribute("data-qr-input") || "");
    if (!input) return;
    input.value = text;
    var form = document.getElementById(root.getAttribute("data-qr-form") || "");
    if (form) {
      if (form.requestSubmit) form.requestSubmit();
      else form.submit();
    }
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-qr-start]");
    if (!btn) return;
    var root = btn.closest("[data-qr-scan]");
    if (!root) return;
    e.preventDefault();
    showError(root, "");
    if (active && active.root === root) {
      stop();
      return;
    }
    var video = root.querySelector("[data-qr-video]");
    if (!video) return;
    startCamera(root, video, function (text) { deliver(root, text); }).catch(function (err) {
      console.error("QR camera error:", err);
      showError(root, root.getAttribute("data-qr-camera-error"));
    });
  });

  document.addEventListener("change", function (e) {
    var fileInput = e.target.closest("[data-qr-file]");
    if (!fileInput || !fileInput.files || !fileInput.files[0]) return;
    var root = fileInput.closest("[data-qr-scan]");
    if (!root) return;
    showError(root, "");
    decodeFile(fileInput.files[0]).then(function (text) {
      deliver(root, text);
    }).catch(function (err) {
      console.error("QR decode error:", err);
      showError(root, root.getAttribute("data-qr-file-error"));
    }).then(function () {
      fileInput.value = "";
    });
  });

  document.addEventListener("cshop:page", stop);
  document.addEventListener("ctime:page", stop);
  document.addEventListener("cbots:page", stop);
  window.addEventListener("pagehide", stop);

  window.CQr = { decodeFile: decodeFile, startCamera: startCamera, stop: stop };
})();
