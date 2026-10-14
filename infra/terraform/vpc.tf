# Network layout (3 Availability Zones):
#   public subnets  -> internet-facing ALB and the NAT gateway
#   private subnets -> EKS worker nodes and pods (no public IPs)
module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.7"

  name = "${var.cluster_name}-vpc"
  cidr = var.vpc_cidr
  azs  = local.azs

  # /20 private subnets = 4096 IPs each: the AWS VPC CNI gives every pod a real VPC IP.
  private_subnets = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, _ in local.azs : cidrsubnet(var.vpc_cidr, 8, i + 48)]

  enable_nat_gateway = true
  # One NAT gateway saves ~$65/month. Production uses one per AZ (one_nat_gateway_per_az = true).
  single_nat_gateway = true

  # These tags tell the AWS Load Balancer Controller where to place load balancers.
  public_subnet_tags = {
    "kubernetes.io/role/elb" = 1
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb" = 1
  }
}
