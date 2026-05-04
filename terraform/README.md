# secure-echo

Terraform configuration for deploying **secure-echo-lite** and **secure-echo-flex** Cloud Run services. Each service pairs a [lite-auth-proxy](/) or [flex-auth-proxy](/) sidecar with a simple HTTP echo container, providing a reference implementation for adding authentication to Cloud Run services.

## Services

| Service | Proxy image | Description |
|---|---|---|
| `secure-echo-lite` | `lite-auth-proxy` | Minimal proxy — no plugins |
| `secure-echo-flex` | `flex-auth-proxy` | Full proxy — all plugins enabled |

## Terraform State

State is stored **locally** using Terraform workspaces, one workspace per GCP project.

| Workspace | State location |
|---|---|
| `default` | `terraform.tfstate` |
| `<project>` | `terraform.tfstate.d/<project>/terraform.tfstate` |

## Makefile Shortcuts

Common Terraform commands are wrapped in `Makefile` targets. Each target automatically uses a workspace named after the active `gcloud` project.

```bash
make workspace   # init + select/create workspace from active gcloud project
make validate    # validate Terraform configuration
make plan        # terraform plan
make apply       # terraform apply
make destroy     # terraform destroy
make test        # curl each service URL with a gcloud identity token
```

Additional helpers:

```bash
make output      # show outputs
make state-list  # list state resources
make clean-state # reset current workspace state
```

## Deploying to a Specific GCP Project

```bash
gcloud config set project YOUR_PROJECT_ID
cd terraform
make workspace
make plan
make apply
```

To deploy to another project:

```bash
gcloud config set project OTHER_PROJECT_ID
make workspace
make plan
make apply
```

## Variables

Each GCP project has its own var file in `tfvars/<project>.tfvars`. The Makefile automatically selects the file matching the active `gcloud` project — if the file does not exist, `make plan`/`apply`/`destroy` will error out.

To add a new project:

```bash
cp terraform.tfvars.example tfvars/<project>.tfvars
# edit tfvars/<project>.tfvars — set project_id, services, env vars, etc.
gcloud config set project <project>
make workspace
make plan
make apply
```

See `terraform.tfvars.example` for all supported variables and their defaults.

## Per-Service Environment Overrides

Each entry in the `services` map accepts an optional `proxy_environment_variables` field. When set, it overrides `proxy_default_environment_variables` for that service only:

```hcl
services = {
  "secure-echo-lite" = {
    docker_repo_url = "europe-docker.pkg.dev/fp8main/docker/lite-auth-proxy"
    image_tag       = "1.1"
    # uses proxy_default_environment_variables
  }
  "secure-echo-flex" = {
    docker_repo_url             = "europe-docker.pkg.dev/fp8main/docker/flex-auth-proxy"
    image_tag                   = "1.1"
    proxy_environment_variables = {
      PROXY_AUTH_JWT_ISSUER            = "https://accounts.google.com"
      PROXY_AUTH_JWT_AUDIENCE          = "32555940559.apps.googleusercontent.com"
      PROXY_AUTH_API_KEY_ENABLED       = "false"
      PROXY_SERVER_HEALTH_CHECK_TARGET = "http://localhost:8080/healthcheck"
    }
  }
}
```

## Resetting Local State

```bash
make clean-state
```
