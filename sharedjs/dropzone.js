(() => {
  "use strict";
  if (window.csDropzone) {
    return;
  }

  const css = `
.upload-dropzone{position:relative;display:flex;flex-direction:column;align-items:center;justify-content:center;min-height:120px;padding:20px;border:2px dashed var(--border-color,#bbb);border-radius:12px;text-align:center;cursor:pointer;background:color-mix(in srgb,currentColor 3%,transparent);transition:border-color .2s ease,background-color .2s ease}
.upload-dropzone:hover,.upload-dropzone.dz-over{border-color:var(--primary-color,#8e44ad);background:color-mix(in srgb,var(--primary-color,#8e44ad) 7%,transparent)}
.upload-dropzone input[type="file"]{position:absolute;inset:0;width:100%;height:100%;opacity:0;cursor:pointer}
.upload-dropzone p{margin:0}
.upload-icon{width:48px;height:48px;fill:var(--primary-color,#8e44ad);margin-bottom:10px}
.upload-preview{display:grid;grid-template-columns:repeat(auto-fill,minmax(100px,1fr));gap:15px;margin-top:15px}
.dz-item{position:relative}
.dz-item img{display:block;width:100%;aspect-ratio:1/1;object-fit:cover;border-radius:10px;border:1px solid var(--border-color,#bbb)}
.dz-remove{position:absolute;top:6px;inset-inline-end:6px;display:flex;align-items:center;justify-content:center;width:28px;height:28px;padding:0;border:0;border-radius:50%;background:#c0392b;cursor:pointer}
.dz-remove svg{width:14px;height:14px;fill:#fff}
.dz-file{grid-column:1/-1;font-size:.9rem;opacity:.85;overflow-wrap:anywhere}
`;

  try {
    const sheet = new CSSStyleSheet();
    sheet.replaceSync(css);
    document.adoptedStyleSheets = document.adoptedStyleSheets.concat([sheet]);
  } catch (err) {
    console.error("dropzone: could not install styles", err);
  }

  const SVG = "http://www.w3.org/2000/svg";

  const iconBase = (zone) => {
    const use = zone.querySelector("use");
    const href = use ? use.getAttribute("href") || "" : "";
    const base = href.split("#")[0];
    return base || "/static/icons.svg";
  };

  const trashIcon = (base) => {
    const svg = document.createElementNS(SVG, "svg");
    const use = document.createElementNS(SVG, "use");
    use.setAttribute("href", base + "#fa-trash");
    svg.appendChild(use);
    return svg;
  };

  const previewArea = (input) => {
    const container = input.closest(".card, .form-group, form");
    if (!container) {
      return null;
    }
    let area = container.querySelector(".upload-preview");
    if (!area) {
      area = document.createElement("div");
      area.className = "upload-preview";
      const zone = input.closest(".upload-dropzone") || input.parentElement;
      zone.after(area);
    }
    return area;
  };

  const sizeText = (file) => (file.size / 1048576 >= 0.1 ? (file.size / 1048576).toFixed(1) + " MB" : Math.max(1, Math.round(file.size / 1024)) + " KB");

  const tooMany = (limit) => {
    const meta = document.querySelector('meta[name="upload-max-images-error"]');
    const text = meta ? (meta.getAttribute("content") || "").replace("{max}", String(limit)) : "";
    if (!text) {
      return;
    }
    if (typeof window.appAlert === "function") {
      window.appAlert(text);
    } else {
      console.error("dropzone:", text);
    }
  };

  const onChange = (input) => {
    const area = previewArea(input);
    if (!area) {
      return;
    }
    const multiple = input.hasAttribute("multiple");
    let limit = parseInt(input.getAttribute("data-max-files") || "10", 10);
    if (isNaN(limit) || limit <= 0) {
      limit = 10;
    }
    const files = Array.from(input.files || []);
    if (!multiple) {
      area.querySelectorAll(".dz-item, .dz-file").forEach((n) => n.remove());
    } else if (area.querySelectorAll(".dz-item").length + files.length > limit) {
      input.value = "";
      tooMany(limit);
      return;
    }
    const base = iconBase(input.closest(".upload-dropzone") || input.parentElement);
    files.forEach((file) => {
      if (!file.type.startsWith("image/")) {
        const row = document.createElement("div");
        row.className = "dz-file";
        row.textContent = file.name + " (" + sizeText(file) + ")";
        area.appendChild(row);
        return;
      }
      const reader = new FileReader();
      reader.onload = (ev) => {
        const item = document.createElement("div");
        item.className = "dz-item";
        const img = document.createElement("img");
        img.src = ev.target.result;
        img.alt = file.name;
        const rm = document.createElement("button");
        rm.type = "button";
        rm.className = "dz-remove";
        rm.setAttribute("aria-label", file.name);
        rm.appendChild(trashIcon(base));
        rm.addEventListener("click", () => item.remove());
        item.appendChild(img);
        item.appendChild(rm);
        area.appendChild(item);
      };
      reader.onerror = () => console.error("dropzone: could not read " + file.name, reader.error);
      reader.readAsDataURL(file);
    });
  };

  document.addEventListener("change", (ev) => {
    const input = ev.target;
    if (input && input.matches && input.matches('.upload-dropzone input[type="file"]')) {
      onChange(input);
    }
  });

  const over = (ev, on) => {
    const zone = ev.target && ev.target.closest ? ev.target.closest(".upload-dropzone") : null;
    if (zone) {
      zone.classList.toggle("dz-over", on);
    }
  };
  document.addEventListener("dragenter", (ev) => over(ev, true));
  document.addEventListener("dragover", (ev) => over(ev, true));
  document.addEventListener("dragleave", (ev) => over(ev, false));
  document.addEventListener("drop", (ev) => over(ev, false));

  window.csDropzone = { version: 1 };
})();
