provider "aws" {
  region = var.region

  # Every AWS resource gets these tags: easy cost tracking and cleanup.
  default_tags {
    tags = local.tags
  }
}

data "aws_caller_identity" "current" {}
data "aws_partition" "current" {}

data "aws_availability_zones" "available" {
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)

  tags = {
    Project     = var.project_name
    Environment = var.environment
    ManagedBy   = "terraform"
    Repository  = "shopverse-eks"
  }

  services = [
    "frontend",
    "api-gateway",
    "user-service",
    "product-service",
    "cart-service",
    "order-service",
    "inventory-service",
    "payment-service",
    "recommendation-service",
    "notification-service",
  ]
}
