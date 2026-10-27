"use strict";
const $ = (id) => document.getElementById(id);
let settings,
  token = "",
  current,
  grants = [],
  loadGeneration = 0,
  proposed = null;
const tell = (message) => {
  $("message").textContent = message;
};
const b64 = (bytes) =>
  btoa(String.fromCharCode(...bytes))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
const random = () => b64(crypto.getRandomValues(new Uint8Array(32)));
async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: {
      "content-type": "application/json",
      authorization: "Bearer " + token,
      ...options.headers,
    },
    cache: "no-store",
    credentials: "omit",
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    if (response.status === 401) {
      token = "";
      $("console").hidden = true;
      $("login").hidden = false;
    }
    throw Error(
      response.status === 409 && body.error === "revision_conflict"
        ? "This configuration or grant changed. Refresh and review the latest revision before publishing."
        : body.error || "Service unavailable. Try again.",
    );
  }
  return response.json();
}
async function login(enroll = false) {
  const verifier = random(),
    state = random();
  sessionStorage.setItem(
    "admin_pkce",
    JSON.stringify({ verifier, state, started: Date.now() }),
  );
  const challenge = b64(
    new Uint8Array(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier)),
    ),
  );
  const url = new URL(
    settings.loginDomain + (enroll ? "/passkeys/add" : "/oauth2/authorize"),
  );
  url.search = new URLSearchParams({
    response_type: "code",
    client_id: settings.clientId,
    redirect_uri: settings.origin + "/callback",
    scope: "openid email aws.cognito.signin.user.admin",
    state,
    code_challenge: challenge,
    code_challenge_method: "S256",
  });
  location.assign(url);
}
async function callback() {
  const params = new URLSearchParams(location.search);
  if (!params.has("code")) return false;
  const saved = JSON.parse(sessionStorage.getItem("admin_pkce") || "null");
  sessionStorage.removeItem("admin_pkce");
  history.replaceState(null, "", "/");
  if (
    !saved ||
    saved.state !== params.get("state") ||
    Date.now() - saved.started > 600000
  )
    throw Error("Sign-in expired or could not be verified. Start again.");
  const response = await fetch(settings.loginDomain + "/oauth2/token", {
    method: "POST",
    headers: { "content-type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "authorization_code",
      client_id: settings.clientId,
      redirect_uri: settings.origin + "/callback",
      code: params.get("code"),
      code_verifier: saved.verifier,
    }),
    credentials: "omit",
  });
  if (!response.ok) throw Error("Sign-in could not be completed. Start again.");
  const body = await response.json();
  token = body.access_token;
  setTimeout(
    () => {
      token = "";
      $("console").hidden = true;
      $("login").hidden = false;
      tell("Your session expired. Sign in again.");
    },
    Math.max(0, (body.expires_in - 30) * 1000),
  );
  return true;
}
async function load() {
  const generation = ++loadGeneration,
    env = $("environment").value;
  current = null;
  proposed = null;
  $("review").hidden = true;
  $("config").hidden = true;
  $("grant").hidden = true;
  $("history").replaceChildren();
  tell("Loading " + env + "…");
  const config = await api("/api/environments/" + env);
  const versions = await api("/api/environments/" + env + "/history");
  if (generation !== loadGeneration || env !== $("environment").value) return;
  current = config;
  $("revision").textContent = current.revision;
  $("updated").textContent = new Date(current.updatedAt).toLocaleString();
  $("enabled").checked = current.features.idCapture;
  for (const name of ["documentType", "jurisdiction", "purpose"])
    $(name).value = current.capture[name];
  $("grant").hidden = !grants.find((g) => g.environment === env)?.manageGrants;
  $("config").hidden = false;
  for (const version of versions) {
    const li = document.createElement("li"),
      title = document.createElement("strong"),
      detail = document.createElement("small");
    title.textContent =
      "Revision " + version.config.revision + " · " + version.action;
    detail.textContent =
      new Date(version.config.updatedAt).toLocaleString() +
      " · " +
      version.actor;
    li.append(title, detail);
    if (version.config.revision !== current.revision) {
      const button = document.createElement("button");
      button.textContent = "Review restore";
      button.onclick = () =>
        review({
          expectedRevision: current.revision,
          config: version.config,
          rollbackRevision: version.config.revision,
        });
      li.append(button);
    }
    $("history").append(li);
  }
  tell("Environment " + env + " · Configuration is current.");
}
function review(body) {
  if (!current) return;
  proposed = body;
  const changes = [];
  for (const [name, before, after] of [
    [
      "Capture enabled",
      current.features.idCapture,
      body.config.features.idCapture,
    ],
    ...["documentType", "jurisdiction", "purpose"].map((n) => [
      n,
      current.capture[n],
      body.config.capture[n],
    ]),
  ]) {
    if (before !== after)
      changes.push(
        name + ": " + JSON.stringify(before) + " → " + JSON.stringify(after),
      );
  }
  $("diff").textContent =
    (body.rollbackRevision !== undefined
      ? "Restore revision " + body.rollbackRevision + " as a new revision.\n"
      : "") + (changes.join("\n") || "No setting values change.");
  $("review").hidden = false;
  $("review").scrollIntoView({ behavior: "smooth", block: "center" });
}
$("confirmPublish").onclick = () => {
  if (proposed) publish(proposed);
};
$("cancelPublish").onclick = () => {
  proposed = null;
  $("review").hidden = true;
};

async function publish(body) {
  if (!current || body.config.environment !== $("environment").value) return;
  $("publish").disabled = true;
  $("confirmPublish").disabled = true;
  try {
    await api("/api/environments/" + current.environment, {
      method: "POST",
      body: JSON.stringify(body),
    });
    await load();
    tell(
      "Published successfully. Applications receive this change on their next refresh.",
    );
  } catch (e) {
    tell(e.message);
    proposed = null;
    $("review").hidden = true;
    current = null;
    $("config").hidden = true;
  } finally {
    $("publish").disabled = false;
    $("confirmPublish").disabled = false;
  }
}
$("refresh").onclick = () => load().catch((e) => tell(e.message));
$("activatePasskey").onclick = async () => {
  try {
    await api("/api/passkey/activate", { method: "POST", body: "{}" });
    tell(
      "Passkey MFA is active. Your authenticator remains available as a fallback.",
    );
  } catch (e) {
    tell(
      e.message.includes("complete_totp")
        ? "Complete authenticator setup and add a passkey before activating passkey MFA."
        : e.message,
    );
  }
};
$("passkey").onclick = () => login(true).catch((e) => tell(e.message));
$("inspectGrant").onclick = async () => {
  const generation = loadGeneration,
    environment = current?.environment,
    subject = $("subject").value.trim();
  if (!environment || !subject) return;
  try {
    const grant = await api(
      "/api/environments/" +
        environment +
        "/grants?subject=" +
        encodeURIComponent(subject),
    );
    if (generation !== loadGeneration || subject !== $("subject").value.trim())
      return;
    $("grantRevision").value = grant.revision;
    $("grantActive").checked = grant.active;
    $("grantManage").checked = grant.manageGrants;
    tell("Loaded current grant revision " + grant.revision + ".");
  } catch (e) {
    tell(e.message);
  }
};
$("login").onclick = () => login().catch((e) => tell(e.message));
$("logout").onclick = () => {
  token = "";
  sessionStorage.removeItem("admin_pkce");
  const url = new URL(settings.loginDomain + "/logout");
  url.search = new URLSearchParams({
    client_id: settings.clientId,
    logout_uri: settings.origin + "/",
  });
  location.assign(url);
};
$("environment").onchange = () => load().catch((e) => tell(e.message));
$("config").onsubmit = (event) => {
  event.preventDefault();
  if (!current) return;
  const config = {
    ...current,
    features: { idCapture: $("enabled").checked },
    capture: {
      documentType: $("documentType").value.trim(),
      jurisdiction: $("jurisdiction").value.trim(),
      purpose: $("purpose").value.trim(),
    },
  };
  if (
    new TextEncoder().encode(config.capture.purpose).length > 1024 ||
    new TextEncoder().encode(config.capture.jurisdiction).length > 1024
  ) {
    tell("Purpose and jurisdiction must each be at most 1024 UTF-8 bytes.");
    return;
  }
  if (
    config.features.idCapture &&
    (config.capture.documentType === "sample_card" ||
      !config.capture.purpose ||
      !config.capture.jurisdiction)
  ) {
    tell(
      "Complete the document type, jurisdiction, and purpose before enabling capture.",
    );
    return;
  }
  review({ expectedRevision: current.revision, config });
};
$("grant").onsubmit = async (event) => {
  event.preventDefault();
  try {
    if (!current) return;
    const generation = loadGeneration;
    const grant = await api(
      "/api/environments/" + current.environment + "/grants",
      {
        method: "POST",
        body: JSON.stringify({
          subject: $("subject").value.trim(),
          environment: current.environment,
          revision: Number($("grantRevision").value),
          active: $("grantActive").checked,
          manageGrants: $("grantManage").checked,
        }),
      },
    );
    if (generation !== loadGeneration) return;
    $("grantRevision").value = grant.revision;
    tell("Grant saved at revision " + grant.revision + ".");
  } catch (e) {
    tell(e.message);
  }
};
(async () => {
  settings = await fetch("/settings", { cache: "no-store" }).then((r) =>
    r.json(),
  );
  if (await callback()) {
    grants = await api("/api/environments");
    for (const g of grants) {
      const option = document.createElement("option");
      option.value = g.environment;
      option.textContent = g.environment;
      $("environment").append(option);
    }
    $("logout").hidden = false;
    $("passkey").hidden = false;
    $("activatePasskey").hidden = false;
    $("refresh").hidden = false;
    if (!grants.length) {
      tell(
        "Your account has no active environment grants. Contact an administrator.",
      );
      return;
    }
    $("console").hidden = false;
    await load();
  } else {
    $("login").hidden = false;
    tell(
      "Invitation-only access · Password with authenticator MFA or a user-verified passkey.",
    );
  }
})().catch((e) => {
  tell(e.message);
  $("login").hidden = false;
});
