# Troubleshooting runbook

Start every investigation with the same three commands:

```bash
./scripts/status.sh                                            # what's not healthy?
kubectl -n <namespace> describe pod <pod>                      # read the Events at the bottom
kubectl -n <namespace> logs <pod> [--previous]                 # what did the app say?
```
and `kubectl get events -A --sort-by=.lastTimestamp | tail -30` for a cluster-wide view.

---

## Terraform / infrastructure

### `Error: creating EKS Node Group ... InsufficientInstanceCapacity` or Spot capacity errors
Spot capacity for those instance types is short in that AZ. Set `node_capacity_type = "ON_DEMAND"` in `infra/terraform/terraform.tfvars` and re-run step 1 (or try another region).

### Nodes never join / node group stuck "Creating" for 20+ minutes
Brand-new AWS accounts often have a low **EC2 vCPU quota**. Check: Console → **Service Quotas** → *Amazon EC2* → "Running On-Demand Standard (A, C, D, H, I, M, R, T, Z) instances" and "All Standard … Spot Instance Requests". You need at least **6 vCPUs** (3 nodes × 2). Request an increase (usually approved within minutes to hours), or set `node_desired_size = 2`.

### `EntityAlreadyExists: Provider with url https://token.actions.githubusercontent.com already exists`
Your account already has the GitHub OIDC provider. Add `create_github_oidc_provider = false` to `terraform.tfvars`.

### `ResourceExistsException: ... secret shopverse/app-secrets ... scheduled for deletion`
A previous secret with that name is pending deletion. Force-delete it, then re-apply:
```bash
aws secretsmanager delete-secret --secret-id shopverse/app-secrets --force-delete-without-recovery
```

### `Error acquiring the state lock`
A previous Terraform run was interrupted. If you're sure nothing else is running: `terraform -chdir=infra/terraform force-unlock <LOCK_ID>`.

### `terraform destroy` fails deleting the VPC / subnets / internet gateway (`DependencyViolation`)
Something Kubernetes created still exists (load balancer, ENI, security group). Run `./scripts/99-destroy-everything.sh` again — it cleans these first. If it still fails: EC2 console → *Load Balancers*, *Target Groups*, *Security Groups* (names starting `k8s-`), *Network Interfaces* filtered by the VPC → delete leftovers → re-run.

### kubectl: `the server has asked for the client to provide credentials` / `You must be logged in`
Your kubeconfig points at a deleted cluster or your AWS session expired.
```bash
aws sts get-caller-identity
aws eks update-kubeconfig --region us-east-1 --name shopverse-eks
```
Only the IAM identity that ran `terraform apply` is cluster admin. To add someone else, use an EKS **access entry** (`access_entries` in `eks.tf`).

---

## Building images (step 4)

| Symptom | Fix |
|---|---|
| `Cannot connect to the Docker daemon` | Start Docker Desktop (WSL2: enable *WSL integration* for Ubuntu). |
| `no basic auth credentials` / `denied` when pushing | The ECR login expired (12 h). Re-run the script — it logs in first. |
| `toomanyrequests` from Docker Hub | Anonymous Docker Hub pull limit. Run `docker login` with a free Docker account and retry. |
| `npm ci` fails: lockfile out of sync | You edited `package.json` without updating the lockfile. In that service folder run `npm install`, commit `package-lock.json`. |
| Go: `checksum mismatch` / `missing go.sum entry` | In that service folder run `go mod tidy`, commit `go.sum`. |
| Very slow on an Apple Silicon Mac | Expected for the Node/Python images the first time (amd64 emulation). Later builds use the cache. Or let GitHub Actions build them (docs/03). |

---

## Pods

### `Pending`
`describe pod` → Events:
| Message | Meaning | Fix |
|---|---|---|
| `Insufficient cpu` / `Insufficient memory` | Nodes are full | Lower requests, add nodes (`node_desired_size`), or add Karpenter |
| `Too many pods` | Per-node IP/pod limit reached (~29–35 on these instance types) | More/larger nodes |
| `volume node affinity conflict` | EBS volume is in another AZ than any free node | Have a node in that AZ (Lab 17) |
| `pod has unbound immediate PersistentVolumeClaims` | PVC can't be provisioned | See "PVC Pending" below |

### `ImagePullBackOff` / `ErrImagePull`
- Image tag not in ECR → `aws ecr list-images --repository-name shopverse/<service>` and re-run `./scripts/04-build-and-push-images.sh <service>`.
- Registry still shows `ACCOUNT_ID.dkr.ecr.REGION...` → you skipped `./scripts/02-configure-repo.sh`.

### `CrashLoopBackOff`
`kubectl -n shopverse logs <pod> --previous`. Common causes here:
- `password authentication failed` → DB password mismatch. If you recreated the secret but kept the old PostgreSQL volume, the roles still have the old passwords: delete the data store and let it re-initialise (**destroys data**): `kubectl delete -k deploy/k8s/data && kubectl -n shopverse-data delete pvc --all && kubectl apply -k deploy/k8s/data`.
- `exec format error` → image built for the wrong CPU architecture (must be `linux/amd64`).
- `JWT_SECRET environment variable is required` → the `shopverse-secrets` Secret is missing (see External Secrets below).

### `CreateContainerConfigError`
Usually `secret "shopverse-secrets" not found` or a missing key. Check `kubectl get externalsecret -A`.

### `OOMKilled` (exit code 137)
Memory limit too low or a leak. Raise `resources.limits.memory` in the service's values file.

### Pod rejected: `violates PodSecurity "restricted:latest"`
The `shopverse` namespace enforces the *restricted* Pod Security Standard. Your pod must set `runAsNonRoot: true`, `allowPrivilegeEscalation: false`, drop `ALL` capabilities and use `seccompProfile: RuntimeDefault`. Copy the security context from `deploy/k8s/debug/debug-toolbox.yaml`.

---

## Networking and the load balancer

### No ALB address (`kubectl get ingress -n shopverse` shows an empty ADDRESS)
```bash
kubectl -n kube-system logs deploy/aws-load-balancer-controller --tail=50
kubectl -n shopverse describe ingress frontend
```
- `AccessDenied` → the Pod Identity association for `aws-load-balancer-controller` is missing: `aws eks list-pod-identity-associations --cluster-name shopverse-eks`.
- `couldn't auto-discover subnets` → public subnets lack the `kubernetes.io/role/elb=1` tag (Terraform adds it — did you change `vpc.tf`?).
- `failed to get VPC ID` → the controller must receive `vpcId` and `region` (step 3 passes them).

### The URL doesn't open right after deployment
New load-balancer DNS names take 1–3 minutes to propagate. Test: `nslookup <alb-hostname>`. If it resolves but shows **502/503**, targets are unhealthy: EC2 → *Target Groups* → *Targets* tab, and check the pods are `Ready`.

### `502 Bad Gateway` from the ALB under load
Usually pods being killed while the ALB still sends traffic. Our chart prevents this with `preStop` sleep + graceful shutdown + `keepAliveTimeout` > ALB idle timeout. If you changed those, put them back.

### A service can't reach another one (timeouts)
Probably a NetworkPolicy. Check who is allowed: `kubectl -n shopverse get netpol <service> -o yaml`. Add the caller to `networkPolicy.allowFrom` in the target's values file.

---

## Storage

### PVC `Pending`
```bash
kubectl -n shopverse-data describe pvc
kubectl -n kube-system get pods -l app.kubernetes.io/name=aws-ebs-csi-driver
```
- `storageclass "gp3" not found` → run `kubectl apply -k deploy/k8s/platform`.
- `waiting for first consumer` → normal until the pod is scheduled (WaitForFirstConsumer).
- CSI controller `AccessDenied` → check the EBS CSI add-on's Pod Identity association.

---

## Secrets

### `ExternalSecret` not `Ready` (`SecretSyncedError`)
```bash
kubectl -n shopverse describe externalsecret shopverse-secrets
kubectl -n external-secrets logs deploy/external-secrets --tail=50
```
- `AccessDeniedException` → Pod Identity association `external-secrets/external-secrets` missing, or the operator pods started **before** the association existed: `kubectl -n external-secrets rollout restart deploy`.
- `region` errors → you skipped `./scripts/02-configure-repo.sh` (ClusterSecretStore still says `AWS_REGION`).
- `secret not found` → the Terraform secret name is `shopverse/app-secrets`; check it exists in the region you deployed to.

---

## Application symptoms

### High error rate
1. Grafana → *5xx error ratio by service* → which service?
2. `kubectl -n shopverse logs -l app.kubernetes.io/name=<service> --tail=200 | jq -c 'select(.level=="error")'`
3. Is it chaos you forgot to remove? `kubectl -n shopverse get deploy <service> -o yaml | grep -A1 CHAOS`
4. Is a dependency down? Platform page / `kubectl -n shopverse-data get pods`.
5. Recent deploy? `helm history <service> -n shopverse` or Argo CD history → roll back.

### Checkout always fails with "temporarily unavailable"
One of cart/product/inventory/payment is down or blocked. The error message names the service. Check its pods and NetworkPolicy.

### Notifications never show up
- Is RabbitMQ up? `kubectl -n shopverse-data get pods`
- Grafana *Outbox backlog* > 0 → publisher can't reach RabbitMQ (check order/user-service logs).
- `notification-service` readiness shows `rabbitmq: disconnected` → check its logs.
- Messages in `notification-service.dlq` → they failed processing; inspect them in the RabbitMQ UI.

### Grafana: "No data"
- Select the *Prometheus* data source at the top of the dashboard.
- Prometheus → *Status → Targets*: are the `shopverse` targets **UP**? If not, check the ServiceMonitors exist (`kubectl -n shopverse get servicemonitors`) and that the monitoring namespace is allowed by the NetworkPolicy.

### Argo CD application `OutOfSync` / `Degraded`
Open it in the UI → the red resource tells you why. Common: repo not reachable (private repo without credentials), a values file with a typo (Helm render error is shown under *Conditions*), or images that don't exist yet (build them first).
