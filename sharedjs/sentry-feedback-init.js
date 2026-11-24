(function () {
  var scriptEl = document.currentScript;
  var dsn = scriptEl && scriptEl.getAttribute("data-sentry-dsn");
  if (!dsn || typeof Sentry === "undefined") return;

  Sentry.init({
    dsn: dsn,
    integrations: [Sentry.feedbackIntegration({ autoInject: false })],
  });

  var form = document.getElementById("feedback-form");
  if (!form) return;

  var emailInput = document.getElementById("feedback-email");
  var messageInput = document.getElementById("feedback-message");
  var shareScreenBtn = document.getElementById("feedback-share-screen-btn");
  var attachImageBtn = document.getElementById("feedback-attach-image-btn");
  var fileInput = document.getElementById("feedback-file-input");
  var previewBox = document.getElementById("feedback-attachment-preview");
  var previewThumb = document.getElementById("feedback-attachment-thumb");
  var removeAttachmentBtn = document.getElementById("feedback-remove-attachment-btn");
  var errorBox = document.getElementById("feedback-error");
  var successBox = document.getElementById("feedback-success");
  var submitBtn = document.getElementById("feedback-submit-btn");

  var attachment = null;

  function setAttachment(filename, contentType, data) {
    attachment = { filename: filename, contentType: contentType, data: data };
    var blob = new Blob([data], { type: contentType });
    previewThumb.src = URL.createObjectURL(blob);
    previewBox.hidden = false;
  }

  function clearAttachment() {
    if (previewThumb.src) URL.revokeObjectURL(previewThumb.src);
    attachment = null;

    previewThumb.removeAttribute("src");
    previewBox.hidden = true;
    fileInput.value = "";
  }

  removeAttachmentBtn.addEventListener("click", clearAttachment);

  attachImageBtn.addEventListener("click", function () {
    fileInput.click();
  });

  fileInput.addEventListener("change", function () {
    var file = fileInput.files && fileInput.files[0];
    if (!file) return;
    file.arrayBuffer().then(function (buf) {
      setAttachment(file.name, file.type || "image/png", new Uint8Array(buf));
    });
  });

  shareScreenBtn.addEventListener("click", function () {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getDisplayMedia) {
      errorBox.textContent = scriptEl.getAttribute("data-error-message") || "";
      errorBox.hidden = false;
      return;
    }
    navigator.mediaDevices
      .getDisplayMedia({ video: true })
      .then(function (stream) {
        var video = document.createElement("video");
        video.srcObject = stream;
        return video.play().then(function () {
          var canvas = document.createElement("canvas");
          canvas.width = video.videoWidth;
          canvas.height = video.videoHeight;
          canvas.getContext("2d").drawImage(video, 0, 0);
          stream.getTracks().forEach(function (t) {
            t.stop();
          });
          return new Promise(function (resolve) {
            canvas.toBlob(resolve, "image/png");
          });
        });
      })
      .then(function (blob) {
        if (!blob) return;
        return blob.arrayBuffer().then(function (buf) {
          setAttachment("screenshot.png", "image/png", new Uint8Array(buf));
        });
      })
      .catch(function (err) {
        if (err && err.name === "NotAllowedError") return;
        console.error("[feedback] screen share failed:", err);
      });
  });

  form.addEventListener("submit", function (e) {
    e.preventDefault();
    errorBox.hidden = true;
    successBox.hidden = true;
    submitBtn.disabled = true;

    try {
      Sentry.captureFeedback(
        {
          message: messageInput.value,
          email: emailInput.value || undefined,
        },
        { attachments: attachment ? [attachment] : [] }
      );
      successBox.hidden = false;
      form.reset();
      clearAttachment();
    } catch (err) {
      console.error("[feedback] submit failed:", err);
      errorBox.textContent = scriptEl.getAttribute("data-error-message") || "";
      errorBox.hidden = false;
    } finally {
      submitBtn.disabled = false;
    }
  });
})();
