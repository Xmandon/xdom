const statusOutput = document.getElementById("statusOutput");
const inventoryOutput = document.getElementById("inventoryOutput");
const createOrderOutput = document.getElementById("createOrderOutput");
const orderLookupOutput = document.getElementById("orderLookupOutput");
const faultOutput = document.getElementById("faultOutput");
const orderIdInput = document.getElementById("orderIdInput");
const adminTokenInput = document.getElementById("adminTokenInput");

function renderJSON(element, value) {
  element.textContent = JSON.stringify(value, null, 2);
}

function setHealthBadge(text, mode) {
  const badge = document.getElementById("healthBadge");
  badge.textContent = text;
  badge.className = "pill";
  if (mode === "success") {
    badge.classList.add("success");
  }
  if (mode === "danger") {
    badge.classList.add("danger");
  }
}

async function requestJSON(path, options = {}) {
  const response = await fetch(path, options);
  const contentType = response.headers.get("content-type") || "";
  const body = contentType.includes("application/json") ? await response.json() : await response.text();

  if (!response.ok) {
    const message = typeof body === "object" && body && body.error ? body.error : String(body);
    throw new Error(`${response.status} ${message}`);
  }
  return body;
}

async function refreshStatus() {
  const [health, version, metrics] = await Promise.all([
    requestJSON("/healthz"),
    requestJSON("/version"),
    requestJSON("/metrics"),
  ]);

  document.getElementById("serviceName").textContent = version.service || health.service || "-";
  document.getElementById("serviceEnv").textContent = version.env || health.env || "-";
  document.getElementById("serviceVersion").textContent = version.version || "-";
  document.getElementById("serviceCommit").textContent = version.commit_sha || "-";
  document.getElementById("serviceBuild").textContent = version.build_id || "-";
  document.getElementById("faultMode").textContent = metrics.fault?.mode || "-";
  document.getElementById("faultDelay").textContent = `${metrics.fault?.delay_ms ?? 0} ms`;

  const degraded = health.status !== "ok";
  setHealthBadge(degraded ? "Degraded" : "Healthy", degraded ? "danger" : "success");
  renderJSON(statusOutput, { health, version, metrics });
}

async function refreshInventory() {
  const payload = await requestJSON("/api/inventory");
  renderJSON(inventoryOutput, payload);

  const body = document.getElementById("inventoryBody");
  const items = payload.items || [];
  if (!items.length) {
    body.innerHTML = '<tr><td colspan="2">No inventory data.</td></tr>';
    return;
  }

  body.innerHTML = items
    .map((item) => `<tr><td>${item.sku}</td><td>${item.available}</td></tr>`)
    .join("");
}

async function refreshDashboard() {
  try {
    await Promise.all([refreshStatus(), refreshInventory()]);
  } catch (error) {
    statusOutput.textContent = error.message;
  }
}

async function createOrder(event) {
  event.preventDefault();
  const form = new FormData(event.currentTarget);
  const payload = {
    user_id: String(form.get("user_id") || ""),
    sku: String(form.get("sku") || ""),
    quantity: Number(form.get("quantity") || 0),
    amount: Number(form.get("amount") || 0),
    payment_channel: String(form.get("payment_channel") || ""),
  };

  try {
    const result = await requestJSON("/api/orders", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    renderJSON(createOrderOutput, result);
    if (result.id) {
      orderIdInput.value = result.id;
    }
    await refreshDashboard();
  } catch (error) {
    createOrderOutput.textContent = error.message;
  }
}

async function lookupOrder(event) {
  event.preventDefault();
  const orderId = orderIdInput.value.trim();
  if (!orderId) {
    orderLookupOutput.textContent = "Order ID is required.";
    return;
  }

  try {
    const result = await requestJSON(`/api/orders/${encodeURIComponent(orderId)}`);
    renderJSON(orderLookupOutput, result);
  } catch (error) {
    orderLookupOutput.textContent = error.message;
  }
}

async function cancelOrder() {
  const orderId = orderIdInput.value.trim();
  if (!orderId) {
    orderLookupOutput.textContent = "Order ID is required.";
    return;
  }

  try {
    const result = await requestJSON(`/api/orders/${encodeURIComponent(orderId)}/cancel`, {
      method: "POST",
    });
    renderJSON(orderLookupOutput, result);
    await refreshDashboard();
  } catch (error) {
    orderLookupOutput.textContent = error.message;
  }
}

async function applyFault(event) {
  event.preventDefault();
  const token = adminTokenInput.value.trim();
  if (!token) {
    faultOutput.textContent = "ADMIN_TOKEN is required for fault injection.";
    return;
  }

  const form = new FormData(event.currentTarget);
  const payload = {
    mode: String(form.get("mode") || "none"),
    delay_ms: Number(form.get("delay_ms") || 0),
  };

  try {
    const result = await requestJSON("/admin/fault", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Admin-Token": token,
      },
      body: JSON.stringify(payload),
    });
    renderJSON(faultOutput, result);
    await refreshDashboard();
  } catch (error) {
    faultOutput.textContent = error.message;
  }
}

async function resetFault() {
  if (!adminTokenInput.value.trim()) {
    faultOutput.textContent = "ADMIN_TOKEN is required for fault injection.";
    return;
  }

  try {
    const result = await requestJSON("/admin/fault", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "X-Admin-Token": adminTokenInput.value.trim(),
      },
      body: JSON.stringify({ mode: "none", delay_ms: 0 }),
    });
    renderJSON(faultOutput, result);
    await refreshDashboard();
  } catch (error) {
    faultOutput.textContent = error.message;
  }
}

document.getElementById("refreshDashboard").addEventListener("click", refreshDashboard);
document.getElementById("refreshInventory").addEventListener("click", refreshInventory);
document.getElementById("createOrderForm").addEventListener("submit", createOrder);
document.getElementById("orderLookupForm").addEventListener("submit", lookupOrder);
document.getElementById("cancelOrderButton").addEventListener("click", cancelOrder);
document.getElementById("faultForm").addEventListener("submit", applyFault);
document.getElementById("resetFaultButton").addEventListener("click", resetFault);

refreshDashboard();
