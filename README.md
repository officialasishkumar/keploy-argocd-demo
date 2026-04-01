# Deploying Keploy with GitOps on Kubernetes

This repo shows how to deploy **Keploy's k8s-proxy** and a sample application to a Kubernetes cluster using **ArgoCD** or **Flux CD** (GitOps), with **Contour** as the ingress controller, for both **staging** and **production** environments.


## How it works

```
┌─────────────────────────────────────────────┐
│  This Git Repo (source of truth)            │
│  ├── environments/staging/    (K8s manifests)│
│  ├── environments/production/ (K8s manifests)│
│  └── argocd/                  (ArgoCD apps)  │
└──────────────────┬──────────────────────────┘
                   │  ArgoCD watches & auto-syncs
                   ▼
┌─────────────────────────────────────────────┐
│  Kubernetes Cluster                          │
│                                              │
│  Namespace: projectcontour                   │
│  └── Contour + Envoy (ingress controller)    │
│       ↓ routes traffic via Ingress/HTTPProxy │
│                                              │
│  Namespace: staging                          │
│  └── sample-order-service (2 replicas)       │
│       ← Ingress: orders.staging.local        │
│                                              │
│  Namespace: keploy                           │
│  └── k8s-proxy (records & replays traffic)   │
│       ← HTTPProxy: <YOUR_INGRESS_HOST>       │
│         (TLS passthrough)                    │
│                                              │
│  Namespace: argocd                           │
│  └── ArgoCD controllers                      │
└─────────────────────────────────────────────┘
```

**ArgoCD** continuously watches this Git repo. When you push a change (e.g. update replicas, change image tag), ArgoCD detects the diff and automatically applies it to the cluster. If someone manually changes the cluster state, ArgoCD self-heals it back to match Git.

**Keploy k8s-proxy** is deployed alongside your app via ArgoCD. It uses a MutatingWebhook to inject an eBPF-based sidecar into your application pods, which captures live traffic without code changes. You can then replay that traffic as automated integration tests from the Keploy UI.

**Contour** is a CNCF ingress controller powered by Envoy proxy. It routes external traffic to both the sample app and the k8s-proxy.


## Why Contour needs special config for Keploy k8s-proxy

The k8s-proxy serves **HTTPS (TLS) natively** on its backend port. This means:

1. A standard Kubernetes `Ingress` resource **won't work** — it only supports plain HTTP backends
2. We need Contour's **HTTPProxy** CRD with **TLS passthrough** — Envoy forwards the encrypted TLS connection directly to the k8s-proxy without terminating or inspecting it

Here's the traffic flow:

```
Client (Keploy cloud / browser)
  │
  │  HTTPS (TLS)
  ▼
Envoy (port 443 / NodePort 30080)    ← TLS passthrough, no termination
  │
  │  TLS connection forwarded as-is
  ▼
k8s-proxy (ClusterIP, port 8080)     ← k8s-proxy terminates TLS itself
```

The **HTTPProxy YAML** you need to create for this is at [`environments/staging/k8s-proxy-httpproxy.yaml`](environments/staging/k8s-proxy-httpproxy.yaml):

```yaml
apiVersion: projectcontour.io/v1
kind: HTTPProxy
metadata:
  name: k8s-proxy-ingress
  namespace: keploy
spec:
  virtualhost:
    fqdn: <YOUR_INGRESS_HOST>   # Must match the host in keploy.ingressUrl
    tls:
      passthrough: true          # Don't terminate TLS — forward encrypted traffic
  tcpproxy:
    services:
      - name: k8s-proxy
        port: 8080
```

The sample-order-service uses a standard `Ingress` (plain HTTP backend) — no special config needed.


## What you need to add for Keploy (with Contour)

If you already use ArgoCD + Contour to deploy your application, here's the **complete list of things to add** for Keploy:

### 1. `keploy-k8s-proxy.yaml` — ArgoCD Application (one per environment)

This tells ArgoCD to deploy the k8s-proxy from its OCI Helm chart. Copy from [`argocd/staging/keploy-k8s-proxy.yaml`](argocd/staging/keploy-k8s-proxy.yaml) and fill in your values:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: keploy-k8s-proxy
  namespace: argocd
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  source:
    chart: k8s-proxy-chart
    repoURL: registry-1.docker.io/keploy
    targetRevision: "3.3.10"
    helm:
      values: |
        replicaCount: 1
        environment: "staging"
        selfHosted: false

        keploy:
          existingSecret: "keploy-credentials"
          existingSecretKey: "access-key"
          clusterName: "<YOUR_CLUSTER_NAME>"
          apiServerUrl: "https://api.staging.keploy.io"   # or https://api.keploy.io for prod
          ingressUrl: "https://<YOUR_INGRESS_HOST>:30080"  # URL where k8s-proxy is reachable

        service:
          type: ClusterIP    # Routed via Contour, not exposed directly

        mongodb:
          enabled: false
        minio:
          enabled: false
  destination:
    server: https://kubernetes.default.svc
    namespace: keploy
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

### 2. `k8s-proxy-httpproxy.yaml` — Contour HTTPProxy (one per environment)

This creates the TLS passthrough route from Contour/Envoy to the k8s-proxy. Copy from [`environments/staging/k8s-proxy-httpproxy.yaml`](environments/staging/k8s-proxy-httpproxy.yaml):

```yaml
apiVersion: projectcontour.io/v1
kind: HTTPProxy
metadata:
  name: k8s-proxy-ingress
  namespace: keploy
spec:
  virtualhost:
    fqdn: <YOUR_INGRESS_HOST>    # Must match the host in keploy.ingressUrl
    tls:
      passthrough: true
  tcpproxy:
    services:
      - name: k8s-proxy
        port: 8080
```

### 3. Kubernetes Secret (one-time, never in Git)

```bash
kubectl create namespace keploy
kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"
```

Get the access key from Keploy UI → Clusters → Connect New Cluster.

### What you do NOT need to change

- Your existing application code — no SDK, no annotations, no sidecar config
- Your existing K8s manifests — no changes to deployments, services, or pods
- Your existing ArgoCD Applications — Keploy runs alongside, not inside, your app


## Repository structure

```
.
├── sample-app/                    # Sample Go REST API (Order Service)
│   ├── main.go
│   ├── go.mod
│   └── Dockerfile
│
├── environments/
│   ├── staging/
│   │   ├── namespace.yaml
│   │   ├── deployment.yaml              # 2 replicas, lower resources
│   │   ├── service.yaml
│   │   ├── ingress.yaml                 # Standard Ingress for sample app (HTTP backend)
│   │   └── k8s-proxy-httpproxy.yaml     # HTTPProxy for k8s-proxy (TLS passthrough)
│   └── production/
│       ├── namespace.yaml
│       ├── deployment.yaml              # 3 replicas, higher resources
│       ├── service.yaml
│       ├── ingress.yaml                 # Standard Ingress for sample app (HTTP backend)
│       └── k8s-proxy-httpproxy.yaml     # HTTPProxy for k8s-proxy (TLS passthrough)
│
├── argocd/                            # ArgoCD deployment manifests
│   ├── staging/
│   │   ├── sample-order-service.yaml    # ArgoCD app → environments/staging/
│   │   ├── keploy-k8s-proxy.yaml        # ArgoCD app → Keploy Helm chart
│   │   └── contour.yaml                 # Contour deployment instructions
│   └── production/
│       ├── sample-order-service.yaml    # ArgoCD app → environments/production/
│       ├── keploy-k8s-proxy.yaml        # ArgoCD app → Keploy Helm chart
│       └── contour.yaml                 # Contour deployment instructions
│
└── flux/                              # Flux CD deployment manifests
    ├── staging/
    │   ├── keploy-source.yaml           # OCI Helm repository for Keploy charts
    │   ├── keploy-k8s-proxy.yaml        # HelmRelease for k8s-proxy
    │   └── k8s-proxy-httpproxy.yaml     # Contour HTTPProxy (TLS passthrough)
    └── production/
        ├── keploy-source.yaml           # OCI Helm repository for Keploy charts
        ├── keploy-k8s-proxy.yaml        # HelmRelease for k8s-proxy
        └── k8s-proxy-httpproxy.yaml     # Contour HTTPProxy (TLS passthrough)
```

### Staging vs Production differences

| Setting | Staging | Production |
|---------|---------|------------|
| App replicas | 2 | 3 |
| CPU limit | 200m | 500m |
| Memory limit | 128Mi | 256Mi |
| k8s-proxy replicas | 1 | 2 |
| Keploy API server | `api.staging.keploy.io` | `api.keploy.io` |
| Envoy service type | NodePort | LoadBalancer |
| k8s-proxy routing | HTTPProxy (TLS passthrough) | HTTPProxy (TLS passthrough) |

---

## Setup Guide

### Prerequisites

- A Kubernetes cluster (Kind, EKS, GKE, AKS, etc.)
- `kubectl` configured to talk to the cluster
- `helm` v3 installed
- ArgoCD installed on the cluster (see Step 2)
- A Keploy account ([app.keploy.io](https://app.keploy.io) for production, [app.staging.keploy.io](https://app.staging.keploy.io) for staging)

### Step 1: Set up a Kind cluster (local/VM only)

> Skip this if you already have a cluster (EKS, GKE, etc.)

```bash
cat <<EOF > kind-config.yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30080
        hostPort: 30080
        protocol: TCP
EOF

kind create cluster --config kind-config.yaml
```

This maps NodePort 30080 on the host. All HTTPS traffic to the k8s-proxy will flow through this port.

### Step 2: Install ArgoCD

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl -n argocd rollout status deployment/argocd-server
```

Get the admin password:

```bash
kubectl -n argocd get secret argocd-initial-admin-secret \
  -o jsonpath="{.data.password}" | base64 -d; echo
```

Access the ArgoCD UI:

```bash
kubectl -n argocd port-forward svc/argocd-server 8443:443 &
# Open https://localhost:8443 in your browser
# Login with username: admin, password: <from above>
```

### Step 3: Deploy Contour ingress controller

Deploy the official Contour manifests:

```bash
kubectl apply -f https://projectcontour.io/quickstart/contour.yaml
```

Wait for it to be ready:

```bash
kubectl -n projectcontour rollout status deployment/contour
kubectl -n projectcontour rollout status daemonset/envoy
```

**For Kind / VM clusters** — patch Envoy so the HTTPS listener (needed for TLS passthrough) uses the mapped NodePort 30080:

```bash
# Swap ports: HTTPS on 30080 (mapped to host), HTTP on 30081
kubectl patch svc envoy -n projectcontour --type='json' -p='[
  {"op": "replace", "path": "/spec/type", "value": "NodePort"},
  {"op": "replace", "path": "/spec/ports/0/nodePort", "value": 30081},
  {"op": "replace", "path": "/spec/ports/1/nodePort", "value": 30080}
]'
```

> **Why swap the ports?** TLS passthrough uses Envoy's HTTPS listener (port 443 inside the cluster). On Kind, only port 30080 is mapped to the host. By assigning Envoy's HTTPS listener to NodePort 30080, TLS passthrough traffic reaches the host. The HTTP listener moves to 30081 (used by standard Ingress resources for the sample app — optional, only needed if you also map 30081 in Kind).

**For cloud clusters (EKS/GKE/AKS)** — skip the patch. The default LoadBalancer type is correct. HTTPS (443) and HTTP (80) both get external IPs automatically.

Verify:

```bash
kubectl get pods -n projectcontour
kubectl get svc  -n projectcontour
# Should show: 80:30081/TCP,443:30080/TCP (Kind) or EXTERNAL-IP (cloud)
```

### Step 4: Build and load the sample app (Kind only)

> Skip if using a container registry. Update `image` and `imagePullPolicy` in the deployment manifests.

```bash
cd sample-app
docker build -t sample-order-service:latest .
kind load docker-image sample-order-service:latest
```

### Step 5: Create the Keploy access key secret

Go to the Keploy UI → **Clusters** → **Connect New Cluster**. Enter your cluster name and ingress URL (e.g. `https://your-host:30080`). You'll get an access key.

Create the secret (**never commit this to Git**):

```bash
kubectl create namespace keploy
kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"
```

### Step 6: Apply the HTTPProxy for k8s-proxy

Edit the `fqdn` in the file to match your ingress hostname, then apply:

```bash
kubectl apply -f environments/staging/k8s-proxy-httpproxy.yaml
```

Verify:

```bash
kubectl get httpproxy -A
# Should show: status "valid"
```

### Step 7: Deploy with ArgoCD

Deploy the sample app and k8s-proxy:

```bash
kubectl apply -f argocd/staging/keploy-k8s-proxy.yaml
kubectl apply -f argocd/staging/sample-order-service.yaml
```

For production:

```bash
kubectl apply -f environments/production/k8s-proxy-httpproxy.yaml
kubectl apply -f argocd/production/keploy-k8s-proxy.yaml
kubectl apply -f argocd/production/sample-order-service.yaml
```

### Step 8: Verify

```bash
# Check ArgoCD apps
kubectl get applications -n argocd

# Check Contour + Envoy
kubectl get pods -n projectcontour

# Check HTTPProxy and Ingress
kubectl get httpproxy -A
kubectl get ingress -A

# Check pods
kubectl get pods -n staging
kubectl get pods -n keploy

# Test k8s-proxy through Contour (TLS passthrough)
curl -sk https://<YOUR_INGRESS_HOST>:30080/healthz
# → {"status":"ok"}

# Test sample app (HTTP, on port 30081 for Kind)
curl -H "Host: orders.staging.local" http://localhost:30081/healthz
# → {"status":"healthy","service":"sample-order-service"}
```

In the ArgoCD UI (`https://localhost:8443`), you'll see your applications with their sync status and a visual resource tree.

---

## Alternative: Deploy with Flux CD

If you use **Flux CD** instead of ArgoCD, the Contour and k8s-proxy configuration is identical — only the GitOps controller changes.

### Step 1: Bootstrap Flux

```bash
flux bootstrap github \
  --owner=<YOUR_GITHUB_USERNAME> \
  --repository=<YOUR_REPO_NAME> \
  --branch=main \
  --path=flux/staging \
  --personal
```

### Step 2: Create the Keploy secret (same as ArgoCD Step 5)

```bash
kubectl create namespace keploy
kubectl -n keploy create secret generic keploy-credentials \
  --from-literal=access-key="<YOUR_ACCESS_KEY>"
```

### Step 3: Push the Flux manifests

Edit the placeholder values in `flux/staging/keploy-k8s-proxy.yaml` and `flux/staging/k8s-proxy-httpproxy.yaml`, then push:

```bash
git add flux/staging/
git commit -m "Add Keploy k8s-proxy via Flux"
git push
```

Flux detects the changes and reconciles:

```bash
flux reconcile source git flux-system
flux get helmreleases -n keploy
kubectl get httpproxy -A
```

### Step 4: Verify (same as ArgoCD Step 8)

```bash
kubectl get pods -n keploy
curl -sk https://<YOUR_INGRESS_HOST>:30080/healthz
# → {"status":"ok"}
```

For a detailed walkthrough, see the [Flux CD deployment guide](https://keploy.io/docs/keploy-cloud/gitops-flux) in the Keploy docs.

---

## Or use Helm directly (without ArgoCD)

If you don't use ArgoCD, deploy the k8s-proxy with Helm:

```bash
helm upgrade --install k8s-proxy oci://docker.io/keploy/k8s-proxy-chart --version 3.3.10 \
  --namespace keploy \
  --create-namespace \
  --set keploy.accessKey="<YOUR_ACCESS_KEY>" \
  --set keploy.clusterName="<YOUR_CLUSTER_NAME>" \
  --set selfHosted=false \
  --set keploy.ingressUrl="https://<YOUR_INGRESS_HOST>:30080" \
  --set service.type=ClusterIP
```

Then apply the HTTPProxy:

```bash
kubectl apply -f environments/staging/k8s-proxy-httpproxy.yaml
```

---

## Integration Testing with Keploy (UI walkthrough)

Once the k8s-proxy is connected, you can record live traffic and replay it as tests — all from the Keploy UI.

### Recording traffic

1. Go to the Keploy UI → **Clusters** → click your cluster
2. Go to the **Deployments** tab
3. Find your deployment (e.g. `sample-order-service` in `staging`)
4. Click the **Record** button (red circle icon)
5. A 3-step wizard opens:
   - **Step 1 - Pods**: Choose how many pods to record from
   - **Step 2 - Record config**: Add filters (path regex, HTTP methods, headers). Configure concurrent sampling rate (default: 5) and static dedup
   - **Step 3 - Auto replay**: Configure automatic replay during recording
6. Click **Start Recording**
7. Send traffic to your application (curl, browser, load test, etc.)
8. The UI shows recorded endpoints in real-time
9. Click **Stop** when done

### Replaying traffic (running tests)

1. After recording, click the **Replay** button next to your deployment
2. Select test sets and configure (API timeout, noise fields, env overrides)
3. Click **Start Replay**
4. The UI shows live progress: passed/failed/noisy counts
5. Review results when complete

### What Keploy does under the hood

1. **Record**: k8s-proxy injects an eBPF agent sidecar into your pods via MutatingWebhook. The agent captures all incoming HTTP requests and outgoing calls (DB queries, external APIs) without code changes
2. **Replay**: k8s-proxy creates an isolated copy of your app, replays the recorded requests, mocks the external dependencies, and compares responses to detect regressions

---

## GitOps workflow

### Deploying a new version

1. Update the image tag in `environments/staging/deployment.yaml`
2. Push to `main`
3. ArgoCD detects the change and rolls out the new version
4. After validating in staging, update `environments/production/deployment.yaml`
5. Push again — ArgoCD deploys to production

### Scaling

1. Change `replicas` in the deployment manifest → push to Git → ArgoCD auto-scales

### Rollback

1. `git revert` the bad commit → push → ArgoCD rolls back the cluster state

ArgoCD also **self-heals**: if someone manually changes the cluster, ArgoCD restores the desired state from Git.
