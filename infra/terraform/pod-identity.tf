# EKS Pod Identity: give individual Kubernetes service accounts their own IAM role.
# No AWS keys in the cluster, and each add-on gets only the permissions it needs.

module "ebs_csi_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 2.9"

  name                      = "${var.cluster_name}-ebs-csi"
  attach_aws_ebs_csi_policy = true
  # The association itself is created by the EKS add-on (see eks.tf).
}

module "aws_lb_controller_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 2.9"

  name                            = "${var.cluster_name}-aws-lbc"
  attach_aws_lb_controller_policy = true

  associations = {
    controller = {
      cluster_name    = module.eks.cluster_name
      namespace       = "kube-system"
      service_account = "aws-load-balancer-controller"
    }
  }
}

module "external_secrets_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "~> 2.9"

  name                           = "${var.cluster_name}-external-secrets"
  attach_external_secrets_policy = true
  # Least privilege: read ONLY the ShopVerse secret.
  external_secrets_secrets_manager_arns = [aws_secretsmanager_secret.app.arn]

  associations = {
    operator = {
      cluster_name    = module.eks.cluster_name
      namespace       = "external-secrets"
      service_account = "external-secrets"
    }
  }
}
