module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 21.29"

  name               = var.cluster_name
  kubernetes_version = var.kubernetes_version

  # Public endpoint so you can run kubectl from your laptop. Restrict the CIDRs for safety.
  endpoint_public_access       = true
  endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs

  # Whoever runs `terraform apply` becomes cluster admin (via EKS access entries).
  enable_cluster_creator_admin_permissions = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  enabled_log_types                      = var.control_plane_log_types
  cloudwatch_log_group_retention_in_days = 7

  # EKS managed add-ons (AWS installs, patches and upgrades them).
  addons = {
    vpc-cni = {
      before_compute = true
      # Turns on the network policy agent so our NetworkPolicies are actually enforced.
      configuration_values = jsonencode({
        enableNetworkPolicy = "true"
      })
    }
    eks-pod-identity-agent = {
      before_compute = true
    }
    coredns    = {}
    kube-proxy = {}
    aws-ebs-csi-driver = {
      pod_identity_association = [{
        role_arn        = module.ebs_csi_pod_identity.iam_role_arn
        service_account = "ebs-csi-controller-sa"
      }]
    }
  }

  eks_managed_node_groups = {
    default = {
      ami_type       = "AL2023_x86_64_STANDARD"
      instance_types = var.node_instance_types
      capacity_type  = var.node_capacity_type

      min_size     = var.node_min_size
      max_size     = var.node_max_size
      desired_size = var.node_desired_size

      # EKS replaces unhealthy nodes automatically.
      node_repair_config = {
        enabled = true
      }

      # Lets you open a shell on a node with AWS Systems Manager (no SSH keys needed).
      iam_role_additional_policies = {
        ssm = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonSSMManagedInstanceCore"
      }

      labels = {
        workload = "general"
      }
    }
  }
}
