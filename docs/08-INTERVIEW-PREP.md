# Interview prep: talking about this project

You didn't write the code — and you don't need to pretend you did. What interviewers for DevOps / platform / SRE roles care about is whether you can **deploy, operate, secure, observe and troubleshoot** workloads on Kubernetes. After the labs, you will have done all of that for real. Be honest: *"I used a reference microservices application and built and operated the platform around it on EKS."*

## A 60-second project summary

> "I deployed a 10-service e-commerce platform — Node.js, Python and Go — to Amazon EKS. The infrastructure is Terraform: a three-AZ VPC, EKS 1.36 with managed add-ons, a Spot managed node group, ECR, and EKS Pod Identity so no AWS keys live in the cluster. Every service ships through one reusable Helm chart with probes, HPA, PDBs, topology spread and default-deny network policies under Pod Security *restricted*. Secrets come from AWS Secrets Manager through External Secrets. CI is GitHub Actions with change detection, tests, Trivy scanning and OIDC to ECR; CD is GitOps with an Argo CD ApplicationSet. I monitor it with Prometheus and Grafana — RED dashboards and alerts — and I practised incident response by injecting failures: crash loops, OOM kills, dependency outages, and a cluster upgrade from 1.36 to 1.37 with zero downtime."

## Résumé bullets (pick 3–4, adapt the wording)

- Provisioned a production-style Amazon EKS environment with Terraform (VPC across 3 AZs, managed node groups on Spot, managed add-ons, EKS Pod Identity, ECR, Secrets Manager), cutting compute cost ~65% versus On-Demand.
- Standardised deployment of 10 polyglot microservices with a single reusable Helm chart (probes, HPA, PDB, topology spread, NetworkPolicies, ServiceMonitors) under the Kubernetes *restricted* Pod Security Standard.
- Built a GitOps delivery pipeline: GitHub Actions (path-based change detection, containerised unit tests, Trivy scanning, keyless OIDC to ECR) and Argo CD ApplicationSets with automated sync and self-healing.
- Implemented observability with kube-prometheus-stack: RED-method Grafana dashboards, recording rules and 9 alert rules covering error rate, latency, saturation, circuit breakers and message backlogs.
- Validated resilience through chaos experiments (error/latency injection, dependency and broker outages, node drains) and performed an in-place EKS version upgrade with zero failed requests.

## Questions you can now answer from experience

| Question | Where you did it |
|---|---|
| Liveness vs readiness vs startup probes? What goes wrong if liveness checks the database? | Lab 10, `templates/deployment.yaml` |
| How does a rolling update achieve zero downtime? | Lab 5 — `maxUnavailable: 0`, readiness, `preStop`, SIGTERM handling |
| Requests vs limits? Why no CPU limit? What is OOMKilled? | Lab 8, `values.yaml` |
| How does the HPA decide to scale? Why is scale-down slow? | Lab 3, `templates/hpa.yaml` |
| How do pods get AWS permissions without access keys? IRSA vs Pod Identity? | `infra/terraform/pod-identity.tf` |
| How do you manage secrets in Kubernetes? | External Secrets + Secrets Manager, Lab 18 |
| How does traffic get from the internet to a pod on EKS? | ALB → target-type `ip` → pod IP (VPC CNI), IngressGroup |
| What is a NetworkPolicy and how is it enforced on EKS? | Lab 11, VPC CNI `enableNetworkPolicy` |
| A pod is stuck Pending / CrashLoopBackOff / ImagePullBackOff — walk me through it. | Labs 6, 7, 9, troubleshooting runbook |
| What's a PodDisruptionBudget and when does it apply? | Labs 4 and 17 |
| Why can't a StatefulSet with an EBS volume move to another AZ? | Lab 17 |
| How do you upgrade EKS safely? | Lab 19 |
| What is GitOps? What happens if someone runs `kubectl edit` in prod? | Lab 12 |
| How do you secure a CI/CD pipeline? | OIDC, branch-restricted trust policy, SHA-pinned actions, least-privilege tokens, image scanning |
| What would you change to make this truly production-ready? | Architecture doc §6: RDS Multi-AZ, NAT per AZ, Karpenter, WAF, SSO for UIs, service mesh, backups (Velero), multi-account |
| How do microservices handle a transaction across services? | The checkout saga + transactional outbox (Labs 13, 15, 16) |

## Be ready to draw

Practise sketching on a whiteboard: (1) the AWS network diagram, (2) a request from browser to database, (3) the CI/CD + GitOps flow. All three are in [01-ARCHITECTURE.md](01-ARCHITECTURE.md) and [03-CICD-GITOPS.md](03-CICD-GITOPS.md).
