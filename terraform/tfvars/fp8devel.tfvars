# Terraform variables for secure-echo deployment to fp8devel project

project_id = "fp8devel"
region     = "europe-west1"

proxy_port        = 8888
memory            = "128Mi"
cpu               = "1000m"
startup_cpu_boost = true

# Base environment variables — applied to all services (lite + flex)
proxy_default_environment_variables = {
  PROXY_AUTH_JWT_ISSUER                 = "https://accounts.google.com"
  PROXY_AUTH_JWT_AUDIENCE               = "32555940559.apps.googleusercontent.com"
  PROXY_AUTH_JWT_FILTERS_EMAIL_VERIFIED = "true"
  PROXY_AUTH_JWT_FILTERS_HD             = "farport.co"
  PROXY_SERVER_HEALTH_CHECK_TARGET      = "http://localhost:8080/healthcheck"
}

services = {
  "secure-echo-lite" = {
    docker_repo_url = "europe-docker.pkg.dev/fp8main/docker/lite-auth-proxy"
    image_tag       = "1.2"
  }
  "secure-echo-flex" = {
    docker_repo_url = "europe-docker.pkg.dev/fp8main/docker/flex-auth-proxy"
    image_tag       = "1.2"

    # Flex-only additions merged on top of base variables above
    proxy_environment_variables = {
      PROXY_ADMIN_ENABLED        = "true"
      PROXY_ADMIN_JWT_FILTERS_HD = "farport.co"
      PROXY_STORAGE_ENABLED      = "true"
      PROXY_STORAGE_DBNAME       = "flex-auth-proxy"
    }
  }
}
