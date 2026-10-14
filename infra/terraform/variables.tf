variable "region" {
  description = "AWS region to deploy into."
  type        = string
  default     = "us-east-1"
}

variable "project_name" {
  description = "Used for naming and tagging."
  type        = string
  default     = "shopverse"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)."
  type        = string
  default     = "dev"
}

variable "cluster_name" {
  description = "Name of the EKS cluster."
  type        = string
  default     = "shopverse-eks"
}

variable "kubernetes_version" {
  description = "EKS Kubernetes version. 1.36 is in standard support until Aug 2027 (upgrading to 1.37 is one of the labs)."
  type        = string
  default     = "1.36"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "node_instance_types" {
  description = "Instance types for the worker nodes. Several similar types make Spot capacity easier to get."
  type        = list(string)
  default     = ["t3.large", "t3a.large", "m5.large", "m5a.large", "m6i.large"]
}

variable "node_capacity_type" {
  description = "SPOT (~60-70% cheaper, can be interrupted) or ON_DEMAND."
  type        = string
  default     = "SPOT"

  validation {
    condition     = contains(["SPOT", "ON_DEMAND"], var.node_capacity_type)
    error_message = "node_capacity_type must be SPOT or ON_DEMAND."
  }
}

variable "node_desired_size" {
  description = "Number of worker nodes to start with."
  type        = number
  default     = 3
}

variable "node_min_size" {
  description = "Minimum number of worker nodes."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Maximum number of worker nodes."
  type        = number
  default     = 5
}

variable "cluster_endpoint_public_access_cidrs" {
  description = "Who may reach the Kubernetes API over the internet. Lock this to your own IP (x.x.x.x/32) for better security."
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

variable "control_plane_log_types" {
  description = "EKS control plane logs sent to CloudWatch (small cost). Set to [] to disable."
  type        = list(string)
  default     = ["api", "audit", "authenticator"]
}

variable "ecr_image_tag_mutability" {
  description = "MUTABLE is friendlier while learning; IMMUTABLE is the production best practice."
  type        = string
  default     = "MUTABLE"
}

variable "github_repository" {
  description = "Your GitHub repository as 'owner/name' (e.g. 'jane/shopverse-eks'). Enables keyless CI/CD to ECR. Leave empty to skip."
  type        = string
  default     = ""
}

variable "create_github_oidc_provider" {
  description = "Set to false if your AWS account already has the GitHub OIDC provider (token.actions.githubusercontent.com)."
  type        = bool
  default     = true
}
