# Cost and cleanup

Everything in AWS is billed by the hour (or second). The single most important habit: **destroy the environment when you stop practising.**

## What it costs (approximate, us-east-1, October 2026)

Prices change and differ per region — confirm on the AWS pricing pages before relying on them.

| Item | Rate | Per hour |
|---|---|---|
| EKS control plane (standard support) | $0.10/h | $0.10 |
| 3 worker nodes, 2 vCPU / 8 GiB, **Spot** | ~$0.025–0.035/h each | ~$0.09 |
| *(same nodes On-Demand)* | ~$0.083/h each | *~$0.25* |
| NAT gateway | $0.045/h + $0.045/GB processed | ~$0.05 |
| Application Load Balancer | ~$0.0225/h + LCUs | ~$0.03 |
| Public IPv4 addresses (NAT + ALB) | $0.005/h each | ~$0.02 |
| EBS gp3 volumes (14 GB) + node root disks | $0.08/GB-month | ~$0.01 |
| Secrets Manager, ECR storage, CloudWatch logs | small | ~$0.01 |
| **Total with Spot** | | **≈ $0.30/hour (~$7/day)** |
| **Total with On-Demand** | | **≈ $0.45/hour (~$11/day)** |

A typical 3-hour practice session costs **about $1**. Leaving it running for a month costs **$200–330**.

> **EKS extended support** costs $0.60/hour for the control plane (6× more). It kicks in automatically when a cluster's Kubernetes version leaves standard support — another reason to keep up with upgrades (Lab 19).

## How this project keeps costs down

- Spot instances by default (`node_capacity_type`)
- One NAT gateway instead of three
- One shared ALB for all Ingresses (IngressGroup)
- Databases inside the cluster instead of RDS/ElastiCache/Amazon MQ
- No persistent volume for Prometheus (2-day retention in memory/disk of the pod)
- ECR lifecycle policy (keeps 30 images), CloudWatch log retention 7 days
- Optional extras (Container Insights, CloudWatch agent) are off

## Protect yourself

1. **Budget alert** (do this once): Console → *Billing and Cost Management* → *Budgets* → *Create budget* → *Monthly cost budget* → e.g. $30 → your email.
2. **Cost Explorer**, filtered by tag `Project = shopverse` (every resource Terraform creates carries it), shows exactly what this project costs. Tags may take 24 h to appear and must be activated once under *Billing → Cost allocation tags*.

## Cleanup

```bash
./scripts/99-destroy-everything.sh
```

What it does, in order (and why the order matters):

1. Deletes Argo CD applications so nothing gets re-created.
2. Deletes the `shopverse` and `shopverse-data` namespaces. This makes the **AWS Load Balancer Controller delete the ALB** and the **EBS CSI driver delete the volumes** — they were created by Kubernetes, not Terraform, so Terraform doesn't know about them.
3. Waits until AWS confirms the load balancer and volumes are gone, and removes leftover `k8s-…` security groups.
4. Runs `terraform destroy` for everything else (cluster, nodes, VPC, NAT, ECR with images, IAM roles, secrets).

### Verify nothing is left (2 minutes)

In the AWS console, region **us-east-1** (or yours):
- [ ] EKS → Clusters: none named `shopverse-eks`
- [ ] EC2 → Instances: no running `shopverse` nodes
- [ ] EC2 → Load Balancers: empty
- [ ] EC2 → Volumes: no `available` volumes left over
- [ ] VPC → NAT gateways: none (or *Deleted*)
- [ ] VPC → Elastic IPs: none unattached
- [ ] Secrets Manager: no `shopverse/app-secrets`

CLI version:
```bash
aws eks list-clusters
aws elbv2 describe-load-balancers --query 'LoadBalancers[].LoadBalancerName'
aws ec2 describe-volumes --filters Name=status,Values=available --query 'Volumes[].VolumeId'
aws ec2 describe-nat-gateways --filter Name=state,Values=available --query 'NatGateways[].NatGatewayId'
aws ec2 describe-addresses --query 'Addresses[?AssociationId==null].PublicIp'
```
All should print `[]`.

If you used the S3 backend, the state bucket remains (a few cents per month) — delete it if you're completely done.
