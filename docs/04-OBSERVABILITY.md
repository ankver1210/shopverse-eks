# Observability: metrics, logs, alerts

"Observability" means being able to answer *what is broken and why* without guessing. ShopVerse gives you the three things SREs rely on every day: **metrics** (Prometheus + Grafana), **logs** (structured JSON) and **alerts** (Alertmanager), all tied together by a **request ID**.

Open everything with:
```bash
./scripts/08-open-dashboards.sh
```

---

## 1. Grafana dashboard tour

Grafana → **Dashboards → ShopVerse — Microservices Overview**. Use the *Service* drop-down to focus on one or more services.

| Row | Panels | What it tells you |
|---|---|---|
| Golden signals | Requests/sec, Error rate, p95 latency, Ready pods, Orders, Revenue | Is the platform healthy *right now*? |
| RED per service | Rate, Errors, Duration per service, responses by status code | *Which* service is the problem? |
| Kubernetes resources | CPU and memory per pod, available replicas, restarts | Is it a capacity problem? Are pods crashing (OOMKilled)? |
| Business & resilience | Orders/payments by outcome, notifications, circuit breakers, outbox backlog, RabbitMQ queue | Is the business impacted? Are safety mechanisms kicking in? |

kube-prometheus-stack also ships dozens of ready-made dashboards — try *Kubernetes / Compute Resources / Namespace (Pods)* and *Node Exporter / Nodes*.

> **RED method** = **R**ate, **E**rrors, **D**uration. For every service, these three answer "is it working for users?".

---

## 2. PromQL you should know

Paste these into Prometheus (<http://localhost:9090>) or Grafana → *Explore*.

```promql
# Requests per second per service
sum by (service) (rate(http_requests_total{namespace="shopverse"}[5m]))

# Percentage of 5xx errors per service
100 * sum by (service) (rate(http_requests_total{namespace="shopverse",status_code=~"5.."}[5m]))
    / sum by (service) (rate(http_requests_total{namespace="shopverse"}[5m]))

# 95th percentile latency per service (seconds)
histogram_quantile(0.95, sum by (service, le) (rate(http_request_duration_seconds_bucket{namespace="shopverse"}[5m])))

# Slowest routes right now
topk(5, histogram_quantile(0.95, sum by (service, route, le) (rate(http_request_duration_seconds_bucket{namespace="shopverse"}[5m]))))

# CPU used vs requested (above 1 = using more than requested)
sum by (pod) (rate(container_cpu_usage_seconds_total{namespace="shopverse",container!=""}[5m]))
  / sum by (pod) (kube_pod_container_resource_requests{namespace="shopverse",resource="cpu"})

# Which version is running where (great during a rolling update)
count by (service, version) (app_build_info{namespace="shopverse"})

# Orders per minute by outcome
sum by (status) (rate(orders_total[5m])) * 60

# Is any circuit breaker open? (2 = open)
max by (upstream) (gateway_circuit_breaker_state)
```

Pre-computed versions of the first three exist as **recording rules** (`shopverse:http_requests:rate5m`, `shopverse:http_errors:ratio5m`, `shopverse:http_latency_p95:5m`) — see `deploy/k8s/monitoring/prometheus-rules.yaml`.

---

## 3. Logs

Every service writes one JSON object per line:
```json
{"timestamp":"2026-10-13T20:39:29Z","level":"info","service":"order-service","version":"a1b2c3d",
 "pod":"order-service-7c9f-x2k","request_id":"4f1c...","msg":"order confirmed","order_id":"63a1..."}
```

```bash
# Last 50 lines from all pods of a service
kubectl -n shopverse logs deploy/order-service --tail=50

# Follow every pod of a service (label selector)
kubectl -n shopverse logs -l app.kubernetes.io/name=order-service -f --max-log-requests 10

# Only errors (requires jq)
kubectl -n shopverse logs deploy/order-service --tail=500 | jq -c 'select(.level=="error")'

# Logs from the previous (crashed) container
kubectl -n shopverse logs <pod-name> --previous
```

> Tip: install [stern](https://github.com/stern/stern) to tail many pods with colours: `stern -n shopverse order-service`.

### Follow one request across all services

The gateway gives every request an `x-request-id` (or reuses yours) and every service passes it on and logs it.

```bash
# 1. Send a request with your own id
curl -s -H "x-request-id: trace-me-123" http://<your-alb-address>/api/products > /dev/null

# 2. Find it everywhere
for s in api-gateway product-service; do
  kubectl -n shopverse logs -l app.kubernetes.io/name=$s --tail=1000 | grep trace-me-123
done
```
Do the same for a checkout (browser dev tools → Network → the `/api/orders` response header `x-request-id`) and you'll see the log line in gateway, order, cart, product, inventory and payment — that's poor-man's distributed tracing. Real tracing (OpenTelemetry) is a stretch goal in Lab 20.

### Logs in AWS (optional)

To ship container logs to CloudWatch, install the EKS add-on **Amazon CloudWatch Observability** (Container Insights). It costs extra (log ingestion), so it isn't enabled by default. EKS **control plane** logs (API server, audit, authenticator) *are* already going to CloudWatch: Console → CloudWatch → Log groups → `/aws/eks/shopverse-eks/cluster`. Try a Logs Insights query on the audit log to see who deleted a pod.

---

## 4. Alerts

Rules live in `deploy/k8s/monitoring/prometheus-rules.yaml`:

| Alert | Fires when | Severity |
|---|---|---|
| ShopverseHighErrorRate | > 5% of a service's requests are 5xx for 2 min | warning |
| ShopverseHighLatency | p95 > 1 s for 5 min | warning |
| ShopverseServiceDown | Prometheus can't scrape a pod for 1 min | critical |
| ShopverseNoReadyReplicas | a deployment has 0 available pods | critical |
| ShopverseHPAMaxedOut | HPA stuck at max replicas for 10 min | warning |
| ShopverseCircuitBreakerOpen | gateway circuit open for 1 min | warning |
| ShopverseOutboxBacklog | > 50 events waiting to be published for 5 min | warning |
| ShopverseDeadLetteredEvents | messages moved to the DLQ | info |
| ShopversePaymentDeclineSpike | > 50% of payments declined | warning |

See them in Prometheus → *Alerts* (pending → firing) and in Alertmanager (<http://localhost:9093>).

**Make one fire** (Lab 13):
```bash
./scripts/chaos.sh product-service errors 0.3     # 30% of requests fail
./scripts/07-run-load-test.sh 10 5m               # generate traffic
# ~3 minutes later ShopverseHighErrorRate is firing
./scripts/chaos.sh product-service off
```

### Sending alerts somewhere useful

Alertmanager can notify Slack, email, PagerDuty, Opsgenie… Add a receiver to `deploy/platform/kube-prometheus-stack.yaml`, for example Slack:

```yaml
alertmanager:
  config:
    route:
      receiver: slack
      group_by: [alertname, service]
      routes:
        - receiver: "null"
          matchers: [alertname = "Watchdog"]
    receivers:
      - name: "null"
      - name: slack
        slack_configs:
          - api_url: https://hooks.slack.com/services/XXX/YYY/ZZZ   # better: load from a Secret
            channel: "#shopverse-alerts"
            send_resolved: true
```
then re-run `./scripts/03-install-platform.sh`.
