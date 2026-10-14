# Hands-on labs

Twenty exercises that turn "I watched a tutorial" into "I've done this". Each lab has a **goal**, **steps**, **what to observe**, **questions** to answer for yourself, and how to **reset**. Break/fix labs hide the solution in a collapsible block — try to solve them first, exactly like an on-call engineer would.

**Before you start**
- Finish the deployment guide (steps 0–5). Keep the store open in a browser tab, especially the **Platform** page.
- Keep a second terminal running `./scripts/08-open-dashboards.sh` so Grafana is at <http://localhost:3000>.
- Set a shortcut for the store URL:
  ```bash
  export STORE=http://$(kubectl -n shopverse get ingress frontend -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
  echo $STORE
  ```

> **Helm mode vs GitOps mode.** Labs 1–11 and 13–18 use `kubectl` to change things directly. If you already enabled Argo CD (step 6), it will undo those changes within seconds (that's its job!). Either do these labs before step 6, or pause auto-sync for the app you're breaking:
> ```bash
> kubectl -n argocd patch application <service> --type merge -p '{"spec":{"syncPolicy":{"automated":null}}}'   # pause
> kubectl -n argocd patch application <service> --type merge -p '{"spec":{"syncPolicy":{"automated":{"prune":true,"selfHeal":true}}}}'   # resume
> ```

| # | Lab | Skills |
|---|---|---|
| 1 | [Find your way around](#lab-1--find-your-way-around) | kubectl basics, events, ephemeral debugging |
| 2 | [Replicas and load balancing](#lab-2--replicas-and-load-balancing) | Deployments, Services, endpoints |
| 3 | [Autoscaling under load](#lab-3--autoscaling-under-load) | HPA, metrics-server, k6 |
| 4 | [Self-healing](#lab-4--self-healing) | ReplicaSets, probes |
| 5 | [Zero-downtime release and rollback](#lab-5--zero-downtime-release-and-rollback) | rolling updates, Helm history |
| 6 | [Break/fix: CrashLoopBackOff](#lab-6--breakfix-crashloopbackoff) | logs, `--previous`, rollout undo |
| 7 | [Break/fix: ImagePullBackOff](#lab-7--breakfix-imagepullbackoff) | events, ECR |
| 8 | [Break/fix: OOMKilled](#lab-8--breakfix-oomkilled) | requests vs limits |
| 9 | [Break/fix: Pending pods](#lab-9--breakfix-pending-pods) | scheduler, capacity |
| 10 | [Readiness vs liveness](#lab-10--readiness-vs-liveness) | probes, EndpointSlices |
| 11 | [Zero-trust networking](#lab-11--zero-trust-networking) | NetworkPolicies |
| 12 | [GitOps drift and self-heal](#lab-12--gitops-drift-and-self-heal) | Argo CD |
| 13 | [Chaos engineering and alerts](#lab-13--chaos-engineering-and-alerts) | Prometheus alerts, circuit breakers, sagas |
| 14 | [Graceful degradation](#lab-14--graceful-degradation) | resilience patterns |
| 15 | [Event-driven durability](#lab-15--event-driven-durability) | RabbitMQ, outbox, DLQ |
| 16 | [Trace a checkout](#lab-16--trace-a-checkout) | request IDs, saga compensation |
| 17 | [Node failure, drains and PDBs](#lab-17--node-failure-drains-and-pdbs) | PDBs, EBS zone affinity |
| 18 | [Rotate a secret](#lab-18--rotate-a-secret) | Secrets Manager, External Secrets |
| 19 | [Upgrade the cluster to 1.37](#lab-19--upgrade-the-cluster-to-137) | EKS upgrades with Terraform |
| 20 | [Stretch goals](#lab-20--stretch-goals) | Karpenter, HTTPS, RDS, tracing, canaries… |

---

## Lab 1 — Find your way around

**Goal:** get fluent with the commands you'll use every day.

```bash
kubectl get nodes -o wide                              # which nodes, which AZ/instance type
kubectl get ns
kubectl get pods -A -o wide                            # every pod, on which node
kubectl -n shopverse get deploy,svc,hpa,pdb,netpol,ingress
kubectl -n shopverse describe deploy cart-service      # read it top to bottom
kubectl -n shopverse get events --sort-by=.lastTimestamp | tail -20
kubectl top nodes && kubectl -n shopverse top pods
kubectl -n shopverse get pod -l app.kubernetes.io/name=cart-service -o yaml | less   # the full pod spec
helm list -A
```

**Get inside the network.** The Go images are *distroless* (no shell — on purpose, for security), so you can't `exec` into them. Use the debug toolbox pod instead:
```bash
kubectl apply -f deploy/k8s/debug/debug-toolbox.yaml
kubectl -n shopverse exec -it debug-toolbox -- sh
  nslookup product-service                                # Kubernetes DNS
  curl -s http://api-gateway/api/products | head -c 300   # call a Service by name
  curl -s http://api-gateway/metrics | head -20           # Prometheus metrics format
  exit
```

**Questions:** How many pods run per node? Which namespace uses the most memory? What is the `checksum/config` annotation on the cart-service pods for?

---

## Lab 2 — Replicas and load balancing

**Goal:** see a Service spread traffic over pods.

1. Raise product-service to 4 replicas. The HPA owns the replica count, so change its minimum:
   ```bash
   kubectl -n shopverse patch hpa product-service -p '{"spec":{"minReplicas":4}}'
   kubectl -n shopverse get pods -l app.kubernetes.io/name=product-service -o wide -w
   ```
2. Look at the Service's endpoints — one IP per ready pod:
   ```bash
   kubectl -n shopverse get endpointslices -l kubernetes.io/service-name=product-service
   ```
3. Hit the API repeatedly and watch which pod answers:
   ```bash
   for i in $(seq 1 10); do curl -s $STORE/api/status | jq -r '.services[] | select(.name=="product-service") | .pod'; done
   ```

**Observe:** pods land on different nodes/AZs (topology spread). Requests rotate between pods.
**Questions:** Why does every pod get a VPC IP address (`kubectl get pods -o wide`)? What would happen if you used `kubectl scale` instead of patching the HPA?
**Reset:** `kubectl -n shopverse patch hpa product-service -p '{"spec":{"minReplicas":2}}'`

---

## Lab 3 — Autoscaling under load

**Goal:** watch the Horizontal Pod Autoscaler add pods when CPU rises.

```bash
# terminal 1
kubectl -n shopverse get hpa -w
# terminal 2
./scripts/07-run-load-test.sh 50 8m
# terminal 3 (optional)
watch -n 5 kubectl -n shopverse top pods
```
In Grafana watch *Requests/sec*, *CPU by pod* and *Available replicas by deployment*.

**Observe:** `TARGETS` (e.g. `cpu: 85%/70%`) climb, replicas increase in steps; after the test, replicas shrink slowly, one pod per minute after a 2-minute wait.
**Questions:** Which service scales first, and why that one (look at *CPU by pod*)? Why is scale-down deliberately slow (`behavior.scaleDown` in `hpa.yaml`)? Did any pod stay `Pending`? (That's when you'd need Cluster Autoscaler or Karpenter — Lab 20.) Read the k6 summary: what was the p95 latency, and did the thresholds pass?

---

## Lab 4 — Self-healing

**Goal:** prove Kubernetes keeps the desired state.

```bash
./scripts/chaos.sh cart-service kill
kubectl -n shopverse get pods -l app.kubernetes.io/name=cart-service -w
```
Delete *all* pods of a service at once and keep refreshing the store:
```bash
kubectl -n shopverse delete pods -l app.kubernetes.io/name=recommendation-service
```
**Observe:** the ReplicaSet creates replacements immediately; new pods only receive traffic once `/readyz` passes.
**Questions:** Who recreated the pod — the Deployment, the ReplicaSet or the scheduler? Why doesn't the PodDisruptionBudget stop `kubectl delete pod`? (It only governs *evictions*: drains, upgrades.)

---

## Lab 5 — Zero-downtime release and rollback

**Goal:** ship a new version without a single failed request, then roll back.

1. In another terminal, start a request loop and leave it running:
   ```bash
   while true; do printf "%s " "$(curl -s -o /dev/null -w '%{http_code}' $STORE/)"; sleep 0.2; done
   ```
2. Change the headline in `services/frontend/public/app.js` (`Gear for builders.` → anything), then:
   ```bash
   ./scripts/04-build-and-push-images.sh frontend
   ./scripts/05-deploy-app.sh frontend
   kubectl -n shopverse rollout status deploy/frontend
   ```
3. History and rollback:
   ```bash
   helm history frontend -n shopverse
   helm rollback frontend -n shopverse          # back to the previous revision
   kubectl -n shopverse rollout history deploy/frontend
   ```

**Observe:** the loop prints only `200`s throughout. That's `maxUnavailable: 0` + readiness probes + `preStop` sleep + graceful shutdown working together.
**Questions:** Why do we sleep 5 s in `preStop` before the app receives SIGTERM? What does `kubectl get rs -n shopverse -l app.kubernetes.io/name=frontend` show during the rollout?

---

## Lab 6 — Break/fix: CrashLoopBackOff

**Break it:**
```bash
kubectl -n shopverse set env deploy/user-service DB_PASSWORD=wrong-password
```
Customers report they can't sign up after "the last deploy". Wait ~3 minutes, then investigate. Use only `get`, `describe`, `logs`.

<details><summary>Hints</summary>

- `kubectl -n shopverse get pods -l app.kubernetes.io/name=user-service` — look at READY, STATUS, RESTARTS.
- `kubectl -n shopverse logs <new-pod> --previous` shows the crashed container's output.
- Why are the old pods still running?
</details>

<details><summary>Solution</summary>

The new pods log `password authentication failed for user "user_service"`, retry for ~2.5 minutes, then exit; the startup probe also gives up → `CrashLoopBackOff`.

The old pods keep serving because the rollout uses `maxUnavailable: 0`: Kubernetes never removes a healthy pod until its replacement is ready. **A bad deploy didn't cause an outage** — sign-ups worked whenever the old pods handled the request.

Fix: `kubectl -n shopverse rollout undo deploy/user-service` (or `kubectl -n shopverse set env deploy/user-service DB_PASSWORD-` to remove the override).
</details>

---

## Lab 7 — Break/fix: ImagePullBackOff

**Break it:**
```bash
REG=$(terraform -chdir=infra/terraform output -raw ecr_registry)
kubectl -n shopverse set image deploy/cart-service cart-service=$REG/shopverse/cart-service:v9.9.9
```
<details><summary>Solution</summary>

`kubectl -n shopverse describe pod <new-pod>` → Events: `Failed to pull image ... not found` → `ImagePullBackOff`. The tag doesn't exist in ECR (`aws ecr list-images --repository-name shopverse/cart-service`).
Fix: `kubectl -n shopverse rollout undo deploy/cart-service`.

Other real-world causes of this error: the node role lacks ECR permissions, a private registry needs an `imagePullSecret`, or a typo in the registry host. A cousin error is `exec format error` in `CrashLoopBackOff`: an arm64 image on amd64 nodes (that's why the build script forces `--platform linux/amd64`).
</details>

---

## Lab 8 — Break/fix: OOMKilled

**Break it:**
```bash
kubectl -n shopverse set resources deploy/recommendation-service --limits=memory=40Mi
```
<details><summary>Solution</summary>

`kubectl -n shopverse describe pod <pod>` → `Last State: Terminated, Reason: OOMKilled, Exit Code: 137`. The Python process needs ~60 MiB; the kernel kills it when it exceeds the limit. Grafana's *Container restarts* panel spikes.

Fix: `kubectl -n shopverse rollout undo deploy/recommendation-service`.

Lesson: **requests** are what the scheduler reserves (guaranteed); **limits** are the hard ceiling. Exceeding a memory limit kills the container; exceeding a CPU limit only throttles it (that's why our chart sets no CPU limit).
</details>

---

## Lab 9 — Break/fix: Pending pods

**Break it:**
```bash
kubectl -n shopverse set resources deploy/product-service --requests=cpu=3
```
<details><summary>Solution</summary>

The new pod is `Pending`. `kubectl -n shopverse describe pod <pod>` → `FailedScheduling ... 0/3 nodes are available: 3 Insufficient cpu`. No node has 3 free CPUs (they have 2 each). The old pods keep serving (again: `maxUnavailable: 0`).
Fix: `kubectl -n shopverse rollout undo deploy/product-service`.

In production, **Cluster Autoscaler** or **Karpenter** would add a bigger node automatically — see Lab 20.
</details>

---

## Lab 10 — Readiness vs liveness

**Goal:** see why liveness probes must not check dependencies.

```bash
kubectl -n shopverse-data scale statefulset redis --replicas=0
kubectl -n shopverse get pods -l 'app.kubernetes.io/name in (cart-service,notification-service)' -w
```
Check the Platform page, then:
```bash
kubectl -n shopverse get endpointslices -l kubernetes.io/service-name=cart-service
curl -s $STORE/api/status | jq '.services[] | select(.name=="cart-service")'
```
**Observe:** pods become `0/1 READY` (readiness fails → removed from the Service) but **are not restarted** (liveness only checks the process). Browsing still works; the cart shows an error.
**Reset:** `kubectl -n shopverse-data scale statefulset redis --replicas=1` — everything recovers by itself, carts intact (Redis append-only file on an EBS volume).
**Question:** what would happen if `/healthz` also checked Redis? (Every cart pod would restart in a loop — a restart storm that fixes nothing.)

---

## Lab 11 — Zero-trust networking

**Goal:** prove NetworkPolicies block traffic that isn't explicitly allowed.

```bash
# order-service IS allowed to call payment-service
kubectl -n shopverse exec deploy/order-service -- python -c \
  "import urllib.request; print(urllib.request.urlopen('http://payment-service/healthz', timeout=3).read())"

# recommendation-service is NOT
kubectl -n shopverse exec deploy/recommendation-service -- python -c \
  "import urllib.request; print(urllib.request.urlopen('http://payment-service/healthz', timeout=3).read())"

# the debug pod can reach only the internet-facing services
kubectl apply -f deploy/k8s/debug/debug-toolbox.yaml
kubectl -n shopverse exec debug-toolbox -- curl -s -m 3 http://api-gateway/healthz
kubectl -n shopverse exec debug-toolbox -- curl -s -m 3 http://payment-service/healthz || echo BLOCKED
kubectl -n shopverse exec debug-toolbox -- curl -s -m 3 telnet://postgres.shopverse-data:5432 || echo BLOCKED
```
**Observe:** the second command times out; so do the last two.
**Questions:** Open `deploy/helm/values/payment-service.yaml` — which callers are allowed? Delete the `default-deny-ingress` policy (`kubectl -n shopverse delete netpol default-deny-ingress`): is payment-service still protected? (Yes — *any* policy that selects a pod isolates it. The default-deny catches pods with no policy, like the debug pod.)
**Reset:** `kubectl apply -k deploy/k8s/app-extras && kubectl delete -f deploy/k8s/debug/debug-toolbox.yaml`

---

## Lab 12 — GitOps drift and self-heal

*Requires step 6 (Argo CD).*

1. Make a manual change: `kubectl -n shopverse set env deploy/cart-service HELLO=world`
2. Watch Argo CD revert it within seconds: `kubectl -n argocd get application cart-service -w` (or the UI: it flashes *OutOfSync* then *Synced*).
3. Delete something: `kubectl -n shopverse delete svc cart-service` — recreated.
4. Now make the change *properly*: add `HELLO: world` under `env:` in `deploy/helm/values/cart-service.yaml`, commit, push. Argo applies it and keeps it.
5. In the Argo CD UI open `cart-service` → **App diff** and **History**.

**Questions:** What is the source of truth now? How would you roll back the change? Why is `prune: false` on the `shopverse-data` application?

---

## Lab 13 — Chaos engineering and alerts

**Goal:** inject failures, watch the safety nets and alerts react.

**Part A — error rate and alerts**
```bash
./scripts/chaos.sh product-service errors 0.3       # GitOps mode: the script tells you the Git way
./scripts/07-run-load-test.sh 10 6m
```
Watch Grafana (*5xx error ratio by service*), then Prometheus → Alerts: `ShopverseHighErrorRate` goes *pending* then *firing* (~3 min). Open Alertmanager at <http://localhost:9093>.

**Part B — circuit breaker**
```bash
./scripts/chaos.sh product-service errors 1.0
for i in $(seq 1 10); do curl -s -o /dev/null -w "%{http_code} " $STORE/api/products; done; echo
```
After 5 failures the gateway answers `503 ... (circuit open)` instantly instead of calling product-service. Grafana's circuit-breaker panel shows `2`. Turn chaos off and the circuit closes again after ~10 s (half-open trial request).

**Part C — a slow dependency inside a saga**
```bash
./scripts/chaos.sh product-service off
./scripts/chaos.sh payment-service latency 6000
```
Place an order in the store. order-service gives up on payment after 5 s, **refunds defensively, releases the stock** and marks the order FAILED ("payment service unavailable"). Check the order in *Orders*, and the stock: `curl -s "$STORE/api/inventory?ids=1,3,4"`.

**Reset:** `./scripts/chaos.sh payment-service off`
**Questions:** Why is an unknown payment outcome treated as "refund just in case"? Why does the alert wait 2 minutes (`for: 2m`) before firing?

---

## Lab 14 — Graceful degradation

1. `kubectl -n shopverse scale deploy recommendation-service --replicas=0` → open a product: the store works, only the suggestions disappear.
2. Open a few products first (warms the cache), then `kubectl -n shopverse scale deploy product-service --replicas=0`. The catalog page fails (the gateway's circuit opens), **but** recommendations keep working from recommendation-service's stale cache, and the cart shows a "degraded" notice instead of crashing.
3. Check the Platform page and Grafana.

**Reset:** `kubectl -n shopverse scale deploy recommendation-service product-service --replicas=2`
**Question:** list each service's behaviour when product-service is down. Which design choice in each service caused it?

---

## Lab 15 — Event-driven durability

**Part A — consumer down**
```bash
kubectl -n shopverse scale deploy notification-service --replicas=0
```
Place two orders. Open RabbitMQ (<http://localhost:15672>) → *Queues* → `notification-service.events` has **Ready** messages waiting. Now:
```bash
kubectl -n shopverse scale deploy notification-service --replicas=2
```
The queue drains and the notifications appear in the store's *Inbox*. Nothing was lost.

**Part B — broker down (transactional outbox)**
```bash
kubectl -n shopverse-data scale statefulset rabbitmq --replicas=0
```
Register a new user and place an order — **both still succeed**. Grafana → *Outbox backlog* rises (events are safely stored in PostgreSQL). Bring RabbitMQ back (`--replicas=1`), wait ~1 minute: the backlog drains to 0 and the inbox fills.

**Part C — poison message**
In RabbitMQ: *Exchanges* → `shopverse.events` → *Publish message*, routing key `order.confirmed`, payload `this is not json` → *Publish*. It lands in `notification-service.dlq`, and the `ShopverseDeadLetteredEvents` alert fires.

**Questions:** Why are events delivered "at least once" and not "exactly once"? How does notification-service avoid sending a duplicate email?

---

## Lab 16 — Trace a checkout

1. In the store open your browser's dev tools → **Network**. Pay with the **declined** test card.
2. Click the `orders` request → *Response headers* → copy `x-request-id`.
3. Find it in every service:
   ```bash
   RID=<paste-it>
   for s in api-gateway order-service cart-service product-service inventory-service payment-service; do
     echo "== $s"; kubectl -n shopverse logs -l app.kubernetes.io/name=$s --tail=2000 | grep "$RID" | jq -c '{service, msg, path, status}'
   done
   ```
4. Check the stock before and after a declined order: `curl -s $STORE/api/inventory/9`.
5. Try the out-of-stock path: add **5 × "The Little Book of YAML"** (only 3 in stock) and check out.

**Observe:** the saga's steps and its compensation (`/inventory/release`) in order, all sharing one request ID.

---

## Lab 17 — Node failure, drains and PDBs

1. See where pods live: `kubectl get pods -A -o wide | grep -E "shopverse|NAME"`
2. Find the node running **postgres-0** and drain it (what an upgrade or Spot interruption does):
   ```bash
   NODE=$(kubectl -n shopverse-data get pod postgres-0 -o jsonpath='{.spec.nodeName}')
   kubectl drain $NODE --ignore-daemonsets --delete-emptydir-data
   ```
3. Watch the microservices: PDBs ensure at least one pod of each service stays up while others move.
4. Now look at postgres: `kubectl -n shopverse-data get pod postgres-0` → probably **Pending**. `describe` says `volume node affinity conflict`.

**Why?** An EBS volume lives in **one** Availability Zone. With one node per AZ, there is no other node in that AZ, so PostgreSQL can't move. This is exactly why production databases use **RDS Multi-AZ**, or why you'd run more nodes per AZ.

**Reset:** `kubectl uncordon $NODE` → postgres starts again with all its data. (Optional: terminate an instance in the EC2 console and watch the managed node group replace it.)

---

## Lab 18 — Rotate a secret

**Goal:** rotate the JWT signing key the way you would after a leak.

```bash
SECRET=shopverse/app-secrets
NEW=$(LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 48)
aws secretsmanager get-secret-value --secret-id $SECRET --query SecretString --output text \
  | jq --arg v "$NEW" '."jwt-secret"=$v' > /tmp/new-secret.json
aws secretsmanager put-secret-value --secret-id $SECRET --secret-string file:///tmp/new-secret.json && rm /tmp/new-secret.json

# Don't wait for the hourly refresh — force External Secrets to sync now
kubectl -n shopverse annotate externalsecret shopverse-secrets force-sync=$(date +%s) --overwrite
# Environment variables are read at start-up, so restart the consumers
kubectl -n shopverse rollout restart deploy/api-gateway deploy/user-service
```
**Observe:** refresh the store — you're logged out (old tokens are now invalid → 401 → the UI sends you to sign in).
**Questions:** Why must the pods restart? (Look up the *Reloader* project.) Why is rotating a **database** password harder? (The PostgreSQL role must be changed too — `ALTER ROLE` — in step with the secret; don't rotate those casually here.)

---

## Lab 19 — Upgrade the cluster to 1.37

EKS supports each Kubernetes version for ~14 months of standard support; after that you pay extra for extended support. Upgrades are routine work.

1. **Check for removed APIs** with [pluto](https://github.com/FairwindsOps/pluto) or [kubent](https://github.com/doitintl/kube-no-trouble), and read the EKS 1.37 release notes and the add-on compatibility tables.
2. Set the version in `infra/terraform/terraform.tfvars`:
   ```hcl
   kubernetes_version = "1.37"
   ```
3. Keep the request loop from Lab 5 running, then:
   ```bash
   terraform -chdir=infra/terraform apply
   ```
4. Watch the order: control plane (~10 min) → managed add-ons → node group rolls node by node (respecting PDBs):
   ```bash
   kubectl get nodes -w
   ```

**Questions:** Why can't you skip from 1.36 straight to 1.38? Why do PodDisruptionBudgets matter here? What would you check *before* upgrading in production (staging cluster, backups, add-on versions)?

---

## Lab 20 — Stretch goals

No step-by-step here — these are the things senior engineers add next. Each is a great portfolio item.

| Goal | Pointers |
|---|---|
| **Node autoscaling with Karpenter** | `terraform-aws-modules/eks/aws//modules/karpenter` + the module's `examples/karpenter`. Repeat Lab 9 and watch a node appear. |
| **HTTPS + your own domain** | Request an ACM certificate, set `ingress.certificateArn` (and `host`) in `frontend.yaml` and `api-gateway.yaml`, add a Route 53 record (or install ExternalDNS with Pod Identity). |
| **Managed database** | Create Amazon RDS for PostgreSQL in Terraform (private subnets, security group from the node SG), store its credentials in Secrets Manager, point `DB_HOST` at it. |
| **Distributed tracing** | Add OpenTelemetry SDKs to the services, run an OTel Collector, send traces to Grafana Tempo or AWS X-Ray. |
| **Progressive delivery** | Install Argo Rollouts, convert one Deployment into a canary `Rollout` with Prometheus analysis on the error-rate metric. |
| **Policy as code** | Kyverno or Gatekeeper: require resource limits, block `:latest` tags, require the `app.kubernetes.io/name` label. |
| **Backups** | Velero with an S3 bucket (the pod-identity module has `attach_velero_policy`). Back up `shopverse-data`, delete it, restore. |
| **Multiple environments** | Add `values/prod/` overlays and a second ApplicationSet for a `prod` cluster or namespace; promote by PR. |
| **Cost visibility** | Install OpenCost and find the most expensive namespace. |
| **Service mesh** | Istio ambient mode or Linkerd: mTLS between services and L7 authorization (e.g. only `POST /payments/charge` from order-service). |
