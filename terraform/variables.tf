terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    external = {
      source  = "hashicorp/external"
      version = "~> 2.3"
    }
  }
}

variable "project_id" {
  description = "GCP project ID (optional; defaults to active gcloud project)"
  type        = string
  default     = null
}

variable "region" {
  description = "GCP region for resources"
  type        = string
  default     = "europe-west1"
}

variable "proxy_port" {
  description = "Port the proxy listens on inside the container"
  type        = number
  default     = 8888
}

variable "memory" {
  description = "Memory allocation for Cloud Run service (e.g., '256Mi', '512Mi')"
  type        = string
  default     = "128Mi"
}

variable "cpu" {
  description = "CPU allocation for Cloud Run service (e.g., '1', '2')"
  type        = string
  default     = "1000m"
}

variable "startup_cpu_boost" {
  description = "Enable Cloud Run Startup CPU boost"
  type        = bool
  default     = true
}

variable "services" {
  description = "Map of Cloud Run services to deploy. Key is the service name."
  type = map(object({
    docker_repo_url             = string
    image_tag                   = string
    proxy_environment_variables = optional(map(string))
  }))
  default = {
    "secure-echo-lite" = {
      docker_repo_url = "europe-docker.pkg.dev/fp8main/docker/lite-auth-proxy"
      image_tag       = "1.1"
    }
    "secure-echo-flex" = {
      docker_repo_url = "europe-docker.pkg.dev/fp8main/docker/flex-auth-proxy"
      image_tag       = "1.1"
    }
  }
}

variable "proxy_default_environment_variables" {
  description = "Base environment variables applied to every proxy service. Per-service proxy_environment_variables are merged on top and take precedence."
  type        = map(string)
  default     = {}
}

variable "echo_environment_variables" {
  description = "Environment variables to pass to the echo container"
  type        = map(string)
  default = {
    JWT_HEADER          = "Authorization"
    LOG_WITHOUT_NEWLINE = "true"
  }
}
