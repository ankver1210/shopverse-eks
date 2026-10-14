# Keyless CI/CD: GitHub Actions gets short-lived AWS credentials through OpenID Connect.
# No long-lived AWS access keys stored in GitHub. Only the `main` branch may push images.

locals {
  enable_github = var.github_repository != ""
  github_oidc_provider_arn = local.enable_github ? (
    var.create_github_oidc_provider
    ? aws_iam_openid_connect_provider.github[0].arn
    : "arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:oidc-provider/token.actions.githubusercontent.com"
  ) : ""
}

resource "aws_iam_openid_connect_provider" "github" {
  count = local.enable_github && var.create_github_oidc_provider ? 1 : 0

  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

data "aws_iam_policy_document" "github_trust" {
  count = local.enable_github ? 1 : 0

  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [local.github_oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = ["repo:${var.github_repository}:ref:refs/heads/main"]
    }
  }
}

resource "aws_iam_role" "github_actions" {
  count = local.enable_github ? 1 : 0

  name               = "${var.cluster_name}-github-actions"
  description        = "Assumed by GitHub Actions (${var.github_repository}) to push images to ECR"
  assume_role_policy = data.aws_iam_policy_document.github_trust[0].json
}

data "aws_iam_policy_document" "github_ecr_push" {
  count = local.enable_github ? 1 : 0

  statement {
    sid       = "EcrLogin"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }

  statement {
    sid = "PushShopverseImages"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:CompleteLayerUpload",
      "ecr:DescribeImages",
      "ecr:DescribeRepositories",
      "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
    ]
    resources = [for repo in aws_ecr_repository.service : repo.arn]
  }
}

resource "aws_iam_role_policy" "github_ecr_push" {
  count = local.enable_github ? 1 : 0

  name   = "ecr-push"
  role   = aws_iam_role.github_actions[0].id
  policy = data.aws_iam_policy_document.github_ecr_push[0].json
}
