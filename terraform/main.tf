terraform {
  backend "gcs" {
    bucket = "fp8main-terraform"
    prefix = "lite-auth-proxy"
  }
}

provider "google" {
  region = var.region
}

# Data source to get current project details
data "google_client_config" "default" {}

data "external" "gcloud_project" {
  program = [
    "bash",
    "--noprofile",
    "--norc",
    "-c",
    "project=\"$(gcloud config get-value project --quiet 2>/dev/null | tr -d '\\r' | grep -E '^[a-z][a-z0-9-]{4,28}[a-z0-9]$' | head -n1 || true)\"; printf '{\"project\":\"%s\"}' \"$project\""
  ]
}

locals {
  gcloud_project_id = try(data.external.gcloud_project.result.project, "")
  project_id        = var.project_id != null && var.project_id != "" ? var.project_id : (local.gcloud_project_id != "" ? local.gcloud_project_id : null)
}

check "project_id_is_configured" {
  assert {
    condition     = local.project_id != null && local.project_id != ""
    error_message = "No GCP project ID resolved. Set var.project_id or run: gcloud config set project <PROJECT_ID>"
  }
}

data "google_project" "current" {
  project_id = local.project_id
}

# Enable required APIs
resource "google_project_service" "required_apis" {
  for_each = toset([
    "run.googleapis.com",
  ])

  project            = local.project_id
  service            = each.value
  disable_on_destroy = false
}

# Data source for shared artifact registry in fp8main
data "google_artifact_registry_repository" "shared_docker_repo" {
  project       = "fp8main"
  location      = "europe"
  repository_id = "docker"
}

# Grant read access to shared artifact registry if target project is different
resource "google_artifact_registry_repository_iam_member" "read_access" {
  count      = local.project_id != "fp8main" ? 1 : 0
  project    = "fp8main"
  location   = "europe"
  repository = data.google_artifact_registry_repository.shared_docker_repo.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:service-${data.google_project.current.number}@serverless-robot-prod.iam.gserviceaccount.com"
}

# Resolve per-service environment variables: base defaults merged with per-service additions.
# Per-service entries take precedence on key conflicts.
locals {
  service_env_vars = {
    for name, svc in var.services :
    name => merge(var.proxy_default_environment_variables, coalesce(svc.proxy_environment_variables, {}))
  }
}

# Cloud Run services — one per entry in var.services
resource "google_cloud_run_v2_service" "proxy" {
  for_each = var.services

  name     = each.key
  project  = local.project_id
  location = var.region

  ingress = "INGRESS_TRAFFIC_ALL"

  template {
    service_account                  = google_service_account.proxy_sa.email
    timeout                          = "30s"
    max_instance_request_concurrency = 20

    containers {
      name    = "proxy"
      image   = "${each.value.docker_repo_url}:${each.value.image_tag}"
      command = []
      args    = []

      # Port configuration
      ports {
        container_port = var.proxy_port
      }

      startup_probe {
        initial_delay_seconds = 2
        timeout_seconds       = 1
        failure_threshold     = 3
        period_seconds        = 1

        http_get {
          path = "/healthz"
          port = var.proxy_port
        }
      }

      # Resource limits
      resources {
        cpu_idle          = true
        startup_cpu_boost = var.startup_cpu_boost
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      # Environment variables
      dynamic "env" {
        for_each = local.service_env_vars[each.key]
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = lookup(local.service_env_vars[each.key], "PROXY_AUTH_API_KEY_ENABLED", "false") == "true" ? [1] : []
        content {
          name = "API_KEY_SECRET"
          value_source {
            secret_key_ref {
              secret  = "APIKEY-SECURE-ECHO"
              version = "latest"
            }
          }
        }
      }
    }

    containers {
      name    = "echo"
      image   = "mendhak/http-https-echo:39"
      command = []

      # Resource limits
      resources {
        cpu_idle          = true
        startup_cpu_boost = var.startup_cpu_boost
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      # Environment variables
      dynamic "env" {
        for_each = var.echo_environment_variables
        content {
          name  = env.key
          value = env.value
        }
      }
    }

    scaling {
      max_instance_count = 1
      min_instance_count = 0
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }

  depends_on = [
    google_project_service.required_apis,
    data.google_artifact_registry_repository.shared_docker_repo,
    google_service_account.proxy_sa,
    google_project_iam_member.proxy_sa_firestore,
    google_secret_manager_secret_iam_member.proxy_sa_apikey,
  ]
}

# Allow public access to each Cloud Run service
resource "google_cloud_run_v2_service_iam_member" "public_access" {
  for_each = var.services

  project  = google_cloud_run_v2_service.proxy[each.key].project
  location = google_cloud_run_v2_service.proxy[each.key].location
  name     = google_cloud_run_v2_service.proxy[each.key].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
