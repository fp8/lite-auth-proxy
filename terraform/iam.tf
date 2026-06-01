
# ── Service Account ───────────────────────────────────────────────────────────

resource "google_service_account" "proxy_sa" {
  project      = local.project_id
  account_id   = "lite-auth-proxy-sa"
  display_name = "Lite/Flex Auth Proxy"
  description  = "Runtime identity for lite-auth-proxy and flex-auth-proxy Cloud Run services"
}

# ── Firestore ─────────────────────────────────────────────────────────────────
# Derive the set of Firestore database names from services that have storage
# enabled. Access is granted per named database, nothing broader.

locals {
  _firestore_dbnames = toset(distinct(compact([
    for env_vars in values(local.service_env_vars) :
    lookup(env_vars, "PROXY_STORAGE_DBNAME", "")
    if lookup(env_vars, "PROXY_STORAGE_ENABLED", "false") == "true"
  ])))
}

resource "google_project_iam_member" "proxy_sa_firestore" {
  for_each = local._firestore_dbnames

  project = local.project_id
  role    = "roles/datastore.user"
  member  = "serviceAccount:${google_service_account.proxy_sa.email}"

  condition {
    title      = "firestore-${each.key}-only"
    description = "Restrict Firestore access to the ${each.key} named database"
    expression  = "resource.name.startsWith(\"projects/${local.project_id}/databases/${each.key}\")"
  }
}

# ── Secret Manager ────────────────────────────────────────────────────────────
# Grant secretAccessor only when at least one service enables API-key auth,
# which mounts the APIKEY-SECURE-ECHO secret.

locals {
  _apikey_secret_needed = anytrue([
    for env_vars in values(local.service_env_vars) :
    lookup(env_vars, "PROXY_AUTH_API_KEY_ENABLED", "false") == "true"
  ])
}

resource "google_secret_manager_secret_iam_member" "proxy_sa_apikey" {
  count = local._apikey_secret_needed ? 1 : 0

  project   = local.project_id
  secret_id = "APIKEY-SECURE-ECHO"
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.proxy_sa.email}"
}
