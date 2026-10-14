#!/usr/bin/env bash
# Tear EVERYTHING down so you stop paying. Takes about 15-20 minutes.
#
# Order matters! Kubernetes created some AWS resources itself (the ALB, EBS volumes,
# security groups). If we delete the cluster first those are orphaned, keep costing money,
# and block the VPC from being deleted. So we:
#   1. stop Argo CD from re-creating apps
#   2. delete the app namespaces  -> the ALB and EBS volumes are removed by their controllers
#   3. wait until AWS confirms they are gone
#   4. terraform destroy
# shellcheck source=lib/common.sh
source "$(dirname "$0")/lib/common.sh"
load_infra

cat <<MSG

${RED}${BOLD}This permanently deletes the ShopVerse environment:${RESET}
  cluster ${CLUSTER_NAME}, all data (orders, users), container images, secrets, VPC.
MSG
if [ "${AUTO_APPROVE:-0}" != "1" ]; then
  printf '\nType the cluster name (%s) to confirm: ' "$CLUSTER_NAME"
  read -r typed
  [ "$typed" = "$CLUSTER_NAME" ] || die "Name did not match. Nothing was deleted."
fi

if aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" >/dev/null 2>&1; then
  aws eks update-kubeconfig --region "$REGION" --name "$CLUSTER_NAME" >/dev/null

  step "1/4 Stopping Argo CD (so it does not re-create anything)"
  kubectl -n argocd delete applicationset --all --ignore-not-found --wait=true 2>/dev/null || true
  kubectl -n argocd delete applications --all --ignore-not-found --wait=true 2>/dev/null || true
  ok "Argo CD applications removed"

  step "2/4 Deleting application namespaces (removes the ALB and EBS volumes)"
  kubectl delete ingress --all -n "$APP_NAMESPACE" --ignore-not-found --wait=true 2>/dev/null || true
  kubectl delete namespace "$APP_NAMESPACE" "$DATA_NAMESPACE" --ignore-not-found --wait=true --timeout=10m 2>/dev/null || warn "namespace deletion timed out, continuing"
  ok "Namespaces deleted"

  step "3/4 Waiting for AWS to release load balancers and volumes"
  for _ in $(seq 1 60); do
    lbs="$(aws elbv2 describe-load-balancers --region "$REGION" --query "LoadBalancers[?VpcId=='${VPC_ID}'].LoadBalancerArn" --output text 2>/dev/null || true)"
    [ -z "$lbs" ] && break
    printf '.'; sleep 10
  done
  echo
  if [ -z "${lbs:-}" ]; then ok "No load balancers left in the VPC"; else warn "Load balancers still present: ${lbs}"; fi

  for _ in $(seq 1 30); do
    vols="$(aws ec2 describe-volumes --region "$REGION" \
      --filters "Name=tag:kubernetes.io/created-for/pvc/namespace,Values=${DATA_NAMESPACE}" \
      --query 'Volumes[].VolumeId' --output text 2>/dev/null || true)"
    [ -z "$vols" ] && break
    printf '.'; sleep 10
  done
  echo
  if [ -z "${vols:-}" ]; then ok "No ShopVerse EBS volumes left"; else warn "Volumes still present: ${vols} (delete them in the EC2 console)"; fi

  # Security groups the load balancer controller created, if any are left behind.
  sgs="$(aws ec2 describe-security-groups --region "$REGION" \
    --filters "Name=vpc-id,Values=${VPC_ID}" "Name=tag-key,Values=elbv2.k8s.aws/cluster" \
    --query 'SecurityGroups[].GroupId' --output text 2>/dev/null || true)"
  for sg in $sgs; do
    if aws ec2 delete-security-group --region "$REGION" --group-id "$sg" 2>/dev/null; then
      ok "deleted leftover security group ${sg}"
    fi
  done
else
  warn "Cluster ${CLUSTER_NAME} not found, skipping Kubernetes cleanup."
fi

step "4/4 terraform destroy (~10-15 minutes)"
terraform -chdir="$TF_DIR" destroy -input=false -auto-approve

step "Everything is deleted"
info "Double-check in the AWS console (EC2 > Load Balancers, EC2 > Volumes, VPC) that nothing is left."
info "Billing dashboard: https://console.aws.amazon.com/billing/home"
