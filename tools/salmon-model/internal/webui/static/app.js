"use strict";

const token = document.querySelector('meta[name="salmon-token"]').content;
const $ = selector => document.querySelector(selector);
const make = (tag, className, text) => {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
};

const formatBytes = value => {
  if (!value) return "Size unavailable";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = Number(value), unit = 0;
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit++; }
  return `${size.toFixed(unit ? 1 : 0)} ${units[unit]}`;
};
const formatCount = value => new Intl.NumberFormat(undefined, { notation: "compact" }).format(value || 0);
const shortHash = value => value ? `${value.slice(0, 10)}…` : "Unavailable";
const GiB = 1024 * 1024 * 1024;
const detectedRAM = Number(navigator.deviceMemory || 0);
let targetProfile = { ramGB: detectedRAM || 8, vramGB: 0, maxDownloadGB: 4, mode: "cpu" };
let lastSearchResults = [], lastProviderResults = [], lastRecommendationData = null;

function safeExternalURL(value) {
  try { const parsed = new URL(value); return parsed.protocol === "https:" ? parsed.href : ""; }
  catch { return ""; }
}

function fitFor(file) {
  if (!file?.size_bytes) return { className: "unknown", label: "Fit unknown", estimatedGB: 0 };
  const sizeGB = file.size_bytes / GiB;
  const estimatedGB = sizeGB * 1.25 + 0.5;
  const availableGB = Math.max(0, targetProfile.ramGB - 2) + (targetProfile.mode === "gpu" ? targetProfile.vramGB * 0.9 : 0);
  if (sizeGB > targetProfile.maxDownloadGB || estimatedGB > availableGB) return { className: "exceeds", label: "Exceeds target", estimatedGB };
  const ratio = availableGB ? estimatedGB / availableGB : Infinity;
  if (ratio <= 0.65) return { className: "comfortable", label: "Fits comfortably", estimatedGB };
  if (ratio <= 0.85) return { className: "likely", label: "Likely fits", estimatedGB };
  return { className: "tight", label: "Tight fit", estimatedGB };
}

function fitElement(file) {
  const fit = fitFor(file), element = make("span", `fit ${fit.className}`, fit.label);
  element.title = fit.estimatedGB ? `Rough runtime estimate: ${fit.estimatedGB.toFixed(1)} GB before larger contexts or batching` : "File size unavailable";
  return element;
}

async function api(path, options = {}) {
  const headers = { Accept: "application/json", ...(options.headers || {}) };
  if (options.body) headers["Content-Type"] = "application/json";
  if (options.method && options.method !== "GET") headers["X-Salmon-Token"] = token;
  const response = await fetch(path, { ...options, headers });
  const data = await response.json().catch(() => ({ error: `HTTP ${response.status}` }));
  if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
  return data;
}

function empty(target, title, detail) {
  const element = make("div", "empty");
  element.append(make("strong", "", title), make("span", "", detail));
  target.replaceChildren(element);
}

function activateView(name) {
  document.querySelectorAll(".nav[data-view], .view").forEach(element => element.classList.remove("active"));
  document.querySelector(`.nav[data-view="${name}"]`).classList.add("active");
  $(`#${name}`).classList.add("active");
  if (name === "local") loadInstalled();
  if (name === "recommended") loadRecommendations();
  if (name === "hardware") populateHardwareForm();
}

document.querySelectorAll(".nav[data-view]").forEach(button => {
  button.addEventListener("click", () => activateView(button.dataset.view));
});
document.querySelectorAll("[data-close]").forEach(button => {
  button.addEventListener("click", () => button.closest("dialog").close());
});

$("#search-form").addEventListener("submit", async event => {
  event.preventDefault();
  const status = $("#search-status"), target = $("#results");
  status.textContent = "Searching Hugging Face…";
  target.replaceChildren();
  try {
    const query = encodeURIComponent($("#query").value);
    const format = encodeURIComponent($("#format").value);
    const data = await api(`/api/search?q=${query}&format=${format}&limit=30`);
    lastSearchResults = data.results;
    renderFilteredResults(lastSearchResults, target, status, "live");
  } catch (error) {
    status.textContent = `Search failed: ${error.message}`;
    empty(target, "Hugging Face could not be reached", "Check your connection and try again.");
  }
});

function selectedFilters() {
  return {
    purpose: $("#filter-purpose").value, quant: $("#filter-quant").value,
    license: $("#filter-license").value, maxGB: Number($("#filter-size").value || 0)
  };
}

function matchesFilters(result) {
  const filters = selectedFilters(), files = result.plan.gguf_files || [];
  if (filters.purpose !== "any" && !(result.plan.candidate_purposes || []).includes(filters.purpose)) return false;
  if (filters.license === "declared" && result.plan.license === "unknown") return false;
  if (!["any", "declared"].includes(filters.license) && result.plan.license.toLowerCase() !== filters.license) return false;
  if (filters.quant !== "any" && !files.some(file => (file.quantization || "").startsWith(filters.quant))) return false;
  if (filters.maxGB && !files.some(file => file.size_bytes && file.size_bytes <= filters.maxGB * GiB)) return false;
  return true;
}

function renderFilteredResults(results, target, status, source) {
  const filtered = results.filter(matchesFilters);
  status.textContent = `${filtered.length} of ${results.length} ${source} result${results.length === 1 ? "" : "s"} match the current filters`;
  if (!filtered.length) { empty(target, "No models match these filters", "Clear a filter or increase the maximum file size."); return; }
  target.replaceChildren(...filtered.map(remoteRow));
}

function refreshFilteredResults() {
  if (lastSearchResults.length) renderFilteredResults(lastSearchResults, $("#results"), $("#search-status"), "live");
  if (lastProviderResults.length) $("#provider-results").replaceChildren(...lastProviderResults.map(remoteRow));
}

["#filter-purpose", "#filter-quant", "#filter-license", "#filter-size"].forEach(selector => {
  $(selector).addEventListener("input", refreshFilteredResults);
});
$("#clear-filters").addEventListener("click", () => {
  $("#filter-purpose").value = "any"; $("#filter-quant").value = "any";
  $("#filter-license").value = "any"; $("#filter-size").value = "";
  refreshFilteredResults();
});
$("#edit-target").addEventListener("click", () => activateView("hardware"));

function remoteRow(result) {
  const row = make("article", "model-row");
  const main = make("div", "model-main");
  main.append(make("div", "model-name", result.repository));
  const metadata = make("div", "model-meta");
  metadata.append(
    make("span", "", result.pipeline || "Task not declared"),
    make("span", result.plan.license === "unknown" ? "license-unknown" : "", `License: ${result.plan.license}`),
    make("span", "", `${result.plan.gguf_files.length} GGUF file${result.plan.gguf_files.length === 1 ? "" : "s"}`),
    make("span", "", `${formatCount(result.downloads)} downloads`),
    make("span", "", `${formatCount(result.likes)} likes`)
  );
  const fitting = (result.plan.gguf_files || []).filter(file => fitFor(file).className !== "exceeds").length;
  metadata.append(make("span", fitting ? "validation-state" : "license-unknown", fitting ? `${fitting} file${fitting === 1 ? "" : "s"} within target` : "No files within target"));
  main.append(metadata);
  const actions = make("div", "model-actions");
  const inspectButton = make("button", "secondary", "View files");
  inspectButton.addEventListener("click", () => inspect(result.repository));
  actions.append(inspectButton);
  row.append(main, actions);
  return row;
}

let recommendationsLoaded = false;
async function loadRecommendations() {
  if (recommendationsLoaded) return;
  $("#starter-status").textContent = "Loading reviewed sources…";
  try {
    const data = await api("/api/recommendations");
    $("#starter-status").textContent = data.warning;
    lastRecommendationData = data;
    renderStarterModels();
    $("#provider-list").replaceChildren(...data.providers.map(providerRow));
    recommendationsLoaded = true;
  } catch (error) {
    $("#starter-status").textContent = `Could not load recommendations: ${error.message}`;
  }
}

function renderStarterModels() {
  if (lastRecommendationData) $("#starter-models").replaceChildren(...lastRecommendationData.models.map(starterRow));
}

function suggestedFile(model) {
  const candidates = model.files.filter(file => fitFor(file).className !== "exceeds");
  if (!candidates.length) return null;
  return candidates.sort((a, b) => (b.size_bytes || 0) - (a.size_bytes || 0))[0];
}

function starterRow(model) {
  const row = make("article", "model-row"), main = make("div", "model-main");
  main.append(make("div", "model-name", model.name));
  const metadata = make("div", "model-meta");
  metadata.append(
    make("span", "validation-state", model.validation_status.replaceAll("_", " ")),
    make("span", "", model.purposes.join(" + ")), make("span", "", `License: ${model.license}`),
    make("span", "", model.files.map(file => file.quantization).join(" / ")),
    make("span", "", model.repository)
  );
  const suggestion = suggestedFile(model);
  if (suggestion) metadata.append(fitElement(suggestion), make("span", "validation-state", `Suggested: ${suggestion.quantization}`));
  else metadata.append(make("span", "license-unknown", "No starter file fits the target profile"));
  main.append(metadata, make("div", "validation-note", model.validation_note));
  const actions = make("div", "model-actions"), view = make("button", "secondary", "View files");
  view.addEventListener("click", () => inspect(model.repository, model.resolved_sha));
  actions.append(view);
  row.append(main, actions);
  return row;
}

function providerRow(provider) {
  const row = make("article", "provider-row");
  row.append(make("h3", "", provider.name), make("div", "category", provider.category), make("p", "", provider.rationale));
  const footer = make("div", "provider-footer");
  footer.append(make("span", "", provider.purposes.join(" · ")));
  const browse = make("button", "secondary", "Browse live models");
  browse.addEventListener("click", () => browseProvider(provider));
  footer.append(browse); row.append(footer);
  return row;
}

async function browseProvider(provider) {
  const section = $("#provider-results-section"), target = $("#provider-results"), status = $("#provider-status");
  section.classList.remove("hidden");
  $("#provider-results-title").textContent = provider.name;
  $("#provider-results-note").textContent = provider.caveat;
  status.textContent = "Querying Hugging Face live…";
  target.replaceChildren();
  section.scrollIntoView({ behavior: "smooth", block: "start" });
  try {
    const data = await api(`/api/providers/${encodeURIComponent(provider.id)}/models?limit=20`);
    lastProviderResults = data.results;
    if (!data.results.length) { status.textContent = "No current GGUF repositories"; empty(target, "No GGUF repositories found", "The provider may not currently publish matching files."); return; }
    status.textContent = `${data.results.length} current GGUF repositor${data.results.length === 1 ? "y" : "ies"} · not stored by SaLMon`;
    target.replaceChildren(...data.results.map(remoteRow));
  } catch (error) {
    status.textContent = `Provider search failed: ${error.message}`;
  }
}
$("#close-provider-results").addEventListener("click", () => $("#provider-results-section").classList.add("hidden"));

async function inspect(repository, revision = "main") {
  const dialog = $("#details-dialog");
  $("#details-title").textContent = repository;
  $("#details-status").textContent = "Resolving repository metadata…";
  $("#details-link").removeAttribute("href");
  $("#details-content").replaceChildren();
  dialog.showModal();
  try {
    const data = await api(`/api/inspect?repository=${encodeURIComponent(repository)}&revision=${encodeURIComponent(revision)}`);
    $("#details-status").textContent = "Live metadata from Hugging Face. Compatibility has not been certified.";
    $("#details-link").href = safeExternalURL(data.source_url);
    renderDetails(data);
  } catch (error) {
    $("#details-status").textContent = `Inspection failed: ${error.message}`;
  }
}

function renderDetails(data) {
  const content = $("#details-content");
  const facts = make("div", "facts");
  [
    ["Resolved commit", shortHash(data.resolved_sha)],
    ["License", data.plan.license],
    ["Architecture", data.plan.architecture || "Unknown"],
    ["Base model", data.base_model ? (typeof data.base_model === "string" ? data.base_model : JSON.stringify(data.base_model)) : "Not declared"],
    ["Task", data.pipeline || "Not declared"],
    ["Downloads", formatCount(data.downloads)],
    ["Candidate use", (data.plan.candidate_purposes || []).join(", ")]
  ].forEach(([label, value]) => {
    const fact = make("div", "fact");
    fact.append(make("small", "", label), make("code", "", String(value)));
    facts.append(fact);
  });
  content.append(facts);
  const licensePanel = make("div", "warning");
  licensePanel.append(make("strong", "", `Declared license: ${data.plan.license}. `));
  const licenseURL = safeExternalURL(data.license_url) || safeExternalURL(data.source_url);
  if (licenseURL) {
    const link = make("a", "", data.license_url ? "Review license terms ↗" : "Review the model card and inherited terms ↗");
    link.href = licenseURL; link.target = "_blank"; link.rel = "noreferrer"; licensePanel.append(link);
  }
  licensePanel.append(make("span", "", " Metadata is not legal certification."));
  content.append(licensePanel);
  if (data.plan.warnings?.length) content.append(make("div", "warning", data.plan.warnings.join(" ")));
  const files = make("div", "files");
  files.append(make("h3", "", data.plan.gguf_files.length ? "Available files" : "No direct GGUF files"));
  data.plan.gguf_files.forEach(file => {
    const row = make("div", "file-row");
    row.append(
      make("code", "", file.name),
      make("span", "", file.quantization || "Unknown"),
      make("span", "", formatBytes(file.size_bytes)),
      fitElement(file)
    );
    const installButton = make("button", "secondary", "Install");
    installButton.disabled = !file.sha256 || !file.size_bytes;
    installButton.title = installButton.disabled ? "A reported size and SHA-256 are required" : "Review this installation";
    installButton.addEventListener("click", () => prepareInstall(data.repository, file.name, data.resolved_sha));
    row.append(installButton);
    files.append(row);
  });
  content.append(files);
}

let pendingPlan = null, currentJob = null, pollTimer = null;

async function prepareInstall(repository, filename, revision = "main") {
  $("#details-dialog").close();
  const dialog = $("#install-dialog");
  $("#install-summary").replaceChildren(make("span", "", "Building an exact installation plan…"));
  $("#install-error").textContent = "";
  $("#consent").checked = false;
  $("#confirm-install").disabled = true;
  $("#confirm-install").classList.remove("hidden");
  $("#cancel-download").classList.add("hidden");
  $("#install-progress").classList.add("hidden");
  dialog.showModal();
  try {
    pendingPlan = await api("/api/install-plan", {
      method: "POST", body: JSON.stringify({ repository, revision, filename })
    });
    renderInstallPlan(pendingPlan);
  } catch (error) {
    $("#install-error").textContent = error.message;
  }
}

function renderInstallPlan(plan) {
  const list = make("dl", "review");
  [
    ["Repository", plan.repository], ["Commit", plan.resolved_sha], ["File", plan.filename],
    ["Download", formatBytes(plan.size_bytes)], ["Estimated fit", `${fitFor({ size_bytes: plan.size_bytes }).label} (planning estimate)`],
    ["License", plan.license], ["SHA-256", plan.sha256], ["Destination", plan.destination]
  ].forEach(([label, value]) => list.append(make("dt", "", label), make("dd", "", String(value || "Unknown"))));
  const licenseURL = safeExternalURL(plan.license_url) || safeExternalURL(plan.source_url);
  if (licenseURL) {
    const link = make("a", "", "Review source and license terms ↗");
    link.href = licenseURL; link.target = "_blank"; link.rel = "noreferrer";
    list.append(make("dt", "", "License review"), make("dd", "", ""));
    list.lastElementChild.append(link);
  }
  $("#install-summary").replaceChildren(list);
}

$("#consent").addEventListener("change", event => {
  $("#confirm-install").disabled = !event.target.checked || !pendingPlan;
});
$("#confirm-install").addEventListener("click", async () => {
  if (!pendingPlan) return;
  $("#confirm-install").disabled = true;
  $("#install-error").textContent = "";
  try {
    currentJob = await api("/api/install", {
      method: "POST",
      body: JSON.stringify({
        repository: pendingPlan.repository, revision: pendingPlan.resolved_sha,
        filename: pendingPlan.filename, consent_digest: pendingPlan.consent_digest
      })
    });
    $("#confirm-install").classList.add("hidden");
    $("#cancel-download").classList.remove("hidden");
    $("#install-progress").classList.remove("hidden");
    pollJob();
  } catch (error) {
    $("#install-error").textContent = error.message;
    $("#confirm-install").disabled = false;
  }
});

async function pollJob() {
  clearTimeout(pollTimer);
  try {
    currentJob = await api(`/api/jobs/${currentJob.id}`);
    const percentage = currentJob.total_bytes ? Math.min(100, currentJob.completed_bytes / currentJob.total_bytes * 100) : 0;
    $("#install-progress progress").value = percentage;
    $("#install-progress p").textContent = `${formatBytes(currentJob.completed_bytes)} of ${formatBytes(currentJob.total_bytes)} · ${currentJob.status}`;
    if (currentJob.status === "running") {
      pollTimer = setTimeout(pollJob, 500);
      return;
    }
    $("#cancel-download").classList.add("hidden");
    if (currentJob.status === "completed") {
      $("#install-progress p").textContent = `Verified and installed: ${currentJob.record.path}`;
      pendingPlan = null;
    } else {
      $("#install-error").textContent = currentJob.error || currentJob.status;
    }
  } catch (error) {
    $("#install-error").textContent = error.message;
  }
}
$("#cancel-download").addEventListener("click", async () => {
  if (currentJob) await api(`/api/jobs/${currentJob.id}`, { method: "DELETE" });
});

let installedModels = [];
async function loadInstalled() {
  const status = $("#installed-status"), target = $("#installed-list");
  status.textContent = "Reading local installations…";
  try {
    const data = await api("/api/installed");
    installedModels = data.models;
    $("#storage-root").textContent = data.root;
    const total = data.models.reduce((sum, model) => sum + model.size_bytes, 0);
    $("#storage-summary").textContent = `${data.models.length} model${data.models.length === 1 ? "" : "s"} · ${formatBytes(total)}`;
    status.textContent = "";
    if (!data.models.length) {
      empty(target, "No local models", "Install a GGUF from Hugging Face to add it here.");
      return;
    }
    target.replaceChildren(...data.models.map(localRow));
  } catch (error) {
    status.textContent = `Could not read local models: ${error.message}`;
  }
}

function localRow(record) {
  const row = make("article", "model-row"), main = make("div", "model-main");
  main.append(make("div", "model-name", record.filename));
  const metadata = make("div", "model-meta");
  metadata.append(
    make("span", "", record.repository), make("span", "", formatBytes(record.size_bytes)),
    make("span", record.license === "unknown" ? "license-unknown" : "", `License: ${record.license}`),
    make("span", "", `SHA-256 ${shortHash(record.sha256)}`), make("span", "", record.runtime_validation)
  );
  main.append(metadata);
  const actions = make("div", "model-actions");
  const assign = make("button", "secondary", "Add to project");
  assign.addEventListener("click", () => openAssignment(record));
  const remove = make("button", "danger", "Remove");
  remove.addEventListener("click", () => removeInstalled(record));
  actions.append(assign, remove);
  row.append(main, actions);
  return row;
}

async function removeInstalled(record) {
  if (!confirm(`Remove ${record.filename} from managed storage?`)) return;
  try {
    await api(`/api/installed/${record.id}`, { method: "DELETE" });
    loadInstalled();
  } catch (error) {
    $("#installed-status").textContent = `Removal failed: ${error.message}`;
  }
}
$("#refresh-installed").addEventListener("click", loadInstalled);

let assignmentRecord = null;
function openAssignment(record) {
  assignmentRecord = record;
  $("#assign-title").textContent = record.filename;
  $("#assign-manifest").value = $("#manifest-path").value;
  $("#assign-export").value = `models/${record.filename}`;
  $("#assign-error").textContent = "";
  document.querySelectorAll('input[name="purpose"]').forEach(input => { input.checked = false; });
  const license = record.license === "unknown" ? "No license was declared. Review the repository before commercial use." : `Declared license: ${record.license}. This metadata is not legal certification.`;
  $("#assign-license").textContent = `${license} Source: ${record.source_url}`;
  $("#assign-dialog").showModal();
}

$("#assign-form").addEventListener("submit", async event => {
  event.preventDefault();
  const purposes = [...document.querySelectorAll('input[name="purpose"]:checked')].map(input => input.value);
  try {
    const manifestPath = $("#assign-manifest").value.trim();
    const manifest = await api("/api/project/assign", {
      method: "POST",
      body: JSON.stringify({
        manifest_path: manifestPath, installation_id: assignmentRecord.id,
        purposes, export_path: $("#assign-export").value.trim()
      })
    });
    $("#manifest-path").value = manifestPath;
    $("#assign-dialog").close();
    activateView("project");
    renderManifest(manifest);
    $("#project-status").textContent = `Saved ${manifestPath}`;
  } catch (error) {
    $("#assign-error").textContent = error.message;
  }
});

$("#load-manifest").addEventListener("click", loadManifest);
async function loadManifest() {
  const path = $("#manifest-path").value.trim();
  $("#project-status").textContent = "Opening manifest…";
  try {
    const manifest = await api(`/api/project?path=${encodeURIComponent(path)}`);
    renderManifest(manifest);
    $("#project-status").textContent = manifest.models.length ? `${manifest.models.length} project model assignment${manifest.models.length === 1 ? "" : "s"}` : "The manifest is ready for its first assignment.";
  } catch (error) {
    $("#project-status").textContent = `Could not open manifest: ${error.message}`;
  }
}

function renderManifest(manifest) {
  const target = $("#project-list");
  if (!manifest.models.length) {
    empty(target, "No model assignments", "Use Add to project from Local models.");
    return;
  }
  target.replaceChildren(...manifest.models.map(assignment => {
    const row = make("article", "model-row"), main = make("div", "model-main");
    main.append(make("div", "model-name", assignment.export_path));
    const metadata = make("div", "model-meta");
    metadata.append(
      make("span", "", assignment.purposes.join(" + ")), make("span", "", assignment.repository),
      make("span", assignment.license === "unknown" ? "license-unknown" : "", `License: ${assignment.license}`),
      make("span", "", `SHA-256 ${shortHash(assignment.sha256)}`)
    );
    main.append(metadata);
    const actions = make("div", "model-actions");
    actions.append(make("span", "", assignment.required ? "Required" : "Optional"));
    const remove = make("button", "danger", "Remove");
    remove.addEventListener("click", () => removeAssignment(assignment));
    actions.append(remove);
    row.append(main, actions);
    return row;
  }));
}

async function removeAssignment(assignment) {
  if (!confirm(`Remove ${assignment.export_path} from this project manifest?`)) return;
  try {
    const manifest = await api("/api/project/assignment", {
      method: "DELETE",
      body: JSON.stringify({ manifest_path: $("#manifest-path").value.trim(), export_path: assignment.export_path })
    });
    renderManifest(manifest);
    $("#project-status").textContent = "Project assignment removed. The locally installed model was not deleted.";
  } catch (error) {
    $("#project-status").textContent = `Could not remove assignment: ${error.message}`;
  }
}

function populateHardwareForm() {
  $("#target-ram").value = targetProfile.ramGB;
  $("#target-vram").value = targetProfile.vramGB;
  $("#target-download").value = targetProfile.maxDownloadGB;
  $("#target-mode").value = targetProfile.mode;
  $("#target-summary").textContent = `${targetProfile.ramGB} GB RAM${targetProfile.vramGB ? ` + ${targetProfile.vramGB} GB VRAM` : ""}`;
  $("#hardware-detected").textContent = detectedRAM ? `Browser reported approximately ${detectedRAM} GB system memory; adjust this for your shipping target.` : "Browser memory detection was unavailable; adjust this for your shipping target.";
}

$("#hardware-form").addEventListener("submit", event => {
  event.preventDefault();
  targetProfile = {
    ramGB: Number($("#target-ram").value), vramGB: Number($("#target-vram").value),
    maxDownloadGB: Number($("#target-download").value), mode: $("#target-mode").value
  };
  populateHardwareForm();
  renderStarterModels();
  refreshFilteredResults();
  activateView("recommended");
  $("#starter-status").textContent = "Target profile applied. Fit labels are conservative planning estimates, not performance guarantees.";
});

populateHardwareForm();
loadRecommendations();

$("#exit").addEventListener("click", async () => {
  if (!confirm("Quit the SaLMon model companion?")) return;
  await api("/api/shutdown", { method: "POST", body: "{}" }).catch(() => {});
  document.body.replaceChildren(make("main", "empty", "SaLMon Model Companion stopped. You can close this tab."));
});
