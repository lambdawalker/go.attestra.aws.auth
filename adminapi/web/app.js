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
const passwordClasses = ["lowercase", "uppercase", "numbers", "symbols"];
function onboardingFor(config) {
  // Historical revisions predate onboarding. Never mutate them while reviewing.
  return config.onboarding || {
    idCapture: config.features.idCapture ? "optional" : "disabled",
    passkey: "required",
    password: "disabled",
    passwordPlacement: "hidden",
    passwordPolicy: {
      minLength: 12, maxLength: 128,
      lowercase: "allowed", uppercase: "allowed", numbers: "allowed", symbols: "allowed",
    },
  };
}
$("passwordRequirement").onchange = () => {
  if ($("passwordRequirement").value === "disabled") $("passwordPlacement").value = "hidden";
  else if ($("passwordPlacement").value === "hidden") $("passwordPlacement").value = "after_verification";
};
$("passwordPlacement").onchange = () => {
  if ($("passwordPlacement").value === "hidden") $("passwordRequirement").value = "disabled";
  else if ($("passwordRequirement").value === "disabled") $("passwordRequirement").value = "optional";
};
let resetState = null;
let resetTimer;
async function load() {
  const generation = ++loadGeneration,
    env = $("environment").value;
  current = null;
  proposed = null;
  resetState = null;
  clearTimeout(resetTimer);
  $("resetPanel").hidden = true;
  $("resetConfirmation").value = "";
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
  const onboarding = onboardingFor(current);
  for (const key of ["idCapture", "passkey", "password"]) $(key + "Requirement").value = onboarding[key];
  $("passwordPlacement").value = onboarding.passwordPlacement;
  for (const key of ["minLength", "maxLength", ...passwordClasses]) $(key).value = onboarding.passwordPolicy[key];
  const messages = current.capture.messages || {};
  $("purposeEn").value =
    messages.en ||
    "Scan your ID to provide your identity details. You'll review and confirm the information afterward.";
  $("purposeEs").value =
    messages.es ??
    "Escanea tu identificación para proporcionar tus datos de identidad. Después podrás revisar y confirmar la información.";
  $("purposeFr").value =
    messages.fr ??
    "Numérisez votre pièce d’identité pour fournir vos renseignements d’identité. Vous pourrez ensuite les vérifier et les confirmer.";
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
  if (grants.find((g) => g.environment === env)?.manageGrants) {
    $("resetPanel").hidden = false;
    refreshReset();
  }
}
function review(body) {
  if (!current) return;
  proposed = body;
  const changes = [];
  for (const [name, before, after] of [
    ...[ ["ID capture", "idCapture"], ["Passkey", "passkey"], ["Password requirement", "password"], ["Password placement", "passwordPlacement"] ].map(([label, key]) => [label, onboardingFor(current)[key], onboardingFor(body.config)[key]]),
    ...[ ["Minimum length", "minLength"], ["Maximum length", "maxLength"], ["Lowercase", "lowercase"], ["Uppercase", "uppercase"], ["Numbers", "numbers"], ["Symbols", "symbols"] ].map(([label, key]) => [label, onboardingFor(current).passwordPolicy[key], onboardingFor(body.config).passwordPolicy[key]]),
    ...["en", "es", "fr"].map((n) => [
      "Disclosure " + n,
      current.capture.messages?.[n] || "",
      body.config.capture.messages?.[n] || "",
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
  const onboarding = {
    idCapture: $("idCaptureRequirement").value,
    passkey: $("passkeyRequirement").value,
    password: $("passwordRequirement").value,
    passwordPlacement: $("passwordPlacement").value,
    passwordPolicy: {
      minLength: Number($("minLength").value),
      maxLength: Number($("maxLength").value),
      ...Object.fromEntries(passwordClasses.map((key) => [key, $(key).value])),
    },
  };
  const policy = onboarding.passwordPolicy;
  if (!Number.isInteger(policy.minLength) || !Number.isInteger(policy.maxLength) ||
      policy.minLength < 8 || policy.maxLength > 128 || policy.minLength > policy.maxLength) {
    tell("Password length must use whole numbers from 8 to 128, with minimum no greater than maximum.");
    return;
  }
  if (passwordClasses.every((key) => policy[key] === "disallowed")) {
    tell("At least one password character class must be allowed or required.");
    return;
  }
  const config = {
    ...current,
    onboarding,
    features: { ...current.features, idCapture: onboarding.idCapture !== "disabled" },
    capture: {
      documentType: "id_card",
      jurisdiction: "",
      purpose: $("purposeEn").value.trim(),
      messages: {
        en: $("purposeEn").value.trim(),
        es: $("purposeEs").value.trim(),
        fr: $("purposeFr").value.trim(),
      },
    },
  };
  if (
    !config.capture.messages.en ||
    Object.values(config.capture.messages).some(
      (v) => new TextEncoder().encode(v).length > 1024,
    )
  ) {
    tell(
      "English disclosure is required. Each translation must be at most 1024 UTF-8 bytes.",
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

async function refreshReset() {
  const env = current?.environment,
    generation = loadGeneration;
  if (!env) return;
  clearTimeout(resetTimer);
  try {
    const result = await api("/api/environments/" + env + "/reset");
    if (generation !== loadGeneration || current?.environment !== env) return;
    resetState = result;
    const state = result.state;
    $("resetStatus").textContent = !result.enabled
      ? "Reset is disabled for this environment."
      : state.error
        ? "Reset paused after an error during " +
          state.phase +
          ". Resolve the operational issue, then confirm the environment name to resume."
        : state.phase && state.phase !== "complete"
          ? "Reset in progress: " +
            state.phase +
            ". The backend is temporarily unavailable. Draining active work takes at least seven minutes."
          : state.phase === "complete"
            ? "Reset completed. Users must sign in again."
            : "No reset has run.";
    updateResetButton();
    if (state.phase && state.phase !== "complete" && !state.error)
      resetTimer = setTimeout(refreshReset, 5000);
  } catch (e) {
    if (generation !== loadGeneration) return;
    resetState = null;
    updateResetButton();
    $("resetStatus").textContent =
      "Reset status unavailable. Deploy backend reset support, or refresh to retry.";
  }
}
function updateResetButton() {
  const active =
    resetState?.state.phase && resetState.state.phase !== "complete";
  $("resetEnvironment").disabled =
    !resetState?.enabled ||
    $("resetConfirmation").value !== current?.environment ||
    (active && !resetState.state.error);
  $("resetEnvironment").textContent = resetState?.state.error
    ? "Resume environment reset"
    : "Reset environment";
}
$("resetConfirmation").oninput = updateResetButton;
$("refreshReset").onclick = refreshReset;
$("resetEnvironment").onclick = async () => {
  if (!current || !resetState || $("resetEnvironment").disabled) return;
  const env = current.environment,
    generation = loadGeneration;
  const request = {
    action: resetState.state.error ? "resume" : "start",
    confirmation: $("resetConfirmation").value,
    operation: resetState.state.error
      ? resetState.state.operation
      : crypto.randomUUID(),
  };
  $("resetEnvironment").disabled = true;
  try {
    await api("/api/environments/" + env + "/reset", {
      method: "POST",
      body: JSON.stringify(request),
    });
    if (generation !== loadGeneration) return;
    $("resetConfirmation").value = "";
  } catch (e) {
    if (generation === loadGeneration)
      tell(
        "Reset request could not be confirmed. Refresh status before retrying.",
      );
  }
  if (generation === loadGeneration) await refreshReset();
};
