"use strict";
// Independent, single-use consent for each optional toolchain or processing operation.
let preparationKind = null, preparationPlan = null, preparationJob = null, preparationPoll = null;
const preparationLabels = {
  "conversion-toolchain": "Install isolated conversion toolchain",
  "quantizer-toolchain": "Install quantizer toolchain",
  "convert": "Convert source to GGUF",
  "quantize": "Quantize managed GGUF"
};
function preparationFacts(kind, plan) {
  if (kind === "convert") return [
    ["Repository", plan.repository], ["Immutable commit", plan.resolved_sha], ["Architecture", plan.architecture],
    ["Source format", plan.source_format], ["Source files and verification", plan.source_files.map(file => `${file.name} · ${formatBytes(file.size_bytes)} · ${file.verification_algorithm} ${file.verification_digest}`).join("\n")],
    ["Total source download", formatBytes(plan.source_bytes)], ["Output type and file", `${plan.outtype} · ${plan.output_filename}`],
    ["Estimated output minimum", formatBytes(plan.estimated_output_minimum_bytes)], ["Maximum output", formatBytes(plan.maximum_output_bytes)],
    ["Maximum working space", formatBytes(plan.maximum_working_bytes)], ["Destination pattern", plan.output_directory_pattern],
    ["License", plan.license], ["Base model", JSON.stringify(plan.base_model || "Not declared")],
    ["Converter identity", `${plan.environment_id} · ${shortHash(plan.environment_converter_sha256)}`]
  ];
  if (kind === "quantize") return [
    ["Input", plan.input], ["Input size", formatBytes(plan.input_size_bytes)], ["Input SHA-256", plan.input_sha256],
    ["Preset", plan.preset], ["Output file", plan.output_filename], ["Output estimate", `${formatBytes(plan.estimated_output_minimum_bytes)}–${formatBytes(plan.estimated_output_maximum_bytes)}`],
    ["Estimated peak disk", formatBytes(plan.estimated_peak_disk_bytes)], ["Destination pattern", plan.output_directory_pattern],
    ["Quantizer SHA-256", plan.toolchain_binary_sha256],
    ["Inherited license metadata", installedModels.find(model => model.path === plan.input)?.license || "Unknown — review source terms"],
    ["Inherited source", installedModels.find(model => model.path === plan.input)?.source_url || "Not recorded"]
  ];
  if (kind === "conversion-toolchain") return [
    ["Toolchain", `${plan.toolchain_id} · ${plan.version}`], ["Platform", `${plan.os} / ${plan.arch}`],
    ["Verified archives", plan.artifacts.map(file => `${file.name} · ${formatBytes(file.size_bytes)} · ${file.sha256}`).join("\n")],
    ["Locked dependencies", `${plan.dependencies.length} · lock ${plan.dependency_lock_sha256}`],
    ["Maximum dependency working space", formatBytes(plan.maximum_dependency_working_bytes)],
    ["Estimated installed size", formatBytes(plan.estimated_installed_bytes)], ["Destination", plan.destination]
  ];
  return [["Toolchain", `${plan.toolchain_id} · ${plan.version}`], ["Archive", plan.archive],
    ["Download", formatBytes(plan.size_bytes)], ["SHA-256", plan.sha256], ["Destination", plan.destination]];
}
async function openPreparation(kind, request) {
  if (preparationJob?.status === "running") { showToast("Cancel the current operation before reviewing another plan."); return; }
  if (document.querySelector("#details-dialog").open) document.querySelector("#details-dialog").close();
  preparationKind = kind; preparationPlan = null; preparationJob = null;
  clearTimeout(preparationPoll);
  const dialog = document.querySelector("#preparation-dialog");
  document.querySelector("#preparation-title").textContent = preparationLabels[kind];
  document.querySelector("#preparation-summary").replaceChildren(make("p", "", "Building an exact plan…"));
  document.querySelector("#preparation-error").textContent = "";
  document.querySelector("#preparation-consent").checked = false;
  document.querySelector("#confirm-preparation").disabled = true;
  document.querySelector("#confirm-preparation").classList.remove("hidden");
  document.querySelector("#cancel-preparation").classList.add("hidden");
  document.querySelector("#preparation-progress").classList.add("hidden");
  const options = document.querySelector("#preparation-options");
  options.replaceChildren(); options.classList.toggle("hidden", kind !== "convert" && kind !== "quantize");
  if (kind === "convert" || kind === "quantize") {
    const label = make("label", "", kind === "convert" ? "Output precision " : "Quantization preset ");
    const select = make("select");
    const choices = kind === "convert" ? ["f16", "bf16"] : ["Q4_K_M", "Q5_K_M", "Q8_0"];
    for (const choice of choices) {
      const option = make("option", "", choice); option.value = choice; select.append(option);
    }
    const field = kind === "convert" ? "outtype" : "preset";
    select.value = request[field];
    select.addEventListener("change", () => {
      dialog.close(); openPreparation(kind, { ...request, [field]: select.value });
    });
    label.append(select); options.append(label);
  }
  dialog.showModal();
  try {
    const plan = await api(`/api/preparation/${kind}/plan`, { method: "POST", body: JSON.stringify(request) });
    if (!dialog.open || preparationKind !== kind) return;
    preparationPlan = plan;
    const list = make("dl", "review");
    for (const [label, value] of preparationFacts(kind, plan)) {
      list.append(make("dt", "", label), make("dd", "", String(value || "Unknown")));
    }
    list.append(make("dt", "", "Exact consent digest"), make("dd", "", plan.consent_digest));
    const note = make("p", "fine-print", plan.warning || "Toolchain installation and model execution have separate consent boundaries.");
    const summary = document.querySelector("#preparation-summary");
    summary.replaceChildren(list, note);
    const source = safeExternalURL(plan.source_url);
    if (source) { const link = make("a", "", "Review immutable source and license ↗"); link.href = source; link.target = "_blank"; link.rel = "noreferrer"; summary.append(link); }
    if (kind === "convert") {
      const linkURL = safeExternalURL(plan.license_url);
      if (linkURL) { const link = make("a", "", "Review declared license ↗"); link.href = linkURL; link.target = "_blank"; link.rel = "noreferrer"; summary.append(link); }
    }
  } catch (error) { document.querySelector("#preparation-error").textContent = error.message; }
}
document.querySelector("#install-quantizer").addEventListener("click", () => openPreparation("quantizer-toolchain", {}));
document.querySelector("#preparation-consent").addEventListener("change", event => {
  document.querySelector("#confirm-preparation").disabled = !event.target.checked || !preparationPlan;
});
document.querySelector("#confirm-preparation").addEventListener("click", async () => {
  if (!preparationPlan) return;
  document.querySelector("#confirm-preparation").disabled = true;
  try {
    preparationJob = await api(`/api/preparation/${preparationKind}/run`, {
      method: "POST", body: JSON.stringify({ consent_digest: preparationPlan.consent_digest })
    });
    document.querySelector("#confirm-preparation").classList.add("hidden");
    document.querySelector("#cancel-preparation").classList.remove("hidden");
    document.querySelector("#preparation-progress").classList.remove("hidden");
    pollPreparation();
  } catch (error) { document.querySelector("#preparation-error").textContent = error.message; document.querySelector("#confirm-preparation").disabled = false; }
});
async function pollPreparation() {
  clearTimeout(preparationPoll);
  try {
    preparationJob = await api(`/api/jobs/${preparationJob.id}`);
    document.querySelector("#preparation-progress progress").value = preparationJob.total_bytes ? Math.min(100, preparationJob.completed_bytes / preparationJob.total_bytes * 100) : 0;
    document.querySelector("#preparation-progress p").textContent = `${preparationJob.stage || "Working"}: ${preparationJob.message || ""}${preparationJob.total_bytes ? ` · ${formatBytes(preparationJob.completed_bytes)} / ${formatBytes(preparationJob.total_bytes)}` : ""}`;
    if (preparationJob.status === "running") { preparationPoll = setTimeout(pollPreparation, 700); return; }
    document.querySelector("#cancel-preparation").classList.add("hidden");
    if (preparationJob.status === "completed") {
      document.querySelector("#preparation-progress p").textContent = preparationJob.record ? `Verified and registered: ${preparationJob.record.filename}. Open Local models to quantize or assign it.` : "Toolchain installed and verified. Review a separate model-processing plan to proceed.";
      preparationPlan = null;
    } else document.querySelector("#preparation-error").textContent = preparationJob.error || preparationJob.status;
  } catch (error) { document.querySelector("#preparation-error").textContent = error.message; }
}
document.querySelector("#preparation-dialog").addEventListener("close", () => {
  if (preparationJob?.status === "running") api(`/api/jobs/${preparationJob.id}`, { method: "DELETE" }).catch(() => {});
  clearTimeout(preparationPoll);
});
document.querySelector("#cancel-preparation").addEventListener("click", async () => {
  if (preparationJob) await api(`/api/jobs/${preparationJob.id}`, { method: "DELETE" });
});
