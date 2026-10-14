# ShopVerse shortcuts. Run `make help` to see everything.
# Each target just calls a script in scripts/ — open them to see exactly what happens.

SHELL := /usr/bin/env bash
SERVICES := frontend api-gateway user-service product-service cart-service order-service \
            inventory-service payment-service recommendation-service notification-service

.DEFAULT_GOAL := help
.PHONY: help prereqs infra configure platform images deploy gitops all loadtest dashboards status \
        destroy local-up local-down test lint

help: ## Show this help
	@echo "ShopVerse on EKS — common commands"
	@echo
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36mmake %-12s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "First time? Follow docs/02-DEPLOYMENT-GUIDE.md"

prereqs: ## Step 0: check tools and AWS credentials
	@./scripts/00-check-prereqs.sh

infra: ## Step 1: create VPC + EKS + ECR with Terraform (~20 min)
	@./scripts/01-create-infrastructure.sh

configure: ## Step 2: write your account/region into the config files
	@./scripts/02-configure-repo.sh

platform: ## Step 3: install ALB controller, metrics-server, External Secrets, Prometheus/Grafana, Argo CD
	@./scripts/03-install-platform.sh

images: ## Step 4: build and push all 10 images to ECR
	@./scripts/04-build-and-push-images.sh

deploy: ## Step 5: deploy data stores + 10 microservices with Helm
	@./scripts/05-deploy-app.sh

gitops: ## Step 6: hand over to Argo CD — usage: make gitops REPO=https://github.com/you/shopverse-eks.git
	@./scripts/06-setup-gitops.sh "$(REPO)"

all: prereqs infra configure platform images deploy ## Steps 0-5 in one go

loadtest: ## Run the k6 load test — usage: make loadtest VUS=50 DURATION=10m
	@./scripts/07-run-load-test.sh "$(or $(VUS),20)" "$(or $(DURATION),5m)"

dashboards: ## Port-forward Grafana, Prometheus, Argo CD, RabbitMQ
	@./scripts/08-open-dashboards.sh

status: ## Show the state of the cluster and the app
	@./scripts/status.sh

destroy: ## Delete EVERYTHING in AWS (stop paying!)
	@./scripts/99-destroy-everything.sh

local-up: ## Run the whole platform locally with Docker Compose (http://localhost:8080)
	docker compose up --build -d
	@echo "Open http://localhost:8080  (RabbitMQ UI: http://localhost:15672)"

local-down: ## Stop the local platform and delete its data
	docker compose down -v

test: ## Run every service's unit tests inside Docker (same as CI)
	@for s in $(SERVICES); do echo "==> $$s"; docker build -q --target test -t shopverse/$$s:test services/$$s >/dev/null || exit 1; echo "    tests passed"; done

lint: ## Format-check Terraform and lint the Helm chart
	terraform -chdir=infra/terraform fmt -check -recursive
	helm lint deploy/helm/microservice -f deploy/helm/values/common.yaml -f deploy/helm/values/api-gateway.yaml
