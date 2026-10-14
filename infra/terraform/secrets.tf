# All application secrets are generated here and stored in AWS Secrets Manager.
# External Secrets Operator syncs them into Kubernetes. Nothing secret is ever committed to Git.

locals {
  generated_secret_keys = [
    "postgres-password",
    "user-service-db-password",
    "product-service-db-password",
    "order-service-db-password",
    "inventory-service-db-password",
    "payment-service-db-password",
    "redis-password",
    "rabbitmq-password",
    "grafana-admin-password",
  ]
}

resource "random_password" "generated" {
  for_each = toset(local.generated_secret_keys)

  length  = 32
  special = false # alphanumeric only: avoids quoting problems in connection strings
}

resource "random_password" "jwt" {
  length  = 48
  special = false
}

resource "aws_secretsmanager_secret" "app" {
  name        = "${var.project_name}/app-secrets"
  description = "ShopVerse application secrets (database passwords, JWT signing key, ...)"
  # 0 = delete immediately on destroy, so you can recreate the environment right away.
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "app" {
  secret_id = aws_secretsmanager_secret.app.id
  secret_string = jsonencode(merge(
    { for key, pw in random_password.generated : key => pw.result },
    { "jwt-secret" = random_password.jwt.result }
  ))
}
