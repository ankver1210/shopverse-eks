# ShopVerse on Amazon EKS — Step-by-Step Deployment Guide

This guide is written for someone who **doesn't code**. You will copy and paste commands; every step explains what it does, what you should see, and what to do if something goes wrong.

| | |
|---|---|
| **Time** | ~1.5 hours the first time (mostly waiting), ~40 minutes after that |
| **Cost** | About **$0.30–0.45 per hour** while it runs (~$7–10/day). Destroy it after each session (Part E). |
| **You need** | An AWS account, a computer (Mac, Linux, or Windows with WSL2), ~10 GB free disk |

> **The golden rule:** AWS charges by the hour. When you finish practising, run `./scripts/99-destroy-everything.sh`. You can recreate everything in ~40 minutes next time.

---

## How it fits together (1-minute version)

```
You (browser) ──► AWS Load Balancer ──► frontend (web page)
                         │
                         └─/api──► api-gateway ──► 8 backend microservices
                                                      │
                                    PostgreSQL · Redis · RabbitMQ  (inside the cluster)
```

You run 6 numbered scripts in order. Each one does one job:

| Step | Script | What it does | Time |
|---|---|---|---|
| 0 | `00-check-prereqs.sh` | Checks your tools and AWS login | 1 min |
| 1 | `01-create-infrastructure.sh` | Terraform creates the network, EKS cluster, image registries, secrets | 15–20 min |
| 2 | `02-configure-repo.sh` | Writes your AWS account ID and region into the config files | 10 sec |
| 3 | `03-install-platform.sh` | Installs the cluster add-ons (load balancer controller, monitoring, GitOps…) | 8–10 min |
| 4 | `04-build-and-push-images.sh` | Builds the 10 container images and uploads them to AWS | 10–15 min |
| 5 | `05-deploy-app.sh` | Deploys the databases and the 10 microservices, prints your store URL | 5 min |

---

## Part A — One-time setup

### A1. Create an AWS account and a login for the command line

1. Create an account at <https://aws.amazon.com> (if you don't have one).
2. **Set a budget alert first** (protects you from surprises): AWS Console → search **Budgets** → *Create budget* → *Use a template* → **Monthly cost budget** → amount `30` → your email → *Create*.
3. Create command-line credentials. The simplest way for a personal learning account:
   - Console → **IAM** → *Users* → *Create user* → name `eks-learner` → *Attach policies directly* → tick **AdministratorAccess** → *Create user*.
   - Open the user → *Security credentials* → *Create access key* → choose **Command Line Interface (CLI)** → copy the **Access key ID** and **Secret access key** (you'll paste them in step A3).

   > In a company you would use AWS IAM Identity Center (SSO) instead of access keys. For a personal sandbox, an admin user is fine — just never share or commit the keys.

### A2. Install the tools

You need: **AWS CLI, Terraform, kubectl, Helm, Docker, Git, jq**.

<details open>
<summary><b>macOS</b> (using Homebrew)</summary>

```bash
# Install Homebrew if you don't have it: https://brew.sh
brew install awscli kubectl helm jq git
brew tap hashicorp/tap && brew install hashicorp/tap/terraform
```
Then install **Docker Desktop** from <https://www.docker.com/products/docker-desktop/> and start it.
</details>

<details>
<summary><b>Windows</b> (use WSL2 — all scripts are bash)</summary>

1. Open PowerShell **as Administrator** and run `wsl --install`, then restart. This gives you Ubuntu Linux.
2. Install **Docker Desktop**, and in *Settings → Resources → WSL integration* turn it on for Ubuntu.
3. Open the **Ubuntu** app and run everything below (and the rest of this guide) there:

```bash
sudo apt-get update && sudo apt-get install -y unzip jq git curl gnupg software-properties-common
# AWS CLI v2
curl -sSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o awscliv2.zip && unzip -q awscliv2.zip && sudo ./aws/install && rm -rf aws awscliv2.zip
# kubectl
curl -sSLO "https://dl.k8s.io/release/$(curl -sSL https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && sudo install kubectl /usr/local/bin/ && rm kubectl
# Helm
curl -sSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-4 | bash
# Terraform
wget -qO- https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp.gpg
echo "deb [signed-by=/usr/share/keyrings/hashicorp.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" | sudo tee /etc/apt/sources.list.d/hashicorp.list
sudo apt-get update && sudo apt-get install -y terraform
```
</details>

<details>
<summary><b>Linux</b> (Ubuntu/Debian)</summary>

Same commands as the Windows/WSL2 box above, plus install Docker Engine: <https://docs.docker.com/engine/install/ubuntu/> (then `sudo usermod -aG docker $USER` and log out/in).
</details>

### A3. Log the AWS CLI in

```bash
aws configure
```
Paste your **Access key ID** and **Secret access key**, region `us-east-1`, output `json`.

Check it worked:
```bash
aws sts get-caller-identity
```
✅ You should see your account number and `arn:aws:iam::...:user/eks-learner`.

### A4. Get the project

Unzip `shopverse-eks.zip` somewhere easy, open a terminal **inside** the folder, and make the scripts executable:

```bash
cd ~/shopverse-eks          # or wherever you unzipped it
chmod +x scripts/*.sh
```

> **Recommended:** put it in your own GitHub repository now (you'll need it for CI/CD and GitOps later):
> create an **empty** repo named `shopverse-eks` on GitHub, then:
> ```bash
> git init -b main && git add -A && git commit -m "Initial import of ShopVerse"
> git remote add origin https://github.com/<your-user>/shopverse-eks.git
> git push -u origin main
> ```

### A5. (Optional, free) Run everything on your laptop first

This proves the app works before you spend any money:

```bash
docker compose up --build -d     # first time ~5 minutes
```
Open <http://localhost:8080>, create an account, add items to your cart and place an order. Stop it with `docker compose down -v`.

---

## Part B — Deploy to Amazon EKS

> Run every command from the project folder. If a step fails, read the red message — it usually tells you exactly what to do. More help: [06-TROUBLESHOOTING.md](06-TROUBLESHOOTING.md).

### Step 0 — Check your setup

```bash
./scripts/00-check-prereqs.sh
```
✅ Every line shows a green ✔ and ends with **All good!**
❌ If a tool is missing, the script prints how to install it.

### Step 1 — Create the infrastructure (15–20 min)

*Optional:* to change region or node type, copy the example settings and edit them:
```bash
cp infra/terraform/terraform.tfvars.example infra/terraform/terraform.tfvars
```

Now create everything:
```bash
./scripts/01-create-infrastructure.sh
```
- It shows the cost and asks you to confirm — type `y`.
- Terraform prints a long plan (~90 resources) and asks **"Do you want to perform these actions?"** — type `yes`.

**What is being built:** a VPC across 3 Availability Zones, an EKS cluster (Kubernetes 1.36), 3 worker nodes (Spot instances), 10 private image registries (ECR), IAM roles for the add-ons (EKS Pod Identity), and randomly generated passwords stored in AWS Secrets Manager.

✅ At the end you see three nodes with status `Ready`:
```
NAME                         STATUS   ROLES    AGE   VERSION
ip-10-0-12-34.ec2.internal   Ready    <none>   2m    v1.36.x-eks-...
```

### Step 2 — Configure the project for your account (10 sec)

```bash
./scripts/02-configure-repo.sh
```
✅ Two green ✔ lines: it filled in your image registry address and region.

If you're using Git, save the change: `git add -A && git commit -m "Configure for my AWS account" && git push`

### Step 3 — Install the platform add-ons (8–10 min)

```bash
./scripts/03-install-platform.sh
```
This installs five things every production cluster has:

| Add-on | Why |
|---|---|
| AWS Load Balancer Controller | Creates the internet-facing load balancer for the store |
| metrics-server | CPU/memory numbers for `kubectl top` and autoscaling |
| External Secrets Operator | Copies the passwords from AWS Secrets Manager into Kubernetes |
| kube-prometheus-stack | Prometheus (metrics), Alertmanager (alerts), Grafana (dashboards) |
| Argo CD | GitOps: deploys whatever is in your Git repository |

✅ It ends with **Platform ready** and a table of 5 Helm releases with status `deployed`.

### Step 4 — Build and upload the container images (10–15 min)

Make sure Docker Desktop is running, then:
```bash
./scripts/04-build-and-push-images.sh
```
Each of the 10 services is built (`[1/10] frontend …`) and pushed to your ECR registry. The new image tag is saved into `deploy/helm/values/<service>.yaml`.

✅ Ends with **All images pushed**.
💡 On a Mac with Apple Silicon this is still fine: the images are built for the cluster's Intel/AMD nodes automatically.

### Step 5 — Deploy ShopVerse (5 min)

```bash
./scripts/05-deploy-app.sh
```
It deploys PostgreSQL, Redis and RabbitMQ first, then the 10 microservices, waits for them to be healthy, and waits for the load balancer.

✅ It ends with:
```
==> ShopVerse is live 🎉
    Open: http://k8s-shopverse-xxxxxxxx-1234567890.us-east-1.elb.amazonaws.com
```
Open that link. **If it doesn't load yet, wait 2–3 minutes** — brand-new load balancer addresses take a moment to appear in DNS.

---

## Part C — Explore what you built

1. **Use the store.** Create an account (any made-up email), add products, check out with the *always approved* test card, then try the *always declined* card. Look at **Orders** and **Inbox**.
2. **Open the Platform page** (top menu). It shows every microservice, its version and which pod answered. Refresh a few times — the pod names change: that's Kubernetes load-balancing across replicas.
3. **See it from Kubernetes:**
   ```bash
   ./scripts/status.sh
   kubectl get pods -n shopverse
   kubectl logs -n shopverse deploy/order-service --tail=20
   ```
4. **Open the dashboards** (keep this terminal open; Ctrl+C closes them):
   ```bash
   ./scripts/08-open-dashboards.sh
   ```
   | UI | Address | Login |
   |---|---|---|
   | Grafana | <http://localhost:3000> | printed by the script |
   | Argo CD | <http://localhost:8080> | printed by the script |
   | Prometheus | <http://localhost:9090> | — |
   | RabbitMQ | <http://localhost:15672> | printed by the script |

   In Grafana: *Dashboards → ShopVerse — Microservices Overview*.
5. **Generate traffic and watch autoscaling** (in a second terminal):
   ```bash
   ./scripts/07-run-load-test.sh 30 5m
   kubectl get hpa -n shopverse -w     # in another terminal: watch replicas grow
   ```
6. **Do the labs:** [05-HANDS-ON-LABS.md](05-HANDS-ON-LABS.md) has 20 guided exercises — scaling, breaking things on purpose, fixing them, GitOps, chaos engineering, upgrades.

---

## Part D — CI/CD and GitOps (the "real job" workflow)

Once the basics work, switch to the way production teams deploy: **push code → pipeline builds and scans images → Argo CD deploys automatically**. Full walkthrough: [03-CICD-GITOPS.md](03-CICD-GITOPS.md). Short version:

1. Push the project to your GitHub repo (step A4).
2. Add `github_repository = "<your-user>/shopverse-eks"` to `infra/terraform/terraform.tfvars` and run `./scripts/01-create-infrastructure.sh` again (it only adds the GitHub role).
3. In GitHub → repo *Settings → Secrets and variables → Actions → Variables*, add `AWS_REGION` and `AWS_ROLE_ARN` (printed by `./scripts/02-configure-repo.sh`).
4. Commit and push everything, then hand deployments to Argo CD:
   ```bash
   ./scripts/06-setup-gitops.sh https://github.com/<your-user>/shopverse-eks.git
   ```
5. Change something (e.g. the headline in `services/frontend/public/app.js`), commit, push — and watch GitHub Actions and Argo CD roll it out.

---

## Part E — Destroy everything (do this every time you finish!)

```bash
./scripts/99-destroy-everything.sh
```
Type the cluster name (`shopverse-eks`) to confirm. It removes the load balancer and disks first (so nothing is left behind), then runs `terraform destroy`. Takes ~15–20 minutes.

✅ Ends with **Everything is deleted**. Check the AWS console → *EC2 → Load Balancers* and *EC2 → Volumes* are empty, and your Billing page the next day.

**Next time:** run steps 1 → 5 again (≈40 minutes). Your code and image tags are still in the folder; the images are rebuilt in step 4 because the registries were deleted.

---

## FAQ

**Can I stop it overnight without destroying it?** Not really — the EKS control plane, NAT gateway and load balancer charge even with zero nodes. Destroying and recreating is the cheapest option and great practice.

**Do I need a domain name?** No. You get a free AWS load balancer address. To use your own domain with HTTPS, see Lab 20 in [05-HANDS-ON-LABS.md](05-HANDS-ON-LABS.md).

**Where are the passwords?** Generated by Terraform and stored in AWS Secrets Manager (`shopverse/app-secrets`). Nothing secret is in the code or in Git.

**Something failed halfway — can I just run the script again?** Yes. Every script is safe to re-run; it continues where it left off.

**Terraform says "UnsupportedAvailabilityZone" or "InsufficientInstanceCapacity" / Spot unavailable.** Set `node_capacity_type = "ON_DEMAND"` in `terraform.tfvars` and run step 1 again.

**Brand-new AWS account: nodes never appear.** New accounts can have a low EC2 vCPU quota. See "Nodes never join" in [06-TROUBLESHOOTING.md](06-TROUBLESHOOTING.md).
