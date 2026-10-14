#!/usr/bin/env bash
# Step 1 — Create the AWS infrastructure with Terraform (VPC, EKS, ECR, IAM, secrets).
# Takes about 15-20 minutes. Usage: ./scripts/01-create-infrastructure.sh
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
require_cmd terraform aws kubectl

cat <<MSG

${BOLD}This creates real AWS resources that cost money while they exist:${RESET}
  - EKS control plane          ~\$0.10/hour
  - 3 worker nodes (Spot)      ~\$0.08/hour   (On-Demand ~\$0.25/hour)
  - NAT gateway + ALB          ~\$0.07/hour
  ${BOLD}Roughly \$0.25-0.45 per hour, ~\$6-10 per day.${RESET}

  Run ./scripts/99-destroy-everything.sh when you finish a session!
MSG
confirm "Create the infrastructure now?"

if [ ! -f "${TF_DIR}/terraform.tfvars" ]; then
  warn "No terraform.tfvars found — using defaults (us-east-1, Spot nodes)."
  info "To customise, copy infra/terraform/terraform.tfvars.example to terraform.tfvars."
fi

step "terraform init"
terraform -chdir="$TF_DIR" init -input=false -upgrade

step "terraform apply (grab a coffee, ~15-20 minutes)"
if [ "${AUTO_APPROVE:-0}" = "1" ]; then
  terraform -chdir="$TF_DIR" apply -input=false -auto-approve
else
  terraform -chdir="$TF_DIR" apply -input=false
fi

load_infra
step "Configuring kubectl"
aws eks update-kubeconfig --region "$REGION" --name "$CLUSTER_NAME"
kubectl get nodes -o wide

step "Infrastructure is ready"
info "Cluster: ${CLUSTER_NAME} (${REGION})"
info "Registry: ${ECR_REGISTRY}"
info "Next: ./scripts/02-configure-repo.sh"
