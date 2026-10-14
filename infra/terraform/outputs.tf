output "region" {
  description = "AWS region."
  value       = var.region
}

output "account_id" {
  description = "AWS account ID."
  value       = data.aws_caller_identity.current.account_id
}

output "cluster_name" {
  description = "EKS cluster name."
  value       = module.eks.cluster_name
}

output "cluster_version" {
  description = "Kubernetes version of the control plane."
  value       = module.eks.cluster_version
}

output "vpc_id" {
  description = "VPC ID (needed by the AWS Load Balancer Controller)."
  value       = module.vpc.vpc_id
}

output "ecr_registry" {
  description = "ECR registry host for docker login / image names."
  value       = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"
}

output "ecr_repositories" {
  description = "ECR repository URL per service."
  value       = { for name, repo in aws_ecr_repository.service : name => repo.repository_url }
}

output "app_secret_name" {
  description = "Secrets Manager secret holding all generated passwords."
  value       = aws_secretsmanager_secret.app.name
}

output "github_actions_role_arn" {
  description = "Put this in your GitHub repo as the AWS_ROLE_ARN variable (empty if github_repository is not set)."
  value       = local.enable_github ? aws_iam_role.github_actions[0].arn : ""
}

output "configure_kubectl" {
  description = "Command that points kubectl at the new cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}
