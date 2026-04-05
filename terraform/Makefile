SHELL := /bin/bash

TF       ?= terraform
TF_DIR   ?= .
PLAN_OUT ?= tfplan

TFVARS_DIR  ?= tfvars
PROJECT     := $(shell gcloud config list --format='value(core.project)' 2>/dev/null | head -n1)
TFVARS_FILE  = $(TFVARS_DIR)/$(PROJECT).tfvars

.PHONY: help workspace init fmt validate plan apply destroy output state-list clean-state test

help:
	@echo "Terraform helper targets"
	@echo "  make workspace   # init + select/create workspace from active gcloud project"
	@echo "  make init        # terraform init"
	@echo "  make fmt         # terraform fmt -recursive"
	@echo "  make validate    # terraform validate (workspace-aware)"
	@echo "  make plan        # terraform plan using tfvars/<project>.tfvars"
	@echo "  make apply       # apply the saved plan"
	@echo "  make destroy     # terraform destroy using tfvars/<project>.tfvars"
	@echo "  make output      # terraform output"
	@echo "  make state-list  # terraform state list"
	@echo "  make clean-state # remove local state files for current workspace"
	@echo "  make test        # curl each service URL with a gcloud identity token"

workspace:
	@if [[ -z "$(PROJECT)" ]]; then \
		echo "No active gcloud project. Run: gcloud config set project <PROJECT_ID>"; \
		exit 1; \
	fi; \
	$(TF) -chdir=$(TF_DIR) init -input=false >/dev/null; \
	$(TF) -chdir=$(TF_DIR) workspace select -or-create "$(PROJECT)" >/dev/null; \
	echo "Using workspace: $(PROJECT)"

init:
	@$(TF) -chdir=$(TF_DIR) init -input=false

fmt:
	@$(TF) -chdir=$(TF_DIR) fmt -recursive

validate: workspace
	@test -f "$(TFVARS_FILE)" || { echo "Error: $(TFVARS_FILE) not found. Create it from terraform.tfvars.example."; exit 1; }
	@$(TF) -chdir=$(TF_DIR) validate

plan: workspace
	@test -f "$(TFVARS_FILE)" || { echo "Error: $(TFVARS_FILE) not found. Create it from terraform.tfvars.example."; exit 1; }
	@$(TF) -chdir=$(TF_DIR) plan -var-file=$(TFVARS_FILE) -out=$(PLAN_OUT)

refresh: workspace
	@test -f "$(TFVARS_FILE)" || { echo "Error: $(TFVARS_FILE) not found. Create it from terraform.tfvars.example."; exit 1; }
	@$(TF) -chdir=$(TF_DIR) apply --refresh-only -var-file=$(TFVARS_FILE)

apply: workspace
	@$(TF) -chdir=$(TF_DIR) apply $(PLAN_OUT)

destroy: workspace
	@test -f "$(TFVARS_FILE)" || { echo "Error: $(TFVARS_FILE) not found. Create it from terraform.tfvars.example."; exit 1; }
	@$(TF) -chdir=$(TF_DIR) destroy -var-file=$(TFVARS_FILE)

output: workspace
	@$(TF) -chdir=$(TF_DIR) output

state-list: workspace
	@$(TF) -chdir=$(TF_DIR) state list

test: workspace
	@token="$$( gcloud auth print-identity-token )"; \
	for name in $$($(TF) -chdir=$(TF_DIR) output -json service_urls 2>/dev/null | python3 -c "import sys,json; [print(k) for k in json.load(sys.stdin)]"); do \
		url="$$( $(TF) -chdir=$(TF_DIR) output -json service_urls | python3 -c "import sys,json; print(json.load(sys.stdin)['$$name'])" )"; \
		echo ""; \
		echo "=== $$name ==="; \
		echo "GET $$url"; \
		curl -si -H "Authorization: Bearer $$token" "$$url"; \
		echo ""; \
	done

clean-state: workspace
	@ws="$$( $(TF) -chdir=$(TF_DIR) workspace show )"; \
	rm -f "$(TF_DIR)/terraform.tfstate" "$(TF_DIR)/terraform.tfstate.backup"; \
	rm -f "$(TF_DIR)/terraform.tfstate.d/$$ws/terraform.tfstate"*; \
	echo "Removed local state files for workspace: $$ws"
