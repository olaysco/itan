// Heydit frontend — talks to the FastAPI backend.
const $ = (sel) => document.querySelector(sel);

const state = {
  project: null,
  view: "current", // "current" | "original"
};

// --- Capability badges -----------------------------------------------------
async function loadCapabilities() {
  try {
    const res = await fetch("/api/health");
    const data = await res.json();
    const caps = data.capabilities || {};
    const el = $("#caps");
    const items = [
      ["Agent", caps.agent],
      ["Audio", caps.audio],
      ["Visual", caps.visual],
      ["ffmpeg", caps.ffmpeg ? "cloud" : "stub"],
    ];
    el.innerHTML = items
      .map(([label, mode]) => {
        const cls = mode === "cloud" ? "cloud" : "stub";
        const val = label === "ffmpeg" ? (caps.ffmpeg ? "ready" : "missing") : mode;
        return `<span class="cap ${cls}"><b>${label}</b> ${val}</span>`;
      })
      .join("");
  } catch (e) {
    $("#caps").innerHTML = `<span class="cap stub">offline</span>`;
  }
}

// --- Upload ----------------------------------------------------------------
const dropzone = $("#dropzone");
const fileInput = $("#fileInput");

dropzone.addEventListener("click", () => fileInput.click());
dropzone.addEventListener("dragover", (e) => { e.preventDefault(); dropzone.classList.add("drag"); });
dropzone.addEventListener("dragleave", () => dropzone.classList.remove("drag"));
dropzone.addEventListener("drop", (e) => {
  e.preventDefault();
  dropzone.classList.remove("drag");
  if (e.dataTransfer.files.length) uploadFile(e.dataTransfer.files[0]);
});
fileInput.addEventListener("change", () => {
  if (fileInput.files.length) uploadFile(fileInput.files[0]);
});

$("#demoBtn").addEventListener("click", async (e) => {
  e.stopPropagation();
  addMessage("assistant", "Spinning up a demo clip…", { spinner: true });
  try {
    const res = await fetch("/api/demo", { method: "POST" });
    if (!res.ok) throw new Error((await res.json()).detail || "demo failed");
    setProject(await res.json());
    replaceLastMessage("assistant", "Here's a demo clip. Ask me to expand it, fix its audio grammar, or change its background!");
  } catch (err) {
    replaceLastMessage("assistant", "Couldn't create a demo clip: " + err.message);
  }
});

async function uploadFile(file) {
  addMessage("assistant", `Uploading <em>${escapeHtml(file.name)}</em>…`, { spinner: true });
  const fd = new FormData();
  fd.append("file", file);
  try {
    const res = await fetch("/api/upload", { method: "POST", body: fd });
    if (!res.ok) throw new Error((await res.json()).detail || "upload failed");
    setProject(await res.json());
    replaceLastMessage("assistant", "Got it! What would you like to change?");
  } catch (err) {
    replaceLastMessage("assistant", "Upload failed: " + err.message);
  }
}

function setProject(project) {
  state.project = project;
  state.view = "current";
  $("#dropzone").classList.add("hidden");
  $("#stage").classList.remove("hidden");
  renderVideo();
  updateToggle();
}

function renderVideo() {
  const url = state.view === "original" ? state.project.original_url : state.project.current_url;
  const player = $("#player");
  player.src = url + "?t=" + Date.now(); // cache-bust after edits
  player.load();
  $("#downloadBtn").href = url;
}

function updateToggle() {
  $("#showOriginal").classList.toggle("active", state.view === "original");
  $("#showCurrent").classList.toggle("active", state.view === "current");
}

$("#showOriginal").addEventListener("click", () => { state.view = "original"; renderVideo(); updateToggle(); });
$("#showCurrent").addEventListener("click", () => { state.view = "current"; renderVideo(); updateToggle(); });
$("#resetBtn").addEventListener("click", () => {
  state.project = null;
  $("#stage").classList.add("hidden");
  $("#dropzone").classList.remove("hidden");
});

// --- Chat ------------------------------------------------------------------
const composer = $("#composer");
const promptInput = $("#prompt");

composer.addEventListener("submit", (e) => {
  e.preventDefault();
  const text = promptInput.value.trim();
  if (!text) return;
  sendMessage(text);
});

document.querySelectorAll(".chip").forEach((chip) => {
  chip.addEventListener("click", () => sendMessage(chip.textContent.trim()));
});

async function sendMessage(text) {
  if (!state.project) {
    addMessage("assistant", "Upload a video first, then tell me what to edit. 🙂");
    return;
  }
  addMessage("user", escapeHtml(text));
  promptInput.value = "";
  setBusy(true);
  addMessage("assistant", "Thinking…", { spinner: true });

  try {
    const res = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ project_id: state.project.id, message: text }),
    });
    if (!res.ok) throw new Error((await res.json()).detail || "request failed");
    const data = await res.json();
    state.project = data.project;
    state.view = "current";
    renderVideo();
    updateToggle();
    replaceLastMessage("assistant", escapeHtml(data.reply), { steps: data.steps });
  } catch (err) {
    replaceLastMessage("assistant", "Something went wrong: " + err.message);
  } finally {
    setBusy(false);
  }
}

function setBusy(busy) {
  $("#send").disabled = busy;
  promptInput.disabled = busy;
  if (!busy) promptInput.focus();
}

// --- Message rendering -----------------------------------------------------
const messages = $("#messages");

function addMessage(role, html, opts = {}) {
  const wrap = document.createElement("div");
  wrap.className = `msg ${role}`;
  wrap.innerHTML = `<div class="bubble">${opts.spinner ? '<span class="spinner"></span> ' : ""}${html}${renderSteps(opts.steps)}</div>`;
  messages.appendChild(wrap);
  messages.scrollTop = messages.scrollHeight;
  return wrap;
}

function replaceLastMessage(role, html, opts = {}) {
  const last = messages.querySelector(".msg:last-child .bubble");
  if (last) {
    last.innerHTML = `${html}${renderSteps(opts.steps)}`;
  } else {
    addMessage(role, html, opts);
  }
  messages.scrollTop = messages.scrollHeight;
}

function renderSteps(steps) {
  if (!steps || !steps.length) return "";
  const rows = steps
    .map((s) => {
      const mode = s.mode || "stub";
      const detail = s.details ? formatDetails(s.details) : "";
      return `
        <div class="step">
          <div class="step-head">
            <span class="tool-tag">${escapeHtml(s.tool)}()</span>
            <span class="badge ${mode}">${escapeHtml(mode)}</span>
          </div>
          <div>${escapeHtml(s.summary || "")}</div>
          ${detail ? `<div class="detail">${detail}</div>` : ""}
        </div>`;
    })
    .join("");
  return `<div class="steps">${rows}</div>`;
}

function formatDetails(d) {
  const parts = [];
  if (d.transcript) parts.push("heard: " + escapeHtml(truncate(d.transcript, 160)));
  if (d.corrected && d.corrected !== d.transcript) parts.push("fixed: " + escapeHtml(truncate(d.corrected, 160)));
  if (d.target_aspect) parts.push("target aspect ≈ " + d.target_aspect);
  if (d.background) parts.push("background: " + escapeHtml(d.background));
  if (d.model) parts.push("model: " + escapeHtml(d.model));
  return parts.join("\n");
}

// --- utils -----------------------------------------------------------------
function escapeHtml(str) {
  return String(str).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function truncate(s, n) { return s.length > n ? s.slice(0, n) + "…" : s; }

loadCapabilities();
